package actions

// snapshot.go — the whitelisted kind→GVR map and the object helpers the snapshot
// substrate shares: generic get, and the strip that makes a captured object
// re-appliable (managedFields/status/resourceVersion removed). The before-state
// itself is captured by the snapshot surface (script_snapshot.go), not here.

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// kindGVR: statische Kind→GroupVersionResource-Karte für alle whitelisted
// Ziel-Kinds. Statisch statt RESTMapper: kein Discovery-Roundtrip, keine
// Cache-Invalidierung — und die Whitelist ist ohnehin geschlossen.
var kindGVR = map[string]schema.GroupVersionResource{
	"Deployment":              {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet":             {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"DaemonSet":               {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"Pod":                     {Group: "", Version: "v1", Resource: "pods"},
	"ConfigMap":               {Group: "", Version: "v1", Resource: "configmaps"},
	"Secret":                  {Group: "", Version: "v1", Resource: "secrets"},
	"Service":                 {Group: "", Version: "v1", Resource: "services"},
	"ServiceAccount":          {Group: "", Version: "v1", Resource: "serviceaccounts"},
	"Namespace":               {Group: "", Version: "v1", Resource: "namespaces"},
	"Node":                    {Group: "", Version: "v1", Resource: "nodes"},
	"PersistentVolumeClaim":   {Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	"ResourceQuota":           {Group: "", Version: "v1", Resource: "resourcequotas"},
	"LimitRange":              {Group: "", Version: "v1", Resource: "limitranges"},
	"Ingress":                 {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
	"NetworkPolicy":           {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
	"PodDisruptionBudget":     {Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
	"HorizontalPodAutoscaler": {Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"},
	"CronJob":                 {Group: "batch", Version: "v1", Resource: "cronjobs"},
	"Job":                     {Group: "batch", Version: "v1", Resource: "jobs"},
}

func clusterScopedKind(kind string) bool {
	switch kind {
	case "Node", "Namespace", "PersistentVolume":
		return true
	}
	return false
}

// getUnstructured holt das Zielobjekt generisch (nil-sicher ohne dyn).
func (r *Runner) getUnstructured(ctx context.Context, kind, namespace, name string) (*unstructured.Unstructured, error) {
	if r.dyn == nil {
		return nil, fmt.Errorf("generic access unavailable (no dynamic client)")
	}
	gvr, ok := kindGVR[kind]
	if !ok {
		return nil, fmt.Errorf("kind %q not in the generic whitelist", kind)
	}
	if clusterScopedKind(kind) || namespace == "-" || namespace == "" {
		return r.dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	return r.dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// stripForSnapshot entfernt Server-Rausch, damit der Snapshot re-applybar ist.
func stripForSnapshot(u *unstructured.Unstructured) map[string]any {
	obj := u.DeepCopy().Object
	delete(obj, "status")
	if md, ok := obj["metadata"].(map[string]any); ok {
		for _, f := range []string{"managedFields", "resourceVersion", "uid", "generation", "creationTimestamp", "selfLink", "ownerReferences"} {
			delete(md, f)
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			delete(ann, "deployment.kubernetes.io/revision")
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
	}
	return obj
}
