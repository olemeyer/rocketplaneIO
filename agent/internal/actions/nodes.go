package actions

// nodes.go — die tägliche Node-Wartung als VERIFIZIERTE Pipelines:
//   cordon    → Node unschedulable, verifiziert
//   uncordon  → Gegenteil
//   drain     → cordon + Eviction-API je Pod (respektiert PodDisruptionBudgets,
//               überspringt DaemonSets/mirror-Pods) + warten bis der Node leer ist
//   rollout_undo → Rollback auf die vorherige ReplicaSet-Revision (kubectl
//               rollout undo), mit voller Rollout-Pipeline danach.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

/* ── cordon / uncordon ──────────────────────────────────────────────────── */

func (r *Runner) setUnschedulable(ctx context.Context, node string, want bool) (string, error) {
	patch := []byte(fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, want))
	_, err := r.clientset.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, patch,
		metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	if err != nil {
		return "", err
	}
	verb := "cordoned"
	if !want {
		verb = "uncordoned"
	}
	return fmt.Sprintf("node %s %s", node, verb), nil
}

func (r *Runner) verifyUnschedulable(ctx context.Context, node string, want bool) (string, error) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		n, err := r.clientset.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
		if err == nil && n.Spec.Unschedulable == want {
			return fmt.Sprintf("verified: unschedulable=%t", want), nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout verifying node schedulability")
		case <-t.C:
		}
	}
}

func (r *Runner) planCordon(a Action, want bool) []step {
	name := "cordon"
	if !want {
		name = "uncordon"
	}
	return []step{
		{name: name, run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.setUnschedulable(ctx, a.TargetName, want)
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.verifyUnschedulable(ctx, a.TargetName, want)
		}},
	}
}

/* ── drain ──────────────────────────────────────────────────────────────── */

// drainablePods: alles auf dem Node außer DaemonSet-Pods, mirror-Pods und
// bereits terminierenden Pods.
func (r *Runner) drainablePods(ctx context.Context, node string) ([]corev1.Pod, error) {
	list, err := r.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node,
	})
	if err != nil {
		return nil, err
	}
	out := []corev1.Pod{}
	for _, p := range list.Items {
		if p.DeletionTimestamp != nil {
			continue
		}
		if _, isMirror := p.Annotations[corev1.MirrorPodAnnotationKey]; isMirror {
			continue
		}
		isDaemon := false
		for _, o := range p.OwnerReferences {
			if o.Kind == "DaemonSet" {
				isDaemon = true
			}
		}
		if isDaemon {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *Runner) planDrain(a Action) []step {
	node := a.TargetName
	return []step{
		{name: "cordon", run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.setUnschedulable(ctx, node, true)
		}},
		{name: "evict", run: func(ctx context.Context, report func(string)) (string, error) {
			pods, err := r.drainablePods(ctx, node)
			if err != nil {
				return "", err
			}
			if len(pods) == 0 {
				return "no pods to evict", nil
			}
			evicted := 0
			for i := range pods {
				p := &pods[i]
				// Eviction-API: respektiert PDBs; 429 = PDB blockiert → retry.
				for {
					err := r.clientset.PolicyV1().Evictions(p.Namespace).Evict(ctx, &policyv1.Eviction{
						ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
					})
					if err == nil || apierrors.IsNotFound(err) {
						break
					}
					if apierrors.IsTooManyRequests(err) {
						report(fmt.Sprintf("evicted %d/%d — %s blocked by PDB, retrying", evicted, len(pods), p.Name))
						select {
						case <-ctx.Done():
							return "", fmt.Errorf("timeout while PDB blocks eviction of %s", p.Name)
						case <-time.After(3 * time.Second):
						}
						continue
					}
					return "", fmt.Errorf("evict %s/%s: %w", p.Namespace, p.Name, err)
				}
				evicted++
				report(fmt.Sprintf("evicted %d/%d pods", evicted, len(pods)))
			}
			return fmt.Sprintf("evicted %d pods (PDB-aware)", evicted), nil
		}},
		{name: "drained", run: func(ctx context.Context, report func(string)) (string, error) {
			t := time.NewTicker(observeEvery)
			defer t.Stop()
			for {
				pods, err := r.drainablePods(ctx, node)
				if err == nil {
					if len(pods) == 0 {
						return "node is drained (daemonsets remain)", nil
					}
					names := make([]string, 0, 3)
					for i := range pods {
						if i < 3 {
							names = append(names, pods[i].Name)
						}
					}
					report(fmt.Sprintf("%d pods still terminating (%s…)", len(pods), strings.Join(names, ", ")))
				}
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("timeout waiting for node to drain")
				case <-t.C:
				}
			}
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.verifyUnschedulable(ctx, node, true)
		}},
	}
}

/* ── rollout undo ───────────────────────────────────────────────────────── */

// previousRS findet das ReplicaSet der VORHERIGEN Revision eines Deployments.
func (r *Runner) previousRS(ctx context.Context, ns, name string) (current, previous string, prevTemplate []byte, err error) {
	d, err := r.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", nil, err
	}
	sel, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return "", "", nil, err
	}
	rsList, err := r.clientset.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return "", "", nil, err
	}
	type rev struct {
		n    int
		name string
		tmpl []byte
	}
	revs := []rev{}
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		rn, _ := strconv.Atoi(rs.Annotations["deployment.kubernetes.io/revision"])
		// Template als Merge-Patch-Payload konservieren
		tmpl, merr := templatePatch(rs)
		if merr != nil {
			continue
		}
		revs = append(revs, rev{n: rn, name: rs.Name, tmpl: tmpl})
	}
	if len(revs) < 2 {
		return "", "", nil, fmt.Errorf("no previous revision (only %d)", len(revs))
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].n > revs[j].n })
	return fmt.Sprintf("rev %d (%s)", revs[0].n, revs[0].name),
		fmt.Sprintf("rev %d (%s)", revs[1].n, revs[1].name), revs[1].tmpl, nil
}

func (r *Runner) planRolloutUndo(a Action) []step {
	var oldPods map[string]string
	var prevDesc string
	var prevTmpl []byte
	return []step{
		{name: "snapshot", run: func(ctx context.Context, _ func(string)) (string, error) {
			pods, err := r.workloadPods(ctx, a)
			if err != nil {
				return "", err
			}
			oldPods = make(map[string]string, len(pods))
			for _, p := range pods {
				oldPods[string(p.UID)] = p.Name
			}
			cur, prev, tmpl, err := r.previousRS(ctx, a.TargetNamespace, a.TargetName)
			if err != nil {
				return "", err
			}
			prevDesc = prev
			prevTmpl = tmpl
			return fmt.Sprintf("current %s → rolling back to %s", cur, prev), nil
		}},
		{name: "rollback", run: func(ctx context.Context, _ func(string)) (string, error) {
			_, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName,
				types.JSONPatchType, prevTmpl, metav1.PatchOptions{FieldManager: "rocketplane-agent"})
			if err != nil {
				return "", err
			}
			return "template restored from " + prevDesc, nil
		}},
		{name: "rollout", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.observeRollout(ctx, a, report)
		}},
		{name: "drain old", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.observeOldGone(ctx, a, oldPods, report)
		}},
		{name: "pods ready", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.observeAllPodsReady(ctx, a, report)
		}},
		{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.verifyStable(ctx, a, report)
		}},
	}
}

/* ── node taint / untaint ───────────────────────────────────────────────── */

type taintParams struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"` // NoSchedule | PreferNoSchedule | NoExecute
}

// applyTaint fügt den in Params beschriebenen Taint hinzu (remove=false) oder
// entfernt ihn (remove=true). Die Taint-Liste wird komplett neu gesetzt
// (Merge-Patch ersetzt Arrays als Ganzes) — read-modify-write auf spec.taints.
func (r *Runner) applyTaint(ctx context.Context, a Action, remove bool) error {
	var p taintParams
	if err := json.Unmarshal(a.Params, &p); err != nil || p.Key == "" {
		return fmt.Errorf("missing params.key")
	}
	if !remove {
		switch p.Effect {
		case "NoSchedule", "PreferNoSchedule", "NoExecute":
		default:
			return fmt.Errorf("effect must be NoSchedule, PreferNoSchedule or NoExecute")
		}
	}
	n, err := r.clientset.CoreV1().Nodes().Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	next := make([]corev1.Taint, 0, len(n.Spec.Taints)+1)
	for _, t := range n.Spec.Taints {
		// beim Setzen wie beim Entfernen: bestehenden gleichen Key+Effect droppen
		// (Set = upsert, Remove = weglassen).
		if t.Key == p.Key && (remove || string(t.Effect) == p.Effect) {
			continue
		}
		next = append(next, t)
	}
	if !remove {
		next = append(next, corev1.Taint{Key: p.Key, Value: p.Value, Effect: corev1.TaintEffect(p.Effect)})
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"taints": next}})
	_, err = r.clientset.CoreV1().Nodes().Patch(ctx, a.TargetName, types.MergePatchType, patch,
		metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	return err
}

func (r *Runner) verifyTaint(ctx context.Context, a Action, remove bool) (string, error) {
	var p taintParams
	_ = json.Unmarshal(a.Params, &p)
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		n, err := r.clientset.CoreV1().Nodes().Get(ctx, a.TargetName, metav1.GetOptions{})
		if err == nil {
			present := false
			for _, tt := range n.Spec.Taints {
				if tt.Key == p.Key {
					present = true
				}
			}
			if present != remove {
				verb := "tainted"
				if remove {
					verb = "untainted"
				}
				return fmt.Sprintf("verified: node %s (%s)", verb, p.Key), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout verifying node taint")
		case <-t.C:
		}
	}
}

func (r *Runner) planTaint(a Action, remove bool) []step {
	name := "taint"
	if remove {
		name = "untaint"
	}
	return []step{
		{name: name, run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := r.applyTaint(ctx, a, remove); err != nil {
				return "", err
			}
			return "node " + name + "ed", nil
		}},
		{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.verifyTaint(ctx, a, remove)
		}},
	}
}

// triggerRolloutUndo setzt das Deployment-Template auf die vorherige Revision
// zurück (kubectl rollout undo) und meldet cur→prev. Vom Starlark-Builtin
// k8s.rollout_undo genutzt (die Pipeline-Variante lebt in planRolloutUndo).
func (r *Runner) triggerRolloutUndo(ctx context.Context, ns, name string) (string, error) {
	cur, prev, tmpl, err := r.previousRS(ctx, ns, name)
	if err != nil {
		return "", err
	}
	if _, err := r.clientset.AppsV1().Deployments(ns).Patch(ctx, name,
		types.JSONPatchType, tmpl, metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s → %s", cur, prev), nil
}

// templatePatch baut den JSON-Patch, der das Deployment-Template auf das eines
// ReplicaSets zurücksetzt (das ist `kubectl rollout undo`). Bewusst ein
// JSON-Patch `replace` auf /spec/template (NICHT strategic-merge): der Merge
// könnte abwesende Felder — z. B. die `restartedAt`-Annotation eines vorherigen
// rollout_restart — NICHT entfernen, das Undo wäre über einen Restart hinweg ein
// No-op. Replace ersetzt den ganzen Teilbaum und rollt daher echt zurück.
func templatePatch(rs *appsv1.ReplicaSet) ([]byte, error) {
	tmpl := rs.Spec.Template.DeepCopy()
	// pod-template-hash ist RS-spezifisch und darf nicht zurück ins Deployment.
	delete(tmpl.Labels, "pod-template-hash")
	return json.Marshal([]map[string]any{{"op": "replace", "path": "/spec/template", "value": tmpl}})
}
