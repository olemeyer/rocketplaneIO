package actions

// snapshot.go — the well-known kind→GVR static map plus generic object helpers
// the snapshot substrate shares: get, dynamic interface resolution, and the strip
// that makes a captured object re-appliable (managedFields/status/resourceVersion
// removed). The before-state itself is captured by script_snapshot.go, not here.

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// kindGVR: well-known kind→GVR fast-path (no Discovery round-trip). When a
// kind is absent here the caller must supply apiVersion+resource explicitly so
// rawGVR can resolve it (CRDs, less-common built-ins).
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
	"Event":                   {Group: "", Version: "v1", Resource: "events"},
	"Endpoints":               {Group: "", Version: "v1", Resource: "endpoints"},
	"PersistentVolume":        {Group: "", Version: "v1", Resource: "persistentvolumes"},
	"ReplicaSet":              {Group: "apps", Version: "v1", Resource: "replicasets"},
	"StorageClass":            {Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"},
	"EndpointSlice":           {Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"},
}

func clusterScopedKind(kind string) bool {
	switch kind {
	case "Node", "Namespace", "PersistentVolume":
		return true
	}
	return false
}

// resolveGVR returns the GVR for a resource. Fast path: kind in kindGVR and no
// overrides. Slow path: rawGVR(apiVersion, kind, resource) for CRDs and any kind
// not in the static map.
func resolveGVR(apiVersion, kind, resource string) (schema.GroupVersionResource, error) {
	if gvr, ok := kindGVR[kind]; ok && apiVersion == "" && resource == "" {
		return gvr, nil
	}
	return rawGVR(apiVersion, kind, resource)
}

// getUnstructured fetches the target object via the dynamic client. apiVersion
// and resource are optional: when both are empty and the kind is in the static
// map the fast path is used; otherwise rawGVR resolves the GVR.
func (r *Runner) getUnstructured(ctx context.Context, apiVersion, kind, namespace, name, resource string) (*unstructured.Unstructured, error) {
	if r.dyn == nil {
		return nil, fmt.Errorf("generic access unavailable (no dynamic client)")
	}
	gvr, err := resolveGVR(apiVersion, kind, resource)
	if err != nil {
		return nil, err
	}
	if clusterScopedKind(kind) || namespace == "-" || namespace == "" {
		return r.dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	return r.dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// stripForSnapshot entfernt Server-Rausch, damit der Snapshot re-applybar ist.
// stripForRead is the read-path variant: same noise removal, but status STAYS.
// A snapshot must not carry status (it is not restorable), while a read without
// status is undiagnosable — pod phase, conditions, container states and event
// messages all live there.
func stripForRead(u *unstructured.Unstructured) map[string]any {
	obj := u.DeepCopy().Object
	status := obj["status"]
	out := stripForSnapshot(u)
	if status != nil {
		out["status"] = status
	}
	return out
}

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
