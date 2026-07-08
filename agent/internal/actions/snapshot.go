package actions

// snapshot.go — generischer Before-Snapshot: VOR jeder Mutation wird das
// Zielobjekt über den dynamic client geholt, um Rausch-Felder (managedFields,
// status, resourceVersion …) gestrippt und als JSON an die Control-Plane
// gemeldet (cluster_actions.snapshot). Er ist die Grundlage für
// restore_resource-Reverts und den Audit-Trail.

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const maxSnapshotBytes = 256 * 1024

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

// mutatingSnapshotKinds: für diese Action-Kinds wird ein Before-Snapshot des
// Ziels erstellt. Reads und Kinds ohne konkretes Einzelobjekt (cleanup_*)
// bleiben draußen.
var mutatingSnapshotKinds = map[string]bool{
	"scale": true, "rollout_restart": true, "rollout_undo": true, "rollout_pause": true,
	"rollout_resume": true, "rollout_to_revision": true, "set_image": true, "set_env": true,
	"set_resources": true, "statefulset_partition": true, "hpa_set": true, "hpa_toggle": true,
	"patch_configmap": true, "patch_secret": true, "delete_configmap": true, "pdb_set": true,
	"patch_resource": true, "pvc_expand": true, "annotate": true, "set_label": true,
	"node_taint": true, "node_untaint": true, "cordon": true, "uncordon": true,
	"delete_pod": true, "evict_pod": true, "delete_job": true, "restore_resource": true,
	"cronjob_suspend": true, "cronjob_resume": true,
}

// prepareSnapshot liefert das gestrippte Before-Objekt als JSON (nil, wenn
// nicht anwendbar/lesbar — der Snapshot ist best effort, nie blockierend).
func (r *Runner) prepareSnapshot(ctx context.Context, a Action) json.RawMessage {
	if !mutatingSnapshotKinds[a.Kind] || kindGVR[a.TargetKind] == (schema.GroupVersionResource{}) {
		return nil
	}
	u, err := r.getUnstructured(ctx, a.TargetKind, a.TargetNamespace, a.TargetName)
	if err != nil {
		return nil
	}
	obj := stripForSnapshot(u)
	// Secrets: Werte hashen — der Snapshot landet in der CP-Datenbank und im
	// UI-Audit; Klartext-Secrets gehören da nicht hin.
	if a.TargetKind == "Secret" {
		redactSecretData(obj)
	}
	b, err := json.Marshal(obj)
	if err != nil || len(b) > maxSnapshotBytes {
		meta := map[string]any{"kind": a.TargetKind, "name": a.TargetName, "namespace": a.TargetNamespace, "note": "snapshot too large — metadata only"}
		b, _ = json.Marshal(meta)
	}
	return b
}
