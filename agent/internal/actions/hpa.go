package actions

// hpa.go — HorizontalPodAutoscaler-Grenzen anpassen. In HPA-Clustern ist ein
// direktes `scale` sinnlos: der Controller dreht die Replicas sofort zurück in
// [min,max]. Der ehrliche Skalier-Griff ist daher, die GRENZEN zu setzen.
//   hpa_set  → min/max patchen, verifizieren. Rollback = alte Grenzen.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// currentHPABounds liest min/max des Ziel-HPA (min ist optional → default 1).
func (r *Runner) currentHPABounds(ctx context.Context, a Action) (min, max int32, err error) {
	h, err := r.clientset.AutoscalingV2().HorizontalPodAutoscalers(a.TargetNamespace).Get(ctx, a.TargetName, metav1.GetOptions{})
	if err != nil {
		return 0, 0, err
	}
	min = int32(1)
	if h.Spec.MinReplicas != nil {
		min = *h.Spec.MinReplicas
	}
	return min, h.Spec.MaxReplicas, nil
}

func (r *Runner) patchHPABounds(ctx context.Context, ns, name string, min, max int32) error {
	patch, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"minReplicas": min, "maxReplicas": max},
	})
	_, err := r.clientset.AutoscalingV2().HorizontalPodAutoscalers(ns).Patch(ctx, name,
		types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	return err
}

// planHPASet: Grenzen setzen → verifizieren, dass der HPA sie übernommen hat.
// Params: {minReplicas, maxReplicas}.
func (r *Runner) planHPASet(a Action) []step {
	var wantMin, wantMax int32
	return []step{
		{name: "set bounds", run: func(ctx context.Context, _ func(string)) (string, error) {
			var p struct {
				MinReplicas *int32 `json:"minReplicas"`
				MaxReplicas *int32 `json:"maxReplicas"`
			}
			if err := json.Unmarshal(a.Params, &p); err != nil || p.MaxReplicas == nil {
				return "", fmt.Errorf("missing params.maxReplicas")
			}
			wantMin = int32(1)
			if p.MinReplicas != nil {
				wantMin = *p.MinReplicas
			}
			wantMax = *p.MaxReplicas
			if wantMin < 1 || wantMax < wantMin || wantMax > 200 {
				return "", fmt.Errorf("invalid bounds: min %d, max %d", wantMin, wantMax)
			}
			if err := r.patchHPABounds(ctx, a.TargetNamespace, a.TargetName, wantMin, wantMax); err != nil {
				return "", err
			}
			return fmt.Sprintf("bounds set to %d–%d", wantMin, wantMax), nil
		}},
		{name: "verify", run: func(ctx context.Context, report func(string)) (string, error) {
			t := time.NewTicker(1 * time.Second)
			defer t.Stop()
			for {
				min, max, err := r.currentHPABounds(ctx, a)
				if err == nil {
					report(fmt.Sprintf("bounds %d–%d", min, max))
					if min == wantMin && max == wantMax {
						return fmt.Sprintf("verified: %d–%d", min, max), nil
					}
				}
				select {
				case <-ctx.Done():
					return "", fmt.Errorf("timeout verifying HPA bounds")
				case <-t.C:
				}
			}
		}},
	}
}
