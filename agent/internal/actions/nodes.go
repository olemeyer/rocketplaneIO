package actions

// nodes.go — node helpers shared by the snapshot surface. Node maintenance
// (cordon/drain/uncordon/rollout_undo/taint) now runs as embedded .star scripts
// over the generic primitives (k8s.patch, k8s.evict, k8s.node_pods); the only
// piece that stays in Go is the drainable-pod selection k8s.node_pods reuses.

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// drainablePods: everything on the node except DaemonSet pods, mirror pods and
// pods that are already terminating. Backs the k8s.node_pods primitive.
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
