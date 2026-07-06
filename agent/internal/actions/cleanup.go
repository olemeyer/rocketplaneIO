package actions

// cleanup.go — Housekeeping: abgeschlossene/gescheiterte Pods eines Namespace
// abräumen (der wöchentliche `kubectl delete pod --field-selector`-Reflex).
//   cleanup_pods → löscht Pods in Phase Failed/Succeeded (inkl. Evicted).
//                  Nicht undo-bar (terminale Pods werden nicht ersetzt), daher
//                  bewusst eng: NUR terminale Pods, nie laufende.

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// planCleanupPods: Ziel ist ein Namespace (TargetName). Params: {phases?}.
// Default räumt Failed + Succeeded. Laufende/pending Pods sind tabu.
func (r *Runner) planCleanupPods(a Action) []step {
	return []step{
		{name: "scan", run: func(ctx context.Context, report func(string)) (string, error) {
			var p struct {
				Phases []string `json:"phases"`
			}
			_ = json.Unmarshal(a.Params, &p)
			want := map[corev1.PodPhase]bool{}
			if len(p.Phases) == 0 {
				want[corev1.PodFailed] = true
				want[corev1.PodSucceeded] = true
			} else {
				for _, ph := range p.Phases {
					switch ph {
					case "Failed":
						want[corev1.PodFailed] = true
					case "Succeeded":
						want[corev1.PodSucceeded] = true
					}
				}
			}
			// Nur terminale Phasen — running/pending werden nie angefasst.
			delete(want, corev1.PodRunning)
			delete(want, corev1.PodPending)

			list, err := r.clientset.CoreV1().Pods(a.TargetName).List(ctx, metav1.ListOptions{})
			if err != nil {
				return "", err
			}
			victims := 0
			for i := range list.Items {
				p := &list.Items[i]
				if p.DeletionTimestamp != nil {
					continue // fährt ohnehin raus
				}
				if want[p.Status.Phase] {
					if err := r.clientset.CoreV1().Pods(a.TargetName).Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil {
						report(fmt.Sprintf("could not delete %s: %v", p.Name, err))
						continue
					}
					victims++
					report(fmt.Sprintf("deleted %d terminal pod(s)", victims))
				}
			}
			if victims == 0 {
				return "nothing to clean up", nil
			}
			return fmt.Sprintf("removed %d terminal pod(s)", victims), nil
		}},
	}
}
