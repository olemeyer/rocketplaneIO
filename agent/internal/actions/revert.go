package actions

// revert.go — die REVERT-Matrix: für jede reversible Action wird VOR der
// Mutation die inverse Katalog-Action mit den aktuellen (Before-)Werten
// gebaut und mit dem Erfolg an die Control-Plane gemeldet. Die Runs-Seite
// bietet daraus "Revert" an — ein normaler, erneut verifizierter Lauf durch
// dieselbe Whitelist/Validierung (kein Sonderweg).
//
// nil = kein sinnvoller Revert (einweg: restart, delete/evict, cleanup,
// cronjob_trigger, reads).

import (
	"context"
	"encoding/json"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func revertSpecJSON(kind, ns, targetKind, targetName string, params map[string]any) json.RawMessage {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{
		"kind": kind, "targetNamespace": ns, "targetKind": targetKind, "targetName": targetName, "params": params,
	})
	if err != nil {
		return nil
	}
	return b
}

func (r *Runner) prepareRevert(ctx context.Context, a Action) json.RawMessage {
	ns, tk, tn := a.TargetNamespace, a.TargetKind, a.TargetName
	switch a.Kind {
	case "scale":
		st, err := r.readState(ctx, a)
		if err != nil {
			return nil
		}
		return revertSpecJSON("scale", ns, tk, tn, map[string]any{"replicas": st.desired})

	case "set_image":
		imgs, err := r.currentImages(ctx, a)
		if err != nil || len(imgs) == 0 {
			return nil
		}
		var p struct {
			Container string `json:"container"`
		}
		_ = json.Unmarshal(a.Params, &p)
		container := p.Container
		if container == "" {
			if len(imgs) != 1 {
				return nil // mehrdeutig — kein automatischer Revert
			}
			for c := range imgs {
				container = c
			}
		}
		prev, ok := imgs[container]
		if !ok {
			return nil
		}
		params := map[string]any{"image": prev}
		if p.Container != "" {
			params["container"] = p.Container
		}
		return revertSpecJSON("set_image", ns, tk, tn, params)

	case "rollout_pause":
		return revertSpecJSON("rollout_resume", ns, tk, tn, nil)
	case "rollout_resume":
		return revertSpecJSON("rollout_pause", ns, tk, tn, nil)

	case "rollout_undo", "rollout_to_revision":
		// Revert = zurück zur Revision, die VOR dem Lauf aktuell war.
		if tk != "Deployment" {
			return nil
		}
		dep, err := r.clientset.AppsV1().Deployments(ns).Get(ctx, tn, metav1.GetOptions{})
		if err != nil {
			return nil
		}
		rev, err := strconv.ParseInt(dep.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		if err != nil || rev < 1 {
			return nil
		}
		return revertSpecJSON("rollout_to_revision", ns, tk, tn, map[string]any{"revision": rev})

	case "hpa_set":
		min, max, err := r.currentHPABounds(ctx, a)
		if err != nil {
			return nil
		}
		return revertSpecJSON("hpa_set", ns, tk, tn, map[string]any{"minReplicas": min, "maxReplicas": max})
	case "hpa_toggle":
		var p struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(a.Params, &p)
		return revertSpecJSON("hpa_toggle", ns, tk, tn, map[string]any{"enabled": !p.Enabled})

	case "cordon":
		return revertSpecJSON("uncordon", "-", "Node", tn, nil)
	case "uncordon":
		return revertSpecJSON("cordon", "-", "Node", tn, nil)
	case "drain":
		// Partieller Revert: der Node wird wieder schedulable (Pods verteilt
		// Kubernetes selbst neu).
		return revertSpecJSON("uncordon", "-", "Node", tn, nil)

	case "cronjob_suspend":
		return revertSpecJSON("cronjob_resume", ns, tk, tn, nil)
	case "cronjob_resume":
		return revertSpecJSON("cronjob_suspend", ns, tk, tn, nil)

	case "node_taint":
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(a.Params, &p)
		if p.Key == "" {
			return nil
		}
		return revertSpecJSON("node_untaint", "-", "Node", tn, map[string]any{"key": p.Key})
	case "node_untaint":
		// Vorherigen Taint (falls vorhanden) sichern, um ihn wiederherzustellen.
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(a.Params, &p)
		node, err := r.clientset.CoreV1().Nodes().Get(ctx, tn, metav1.GetOptions{})
		if err != nil {
			return nil
		}
		for _, t := range node.Spec.Taints {
			if t.Key == p.Key {
				return revertSpecJSON("node_taint", "-", "Node", tn, map[string]any{"key": t.Key, "value": t.Value, "effect": string(t.Effect)})
			}
		}
		return nil

	case "annotate", "set_label":
		field := "annotations"
		if a.Kind == "set_label" {
			field = "labels"
		}
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(a.Params, &p)
		m, err := r.getMeta(ctx, ns, tk, tn)
		if err != nil || p.Key == "" {
			return nil
		}
		cur := m.Annotations
		if field == "labels" {
			cur = m.Labels
		}
		if prev, had := cur[p.Key]; had {
			return revertSpecJSON(a.Kind, ns, tk, tn, map[string]any{"key": p.Key, "value": prev})
		}
		return revertSpecJSON(a.Kind, ns, tk, tn, map[string]any{"key": p.Key, "remove": true})

	case "patch_configmap":
		var p struct {
			Key string `json:"key"`
		}
		_ = json.Unmarshal(a.Params, &p)
		cm, err := r.clientset.CoreV1().ConfigMaps(ns).Get(ctx, tn, metav1.GetOptions{})
		if err != nil || p.Key == "" {
			return nil
		}
		if prev, had := cm.Data[p.Key]; had {
			return revertSpecJSON("patch_configmap", ns, tk, tn, map[string]any{"key": p.Key, "value": prev})
		}
		return revertSpecJSON("patch_configmap", ns, tk, tn, map[string]any{"key": p.Key, "remove": true})

	case "set_env":
		var p struct {
			Container string `json:"container"`
			Name      string `json:"name"`
		}
		_ = json.Unmarshal(a.Params, &p)
		tmpl, err := r.podTemplate(ctx, ns, tk, tn)
		if err != nil || p.Name == "" {
			return nil
		}
		cn := p.Container
		if cn == "" && len(tmpl.Spec.Containers) == 1 {
			cn = tmpl.Spec.Containers[0].Name
		}
		for i := range tmpl.Spec.Containers {
			if tmpl.Spec.Containers[i].Name != cn {
				continue
			}
			for _, e := range tmpl.Spec.Containers[i].Env {
				if e.Name == p.Name {
					params := map[string]any{"name": p.Name, "value": e.Value}
					if p.Container != "" {
						params["container"] = p.Container
					}
					return revertSpecJSON("set_env", ns, tk, tn, params)
				}
			}
		}
		params := map[string]any{"name": p.Name, "remove": true}
		if p.Container != "" {
			params["container"] = p.Container
		}
		return revertSpecJSON("set_env", ns, tk, tn, params)

	case "set_resources":
		var p struct {
			Container string `json:"container"`
		}
		_ = json.Unmarshal(a.Params, &p)
		tmpl, err := r.podTemplate(ctx, ns, tk, tn)
		if err != nil {
			return nil
		}
		cn := p.Container
		if cn == "" && len(tmpl.Spec.Containers) == 1 {
			cn = tmpl.Spec.Containers[0].Name
		}
		for i := range tmpl.Spec.Containers {
			c := &tmpl.Spec.Containers[i]
			if c.Name != cn {
				continue
			}
			params := map[string]any{}
			if v, ok := c.Resources.Requests["cpu"]; ok {
				params["requestsCpu"] = v.String()
			}
			if v, ok := c.Resources.Requests["memory"]; ok {
				params["requestsMemory"] = v.String()
			}
			if v, ok := c.Resources.Limits["cpu"]; ok {
				params["limitsCpu"] = v.String()
			}
			if v, ok := c.Resources.Limits["memory"]; ok {
				params["limitsMemory"] = v.String()
			}
			if len(params) == 0 {
				return nil // vorher gar keine Resources gesetzt — kein sauberer Revert
			}
			if p.Container != "" {
				params["container"] = p.Container
			}
			return revertSpecJSON("set_resources", ns, tk, tn, params)
		}
		return nil

	case "statefulset_partition":
		sts, err := r.clientset.AppsV1().StatefulSets(ns).Get(ctx, tn, metav1.GetOptions{})
		if err != nil {
			return nil
		}
		var prev int32
		if ru := sts.Spec.UpdateStrategy.RollingUpdate; ru != nil && ru.Partition != nil {
			prev = *ru.Partition
		}
		return revertSpecJSON("statefulset_partition", ns, tk, tn, map[string]any{"partition": prev})

	case "patch_secret":
		// Key-Level-Inverse wie patch_configmap. WICHTIG: der Vorher-Wert wandert
		// in die Revert-Params (DB) — bewusste v1-Entscheidung, dokumentiert:
		// wer patch_secret revertieren will, braucht den Wert.
		var p patchSecretParams
		_ = json.Unmarshal(a.Params, &p)
		sec, err := r.clientset.CoreV1().Secrets(ns).Get(ctx, tn, metav1.GetOptions{})
		if err != nil || p.Key == "" {
			return nil
		}
		if prev, had := sec.Data[p.Key]; had {
			return revertSpecJSON("patch_secret", ns, tk, tn, map[string]any{"key": p.Key, "value": string(prev)})
		}
		return revertSpecJSON("patch_secret", ns, tk, tn, map[string]any{"key": p.Key, "remove": true})

	case "create_configmap":
		return revertSpecJSON("delete_configmap", ns, tk, tn, nil)

	case "delete_configmap":
		// Revert = restore_resource mit dem Before-Snapshot.
		u, err := r.getUnstructured(ctx, "ConfigMap", ns, tn)
		if err != nil {
			return nil
		}
		return revertSpecJSON("restore_resource", ns, "ConfigMap", tn, map[string]any{"snapshot": stripForSnapshot(u)})

	case "pdb_set":
		pdb, err := r.clientset.PolicyV1().PodDisruptionBudgets(ns).Get(ctx, tn, metav1.GetOptions{})
		if err != nil {
			return nil
		}
		if pdb.Spec.MinAvailable != nil {
			return revertSpecJSON("pdb_set", ns, tk, tn, map[string]any{"minAvailable": pdb.Spec.MinAvailable.String()})
		}
		if pdb.Spec.MaxUnavailable != nil {
			return revertSpecJSON("pdb_set", ns, tk, tn, map[string]any{"maxUnavailable": pdb.Spec.MaxUnavailable.String()})
		}
		return nil

	case "patch_resource", "restore_resource":
		// Revert = restore_resource mit dem VOR diesem Lauf gültigen Objekt —
		// auch ein Restore ist damit selbst wieder revertierbar (Kette).
		u, err := r.getUnstructured(ctx, tk, ns, tn)
		if err != nil {
			return nil
		}
		return revertSpecJSON("restore_resource", ns, tk, tn, map[string]any{"snapshot": stripForSnapshot(u)})
	}
	return nil
}
