package actions

// catalog.go — die erweiterten Safe-Actions (Batch 1). Jede folgt demselben
// Contract wie die bestehenden: plan() liefert die trigger→observe→verify-Steps,
// prepareUndo() (in actions.go) snapshottet das Gegenteil VOR der Mutation für
// Auto-Rollback. Reuse der Observer/Verify-Helfer aus actions.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const fieldManager = "rocketplane-agent"

/* ── gemeinsame Metadaten-Helfer (annotate / set_label) ─────────────────── */

// getMeta liest ObjectMeta eines der unterstützten Kinds (für Undo-Snapshot).
func (r *Runner) getMeta(ctx context.Context, ns, kind, name string) (*metav1.ObjectMeta, error) {
	switch kind {
	case "Deployment":
		o, err := r.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "StatefulSet":
		o, err := r.clientset.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "DaemonSet":
		o, err := r.clientset.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "Pod":
		o, err := r.clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "Node":
		o, err := r.clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "Namespace":
		o, err := r.clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "ConfigMap":
		o, err := r.clientset.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "Service":
		o, err := r.clientset.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "PersistentVolumeClaim":
		o, err := r.clientset.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	case "CronJob":
		o, err := r.clientset.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &o.ObjectMeta, nil
	}
	return nil, fmt.Errorf("annotate/label not supported on kind %q", kind)
}

// patchMeta merge-patcht metadata (annotations|labels) auf dem passenden Client.
func (r *Runner) patchMeta(ctx context.Context, ns, kind, name string, patch []byte) error {
	opt := metav1.PatchOptions{FieldManager: fieldManager}
	pt := types.MergePatchType
	var err error
	switch kind {
	case "Deployment":
		_, err = r.clientset.AppsV1().Deployments(ns).Patch(ctx, name, pt, patch, opt)
	case "StatefulSet":
		_, err = r.clientset.AppsV1().StatefulSets(ns).Patch(ctx, name, pt, patch, opt)
	case "DaemonSet":
		_, err = r.clientset.AppsV1().DaemonSets(ns).Patch(ctx, name, pt, patch, opt)
	case "Pod":
		_, err = r.clientset.CoreV1().Pods(ns).Patch(ctx, name, pt, patch, opt)
	case "Node":
		_, err = r.clientset.CoreV1().Nodes().Patch(ctx, name, pt, patch, opt)
	case "Namespace":
		_, err = r.clientset.CoreV1().Namespaces().Patch(ctx, name, pt, patch, opt)
	case "ConfigMap":
		_, err = r.clientset.CoreV1().ConfigMaps(ns).Patch(ctx, name, pt, patch, opt)
	case "Service":
		_, err = r.clientset.CoreV1().Services(ns).Patch(ctx, name, pt, patch, opt)
	case "PersistentVolumeClaim":
		_, err = r.clientset.CoreV1().PersistentVolumeClaims(ns).Patch(ctx, name, pt, patch, opt)
	case "CronJob":
		_, err = r.clientset.BatchV1().CronJobs(ns).Patch(ctx, name, pt, patch, opt)
	default:
		return fmt.Errorf("patch not supported on kind %q", kind)
	}
	return err
}

// metaJSON baut den Merge-Patch für ein einzelnes annotations/labels-Feld.
// value=nil (JSON null) entfernt den Schlüssel.
func metaJSON(field, key string, value *string) []byte {
	var v any
	if value != nil {
		v = *value
	}
	b, _ := json.Marshal(map[string]any{"metadata": map[string]any{field: map[string]any{key: v}}})
	return b
}

// planMetaSet ist die gemeinsame Pipeline für annotate & set_label.
func (r *Runner) planMetaSet(a Action, field string) []step {
	var p struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Remove bool   `json:"remove"`
	}
	_ = json.Unmarshal(a.Params, &p)
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			var val *string
			if !p.Remove {
				v := p.Value
				val = &v
			}
			if err := r.patchMeta(ctx, a.TargetNamespace, a.TargetKind, a.TargetName, metaJSON(field, p.Key, val)); err != nil {
				return "", err
			}
			if p.Remove {
				return fmt.Sprintf("%s %q removed", field, p.Key), nil
			}
			return fmt.Sprintf("%s %s=%s", field, p.Key, p.Value), nil
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			m, err := r.getMeta(ctx, a.TargetNamespace, a.TargetKind, a.TargetName)
			if err != nil {
				return "", err
			}
			cur := m.Annotations
			if field == "labels" {
				cur = m.Labels
			}
			got, ok := cur[p.Key]
			if p.Remove {
				if ok {
					return "", fmt.Errorf("%s %q still present", field, p.Key)
				}
				return "confirmed removed", nil
			}
			if got != p.Value {
				return "", fmt.Errorf("%s %q = %q, expected %q", field, p.Key, got, p.Value)
			}
			return "confirmed " + p.Key + "=" + p.Value, nil
		}},
	}
}

// undoMetaSet snapshottet den Vorher-Wert für den Rollback.
func (r *Runner) undoMetaSet(ctx context.Context, a Action, field string) (string, func(context.Context) error) {
	var p struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(a.Params, &p)
	m, err := r.getMeta(ctx, a.TargetNamespace, a.TargetKind, a.TargetName)
	if err != nil {
		return "", nil
	}
	cur := m.Annotations
	if field == "labels" {
		cur = m.Labels
	}
	prev, had := cur[p.Key]
	return fmt.Sprintf("restored %s %q", field, p.Key), func(rctx context.Context) error {
		var val *string
		if had {
			v := prev
			val = &v
		}
		return r.patchMeta(rctx, a.TargetNamespace, a.TargetKind, a.TargetName, metaJSON(field, p.Key, val))
	}
}

/* ── patch_configmap ────────────────────────────────────────────────────── */

func (r *Runner) planPatchConfigmap(a Action) []step {
	var p struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Remove bool   `json:"remove"`
	}
	_ = json.Unmarshal(a.Params, &p)
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			var v any
			if !p.Remove {
				v = p.Value
			}
			patch, _ := json.Marshal(map[string]any{"data": map[string]any{p.Key: v}})
			if _, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
				return "", err
			}
			return fmt.Sprintf("data[%q] set — NOTE: running pods pick this up only after a rollout_restart", p.Key), nil
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			cm, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			got, ok := cm.Data[p.Key]
			if p.Remove {
				if ok {
					return "", fmt.Errorf("key %q still present", p.Key)
				}
				return "confirmed removed (restart consumers to apply)", nil
			}
			if got != p.Value {
				return "", fmt.Errorf("key %q not updated", p.Key)
			}
			return "confirmed (restart consumers to apply)", nil
		}},
	}
}

func (r *Runner) undoPatchConfigmap(ctx context.Context, a Action) (string, func(context.Context) error) {
	var p struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(a.Params, &p)
	cm, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	prev, had := cm.Data[p.Key]
	return fmt.Sprintf("restored configmap key %q", p.Key), func(rctx context.Context) error {
		var v any
		if had {
			v = prev
		}
		patch, _ := json.Marshal(map[string]any{"data": map[string]any{p.Key: v}})
		_, err := r.clientset.CoreV1().ConfigMaps(a.TargetNamespace).Patch(rctx, a.TargetName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
		return err
	}
}

/* ── set_env / set_resources (Container-Template-Edits) ─────────────────── */

// pickContainer wählt den Ziel-Container (Name oder, bei genau einem, diesen).
func containerNames(tmpl corev1.PodTemplateSpec) []string {
	out := make([]string, len(tmpl.Spec.Containers))
	for i, c := range tmpl.Spec.Containers {
		out[i] = c.Name
	}
	return out
}

func (r *Runner) podTemplate(ctx context.Context, ns, kind, name string) (corev1.PodTemplateSpec, error) {
	switch kind {
	case "Deployment":
		o, err := r.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodTemplateSpec{}, err
		}
		return o.Spec.Template, nil
	case "StatefulSet":
		o, err := r.clientset.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodTemplateSpec{}, err
		}
		return o.Spec.Template, nil
	case "DaemonSet":
		o, err := r.clientset.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodTemplateSpec{}, err
		}
		return o.Spec.Template, nil
	}
	return corev1.PodTemplateSpec{}, fmt.Errorf("kind %q has no pod template", kind)
}

func (r *Runner) resolveContainer(ctx context.Context, a Action, want string) (string, error) {
	tmpl, err := r.podTemplate(ctx, a.TargetNamespace, a.TargetKind, a.TargetName)
	if err != nil {
		return "", err
	}
	names := containerNames(tmpl)
	if want != "" {
		for _, n := range names {
			if n == want {
				return want, nil
			}
		}
		return "", fmt.Errorf("container %q not found (have: %s)", want, strings.Join(names, ", "))
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", fmt.Errorf("workload has %d containers (%s) — specify container", len(names), strings.Join(names, ", "))
}

// patchContainerTemplate strategic-merge-patcht EINEN Container im Pod-Template
// (merge-keyed by name), plus die üblichen Rollout/Ready/Verify-Steps.
func (r *Runner) patchContainerTemplate(ctx context.Context, a Action, container string, containerPatch map[string]any) error {
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{mergeName(container, containerPatch)},
		}}},
	})
	opt := metav1.PatchOptions{FieldManager: fieldManager}
	var err error
	switch a.TargetKind {
	case "Deployment":
		_, err = r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opt)
	case "StatefulSet":
		_, err = r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opt)
	case "DaemonSet":
		_, err = r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opt)
	default:
		return fmt.Errorf("unsupported kind %q", a.TargetKind)
	}
	return err
}

func mergeName(name string, m map[string]any) map[string]any {
	out := map[string]any{"name": name}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (r *Runner) rolloutSteps(a Action) []step {
	return []step{
		{name: "rollout", run: func(ctx context.Context, rep func(string)) (string, error) { return r.observeRollout(ctx, a, rep) }},
		{name: "pods ready", run: func(ctx context.Context, rep func(string)) (string, error) { return r.observeAllPodsReady(ctx, a, rep) }},
		{name: "verify", run: func(ctx context.Context, rep func(string)) (string, error) { return r.verifyStable(ctx, a, rep) }},
	}
}

func (r *Runner) planSetEnv(a Action) []step {
	var p struct {
		Container string `json:"container"`
		Name      string `json:"name"`
		Value     string `json:"value"`
		Remove    bool   `json:"remove"`
	}
	_ = json.Unmarshal(a.Params, &p)
	steps := []step{{name: "trigger", run: func(ctx context.Context, _ func(string)) (string, error) {
		c, err := r.resolveContainer(ctx, a, p.Container)
		if err != nil {
			return "", err
		}
		var envPatch map[string]any
		if p.Remove {
			// strategic-merge kann per name+$patch:delete ein env-Element entfernen
			envPatch = map[string]any{"env": []any{map[string]any{"name": p.Name, "$patch": "delete"}}}
		} else {
			envPatch = map[string]any{"env": []any{map[string]any{"name": p.Name, "value": p.Value}}}
		}
		if err := r.patchContainerTemplate(ctx, a, c, envPatch); err != nil {
			return "", err
		}
		if p.Remove {
			return fmt.Sprintf("env %s removed on %s", p.Name, c), nil
		}
		return fmt.Sprintf("env %s=%s on %s", p.Name, p.Value, c), nil
	}}}
	return append(steps, r.rolloutSteps(a)...)
}

func (r *Runner) planSetResources(a Action) []step {
	var p struct {
		Container      string `json:"container"`
		RequestsCPU    string `json:"requestsCpu"`
		RequestsMemory string `json:"requestsMemory"`
		LimitsCPU      string `json:"limitsCpu"`
		LimitsMemory   string `json:"limitsMemory"`
	}
	_ = json.Unmarshal(a.Params, &p)
	steps := []step{{name: "trigger", run: func(ctx context.Context, _ func(string)) (string, error) {
		c, err := r.resolveContainer(ctx, a, p.Container)
		if err != nil {
			return "", err
		}
		req := map[string]any{}
		lim := map[string]any{}
		if p.RequestsCPU != "" {
			req["cpu"] = p.RequestsCPU
		}
		if p.RequestsMemory != "" {
			req["memory"] = p.RequestsMemory
		}
		if p.LimitsCPU != "" {
			lim["cpu"] = p.LimitsCPU
		}
		if p.LimitsMemory != "" {
			lim["memory"] = p.LimitsMemory
		}
		res := map[string]any{}
		if len(req) > 0 {
			res["requests"] = req
		}
		if len(lim) > 0 {
			res["limits"] = lim
		}
		if len(res) == 0 {
			return "", fmt.Errorf("no resource values given")
		}
		if err := r.patchContainerTemplate(ctx, a, c, map[string]any{"resources": res}); err != nil {
			return "", err
		}
		return fmt.Sprintf("resources set on %s (req=%v lim=%v)", c, req, lim), nil
	}}}
	return append(steps, r.rolloutSteps(a)...)
}

// currentContainerResources snapshottet für den Undo die Resources des Ziel-Containers.
func (r *Runner) undoContainerTemplate(ctx context.Context, a Action, field string) (string, func(context.Context) error) {
	var p struct {
		Container string `json:"container"`
		Name      string `json:"name"`
	}
	_ = json.Unmarshal(a.Params, &p)
	tmpl, err := r.podTemplate(ctx, a.TargetNamespace, a.TargetKind, a.TargetName)
	if err != nil {
		return "", nil
	}
	cn := p.Container
	if cn == "" && len(tmpl.Spec.Containers) == 1 {
		cn = tmpl.Spec.Containers[0].Name
	}
	var target *corev1.Container
	for i := range tmpl.Spec.Containers {
		if tmpl.Spec.Containers[i].Name == cn {
			target = &tmpl.Spec.Containers[i]
			break
		}
	}
	if target == nil {
		return "", nil
	}
	switch field {
	case "resources":
		prev := target.Resources.DeepCopy()
		return "restored previous resources", func(rctx context.Context) error {
			b, _ := json.Marshal(prev)
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if err := r.patchContainerTemplate(rctx, a, cn, map[string]any{"resources": m}); err != nil {
				return err
			}
			_, err := r.observeRollout(rctx, a, func(string) {})
			return err
		}
	case "env":
		// Vorheriges env des Ziel-Namens (Wert oder Abwesenheit)
		var prevVal *string
		for _, e := range target.Env {
			if e.Name == p.Name {
				v := e.Value
				prevVal = &v
				break
			}
		}
		return "restored previous env", func(rctx context.Context) error {
			var envPatch map[string]any
			if prevVal == nil {
				envPatch = map[string]any{"env": []any{map[string]any{"name": p.Name, "$patch": "delete"}}}
			} else {
				envPatch = map[string]any{"env": []any{map[string]any{"name": p.Name, "value": *prevVal}}}
			}
			if err := r.patchContainerTemplate(rctx, a, cn, envPatch); err != nil {
				return err
			}
			_, err := r.observeRollout(rctx, a, func(string) {})
			return err
		}
	}
	return "", nil
}

/* ── statefulset_partition ──────────────────────────────────────────────── */

func (r *Runner) planStatefulsetPartition(a Action) []step {
	var p struct {
		Partition int32 `json:"partition"`
	}
	_ = json.Unmarshal(a.Params, &p)
	return []step{
		{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
			patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"updateStrategy": map[string]any{
				"type": "RollingUpdate", "rollingUpdate": map[string]any{"partition": p.Partition},
			}}})
			if _, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
				return "", err
			}
			return fmt.Sprintf("partition set to %d (ordinals >= %d update on next template change)", p.Partition, p.Partition), nil
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			sts, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			got := int32(-1)
			if ru := sts.Spec.UpdateStrategy.RollingUpdate; ru != nil && ru.Partition != nil {
				got = *ru.Partition
			}
			if got != p.Partition {
				return "", fmt.Errorf("partition is %d, expected %d", got, p.Partition)
			}
			return fmt.Sprintf("confirmed partition=%d", p.Partition), nil
		}},
	}
}

func (r *Runner) undoPartition(ctx context.Context, a Action) (string, func(context.Context) error) {
	sts, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	var prev int32
	if ru := sts.Spec.UpdateStrategy.RollingUpdate; ru != nil && ru.Partition != nil {
		prev = *ru.Partition
	}
	return fmt.Sprintf("restored partition %d", prev), func(rctx context.Context) error {
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"updateStrategy": map[string]any{"rollingUpdate": map[string]any{"partition": prev}}}})
		_, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(rctx, a.TargetName, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
		return err
	}
}

/* ── hpa_toggle (freeze/unfreeze) ──────────────────────────────────────── */

func (r *Runner) planHPAToggle(a Action) []step {
	var p struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(a.Params, &p)
	return []step{{name: "patch", run: func(ctx context.Context, _ func(string)) (string, error) {
		hpa, err := r.clientset.AutoscalingV2().HorizontalPodAutoscalers(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		if p.Enabled {
			// unfreeze: aus der gespeicherten Annotation wiederherstellen
			min, max := int32(1), hpa.Spec.MaxReplicas
			if raw := hpa.Annotations["rocketplane.io/hpa-frozen-bounds"]; raw != "" {
				parts := strings.SplitN(raw, "-", 2)
				if len(parts) == 2 {
					if v, e := strconv.Atoi(parts[0]); e == nil {
						min = int32(v)
					}
					if v, e := strconv.Atoi(parts[1]); e == nil {
						max = int32(v)
					}
				}
			}
			if err := r.patchHPABounds(ctx, a.TargetNamespace, a.TargetName, min, max); err != nil {
				return "", err
			}
			return fmt.Sprintf("HPA unfrozen — bounds restored to %d-%d", min, max), nil
		}
		// freeze: pin min=max=current desired, prior bounds in Annotation snapshotten
		cur := hpa.Status.DesiredReplicas
		if cur < 1 {
			cur = hpa.Status.CurrentReplicas
		}
		if cur < 1 {
			cur = 1
		}
		prevMin := int32(1)
		if hpa.Spec.MinReplicas != nil {
			prevMin = *hpa.Spec.MinReplicas
		}
		annPatch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{
			"rocketplane.io/hpa-frozen-bounds": fmt.Sprintf("%d-%d", prevMin, hpa.Spec.MaxReplicas),
		}}})
		_, _ = r.clientset.AutoscalingV2().HorizontalPodAutoscalers(a.TargetNamespace).Patch(ctx, a.TargetName, types.MergePatchType, annPatch, metav1.PatchOptions{FieldManager: fieldManager})
		if err := r.patchHPABounds(ctx, a.TargetNamespace, a.TargetName, cur, cur); err != nil {
			return "", err
		}
		return fmt.Sprintf("HPA frozen at %d replicas (was %d-%d)", cur, prevMin, hpa.Spec.MaxReplicas), nil
	}}}
}

/* ── rollout_to_revision (Deployment) ──────────────────────────────────── */

func (r *Runner) planRolloutToRevision(a Action) []step {
	var p struct {
		Revision int64 `json:"revision"`
	}
	_ = json.Unmarshal(a.Params, &p)
	steps := []step{{name: "trigger", run: func(ctx context.Context, _ func(string)) (string, error) {
		rs, err := r.replicaSetForRevision(ctx, a, p.Revision)
		if err != nil {
			return "", err
		}
		if err := r.triggerTemplateReplace(ctx, a, rs); err != nil {
			return "", err
		}
		return fmt.Sprintf("rolled to revision %d", p.Revision), nil
	}}}
	return append(steps, r.rolloutSteps(a)...)
}

// replicaSetForRevision findet die owned ReplicaSet mit der gegebenen Revision.
func (r *Runner) replicaSetForRevision(ctx context.Context, a Action, revision int64) (*appsv1.ReplicaSet, error) {
	dep, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	sel := metav1.FormatLabelSelector(dep.Spec.Selector)
	list, err := r.clientset.AppsV1().ReplicaSets(a.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, err
	}
	avail := []string{}
	for i := range list.Items {
		rs := &list.Items[i]
		if !metav1.IsControlledBy(rs, dep) {
			continue
		}
		rev, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		avail = append(avail, strconv.FormatInt(rev, 10))
		if rev == revision {
			return rs, nil
		}
	}
	sort.Strings(avail)
	return nil, fmt.Errorf("revision %d not found (available: %s)", revision, strings.Join(avail, ", "))
}

// triggerTemplateReplace ersetzt /spec/template durch das RS-Template (wie rollout_undo).
func (r *Runner) triggerTemplateReplace(ctx context.Context, a Action, rs *appsv1.ReplicaSet) error {
	tmpl := rs.Spec.Template.DeepCopy()
	delete(tmpl.Labels, "pod-template-hash")
	b, _ := json.Marshal(tmpl)
	patch := []byte(fmt.Sprintf(`[{"op":"replace","path":"/spec/template","value":%s}]`, string(b)))
	_, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName, types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

func (r *Runner) undoTemplateReplace(ctx context.Context, a Action) (string, func(context.Context) error) {
	dep, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return "", nil
	}
	prev := dep.Spec.Template.DeepCopy()
	return "restored previous template", func(rctx context.Context) error {
		b, _ := json.Marshal(prev)
		patch := []byte(fmt.Sprintf(`[{"op":"replace","path":"/spec/template","value":%s}]`, string(b)))
		if _, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(rctx, a.TargetName, types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
			return err
		}
		_, err := r.observeRollout(rctx, a, func(string) {})
		return err
	}
}

/* ── evict_pod (PDB-aware) ──────────────────────────────────────────────── */

func (r *Runner) planEvictPod(a Action) []step {
	var p struct {
		GracePeriodSeconds *int64 `json:"gracePeriodSeconds"`
	}
	_ = json.Unmarshal(a.Params, &p)
	return []step{
		{name: "evict", run: func(ctx context.Context, _ func(string)) (string, error) {
			ev := &policyv1.Eviction{
				ObjectMeta:    metav1.ObjectMeta{Name: a.TargetName, Namespace: a.TargetNamespace},
				DeleteOptions: &metav1.DeleteOptions{},
			}
			if p.GracePeriodSeconds != nil {
				ev.DeleteOptions.GracePeriodSeconds = p.GracePeriodSeconds
			}
			if err := r.clientset.PolicyV1().Evictions(a.TargetNamespace).Evict(ctx, ev); err != nil {
				if strings.Contains(err.Error(), "Cannot evict") || strings.Contains(err.Error(), "disruption") {
					return "", fmt.Errorf("eviction blocked by a PodDisruptionBudget: %v", err)
				}
				return "", err
			}
			return "eviction requested (graceful, PDB-aware)", nil
		}},
		{name: "drain", run: func(ctx context.Context, rep func(string)) (string, error) { return r.observePodGone(ctx, a, rep) }},
		{name: "recreate", run: func(ctx context.Context, rep func(string)) (string, error) { return r.observeReplacement(ctx, a, rep) }},
		{name: "verify", run: func(ctx context.Context, rep func(string)) (string, error) { return r.verifySiblingsStable(ctx, a, rep) }},
	}
}

/* ── cleanup_jobs (Namespace) ──────────────────────────────────────────── */

func (r *Runner) planCleanupJobs(a Action) []step {
	var p struct {
		States        []string `json:"states"`
		OlderThanHours int     `json:"olderThanHours"`
	}
	_ = json.Unmarshal(a.Params, &p)
	wantComplete, wantFailed := true, true
	if len(p.States) > 0 {
		wantComplete, wantFailed = false, false
		for _, s := range p.States {
			switch s {
			case "Complete":
				wantComplete = true
			case "Failed":
				wantFailed = true
			}
		}
	}
	return []step{{name: "delete", run: func(ctx context.Context, rep func(string)) (string, error) {
		ns := a.TargetName // TargetName = Namespace
		jobs, err := r.clientset.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		bg := metav1.DeletePropagationBackground
		deleted := 0
		for i := range jobs.Items {
			j := &jobs.Items[i]
			complete, failed := jobConditions(j)
			if (complete && wantComplete) || (failed && wantFailed) {
				if p.OlderThanHours > 0 && time.Since(j.CreationTimestamp.Time) < time.Duration(p.OlderThanHours)*time.Hour {
					continue
				}
				if err := r.clientset.BatchV1().Jobs(ns).Delete(ctx, j.Name, metav1.DeleteOptions{PropagationPolicy: &bg}); err == nil {
					deleted++
					rep(fmt.Sprintf("deleted %d jobs", deleted))
				}
			}
		}
		return fmt.Sprintf("deleted %d finished job(s) in %s (active jobs untouched)", deleted, ns), nil
	}}}
}

func jobConditions(j *batchv1.Job) (complete, failed bool) {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		if c.Type == batchv1.JobComplete {
			complete = true
		}
		if c.Type == batchv1.JobFailed {
			failed = true
		}
	}
	return
}

/* ── rollout_history (read) ────────────────────────────────────────────── */

func (r *Runner) planRolloutHistory(a Action) []step {
	var p struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal(a.Params, &p)
	if p.Limit <= 0 || p.Limit > 20 {
		p.Limit = 10
	}
	return []step{{name: "history", run: func(ctx context.Context, _ func(string)) (string, error) {
		if a.TargetKind != "Deployment" {
			return "", fmt.Errorf("rollout_history currently supports Deployment")
		}
		dep, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		list, err := r.clientset.AppsV1().ReplicaSets(a.TargetNamespace).List(ctx, metav1.ListOptions{LabelSelector: metav1.FormatLabelSelector(dep.Spec.Selector)})
		if err != nil {
			return "", err
		}
		type rev struct {
			Revision int64  `json:"revision"`
			Image    string `json:"image"`
			Cause    string `json:"changeCause"`
			Age      string `json:"age"`
		}
		revs := []rev{}
		for i := range list.Items {
			rs := &list.Items[i]
			if !metav1.IsControlledBy(rs, dep) {
				continue
			}
			n, _ := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
			img := ""
			if len(rs.Spec.Template.Spec.Containers) > 0 {
				img = rs.Spec.Template.Spec.Containers[0].Image
			}
			revs = append(revs, rev{n, img, rs.Annotations["kubernetes.io/change-cause"], time.Since(rs.CreationTimestamp.Time).Round(time.Second).String()})
		}
		sort.Slice(revs, func(i, j int) bool { return revs[i].Revision > revs[j].Revision })
		if len(revs) > p.Limit {
			revs = revs[:p.Limit]
		}
		out, _ := json.Marshal(map[string]any{"deployment": a.TargetName, "revisions": revs})
		return string(out), nil
	}}}
}

/* ── drain_preview (read) ──────────────────────────────────────────────── */

func (r *Runner) planDrainPreview(a Action) []step {
	return []step{{name: "preview", run: func(ctx context.Context, _ func(string)) (string, error) {
		node := a.TargetName
		pods, err := r.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
		if err != nil {
			return "", err
		}
		perWorkload := map[string]int{}
		evictable := 0
		daemon := 0
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Namespace == "kube-system" {
				// weiter zählen, aber markieren
			}
			isDaemon := false
			ctrl := ""
			for _, o := range pod.OwnerReferences {
				if o.Kind == "DaemonSet" {
					isDaemon = true
				}
				ctrl = pod.Namespace + "/" + o.Name
			}
			if isDaemon {
				daemon++
				continue
			}
			evictable++
			if ctrl != "" {
				perWorkload[ctrl]++
			}
		}
		// PDBs, die blocken könnten
		pdbs, _ := r.clientset.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
		blocking := []string{}
		for i := range pdbs.Items {
			pdb := &pdbs.Items[i]
			if pdb.Status.DisruptionsAllowed == 0 && pdb.Status.CurrentHealthy > 0 {
				blocking = append(blocking, pdb.Namespace+"/"+pdb.Name)
			}
		}
		out, _ := json.Marshal(map[string]any{
			"node": node, "evictablePods": evictable, "daemonSetPods": daemon,
			"perWorkload": perWorkload, "blockingPDBs": blocking,
			"note": "ESTIMATE — no mutation. DaemonSet pods are not evicted; blockingPDBs would stall a real drain.",
		})
		return string(out), nil
	}}}
}
