package actions

// deploy.go — die Release-Handgriffe des Alltags als verifizierte Pipelines:
//   set_image      → Container-Image setzen (`kubectl set image`) + voller
//                    Rollout, verifiziert auf Pod-Ebene. Rollback = Vorher-Image.
//   rollout_pause  → Rollout einfrieren (`kubectl rollout pause`) — kein Warten
//                    auf Pods (das ist der Sinn: anhalten). Rollback = resume.
//   rollout_resume → Gegenteil, danach die volle Rollout-Beobachtung.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

/* ── set_image ──────────────────────────────────────────────────────────── */

// currentImages liest die aktuellen Container-Images des Ziel-Workloads
// (name → image) — die Snapshot-Basis für den Rollback.
func (r *Runner) currentImages(ctx context.Context, a Action) (map[string]string, error) {
	out := map[string]string{}
	switch a.TargetKind {
	case "Deployment":
		d, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			out[c.Name] = c.Image
		}
	case "StatefulSet":
		s, err := r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, c := range s.Spec.Template.Spec.Containers {
			out[c.Name] = c.Image
		}
	case "DaemonSet":
		d, err := r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		for _, c := range d.Spec.Template.Spec.Containers {
			out[c.Name] = c.Image
		}
	default:
		return nil, fmt.Errorf("set_image unsupported for %s", a.TargetKind)
	}
	return out, nil
}

// applyImages patcht die genannten Container-Images per Strategic-Merge
// (Container-Liste ist über den Namen merge-keyed — unerwähnte bleiben unberührt).
func (r *Runner) applyImages(ctx context.Context, a Action, images map[string]string) error {
	containers := make([]map[string]string, 0, len(images))
	for name, img := range images {
		containers = append(containers, map[string]string{"name": name, "image": img})
	}
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": containers}}},
	})
	opts := metav1.PatchOptions{FieldManager: "rocketplane-agent"}
	var err error
	switch a.TargetKind {
	case "Deployment":
		_, err = r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	case "StatefulSet":
		_, err = r.clientset.AppsV1().StatefulSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	case "DaemonSet":
		_, err = r.clientset.AppsV1().DaemonSets(a.TargetNamespace).Patch(ctx, a.TargetName, types.StrategicMergePatchType, patch, opts)
	default:
		return fmt.Errorf("set_image unsupported for %s", a.TargetKind)
	}
	return err
}

// triggerSetImage setzt das neue Image. Params: {container?, image}. Fehlt der
// Container-Name, muss der Workload genau EINEN Container haben (sonst ist
// mehrdeutig, welcher gemeint ist).
func (r *Runner) triggerSetImage(ctx context.Context, a Action) (string, error) {
	var p struct {
		Container string `json:"container"`
		Image     string `json:"image"`
	}
	if err := json.Unmarshal(a.Params, &p); err != nil || p.Image == "" {
		return "", fmt.Errorf("missing params.image")
	}
	cur, err := r.currentImages(ctx, a)
	if err != nil {
		return "", err
	}
	target := p.Container
	if target == "" {
		if len(cur) != 1 {
			return "", fmt.Errorf("container name required (%d containers)", len(cur))
		}
		for name := range cur {
			target = name
		}
	} else if _, ok := cur[target]; !ok {
		return "", fmt.Errorf("no container %q in %s/%s", target, a.TargetKind, a.TargetName)
	}
	if err := r.applyImages(ctx, a, map[string]string{target: p.Image}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s → %s", target, p.Image), nil
}

// planSetImage: setzen → voller Rollout (alte Pods raus, neue ready, stabil).
func (r *Runner) planSetImage(a Action) []step {
	var oldPods map[string]string
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
			return fmt.Sprintf("%d pods before update", len(oldPods)), nil
		}},
		{name: "set image", run: func(ctx context.Context, _ func(string)) (string, error) {
			return r.triggerSetImage(ctx, a)
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

/* ── rollout pause / resume ─────────────────────────────────────────────── */

// setPaused schaltet spec.paused eines Deployments (nur Deployment kennt das).
func (r *Runner) setPaused(ctx context.Context, a Action, paused bool) error {
	patch := []byte(fmt.Sprintf(`{"spec":{"paused":%t}}`, paused))
	_, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Patch(ctx, a.TargetName,
		types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	return err
}

func (r *Runner) verifyPaused(ctx context.Context, a Action, want bool) (string, error) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		d, err := r.clientset.AppsV1().Deployments(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
		if err == nil && d.Spec.Paused == want {
			return fmt.Sprintf("verified: paused=%t", want), nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout verifying pause state")
		case <-t.C:
		}
	}
}

// planPause: pause hält NUR an (kein Pod-Warten); resume beobachtet danach den
// nun freigegebenen Rollout bis zur verifizierten Stabilität.
func (r *Runner) planPause(a Action, pause bool) []step {
	if pause {
		return []step{
			{name: "pause", run: func(ctx context.Context, _ func(string)) (string, error) {
				if err := r.setPaused(ctx, a, true); err != nil {
					return "", err
				}
				return "rollout paused", nil
			}},
			{name: "verify", run: func(ctx context.Context, _ func(string)) (string, error) {
				return r.verifyPaused(ctx, a, true)
			}},
		}
	}
	return []step{
		{name: "resume", run: func(ctx context.Context, _ func(string)) (string, error) {
			if err := r.setPaused(ctx, a, false); err != nil {
				return "", err
			}
			return "rollout resumed", nil
		}},
		{name: "rollout", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.observeRollout(ctx, a, report)
		}},
		{name: "pods ready", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.observeAllPodsReady(ctx, a, report)
		}},
		{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
			return r.verifyStable(ctx, a, report)
		}},
	}
}
