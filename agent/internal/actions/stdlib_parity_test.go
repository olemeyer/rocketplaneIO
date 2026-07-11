package actions

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// stdlib_parity_test.go — Phase 3 proof: the built-in kinds, expressed as
// snapshot-aware scripts over the GENERIC k8s.patch primitive, restore to the
// EXACT before-state (which is what revert.go's per-kind inverse achieves). One
// primitive + one generic restore replaces ~40 hand-written inverses. Each test
// covers a distinct restore shape.

func node(name string, unschedulable bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": name},
		"spec":     map[string]any{"unschedulable": unschedulable},
	}}
}
func hpa(ns, name string, min, max int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"minReplicas": min, "maxReplicas": max, "scaleTargetRef": map[string]any{"name": "api"}},
	}}
}

func getField(t *testing.T, r *Runner, gvr schema.GroupVersionResource, ns, name string, path ...string) any {
	t.Helper()
	var u *unstructured.Unstructured
	var err error
	if ns == "" {
		u, err = r.dyn.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{})
	} else {
		u, err = r.dyn.Resource(gvr).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	}
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	v, _ := nestedGet(u.Object, path)
	return v
}

// runParity runs a built-in-style script (snapshot + generic patch) then
// restores, asserting the whole run round-trips to the before-state.
func runParity(t *testing.T, r *Runner, src string) {
	t.Helper()
	log, err := r.runCapturedScript(context.Background(), src, nil, nil)
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	for _, x := range r.Restore(context.Background(), log.List()) {
		if !x.OK {
			t.Fatalf("restore failed: %s/%s %s", x.Kind, x.Name, x.Detail)
		}
	}
}

// cordon: Node spec.unschedulable false→true→restore false (cluster-scoped object).
func TestParityCordon(t *testing.T) {
	r := snapRunner(t, node("worker-1", false))
	runParity(t, r, `k8s.patch("-", "Node", "worker-1", {"spec": {"unschedulable": True}})`)
	if v := getField(t, r, nodeGVR, "", "worker-1", "spec", "unschedulable"); v != false {
		t.Fatalf("cordon not restored: unschedulable=%v", v)
	}
}

// hpa_set: bounds 1..5 → 3..10 → restore 1..5 (two fields on one object).
func TestParityHPASet(t *testing.T) {
	r := snapRunner(t, hpa("shop", "api", 1, 5))
	runParity(t, r, `k8s.patch("shop", "HorizontalPodAutoscaler", "api", {"spec": {"minReplicas": 3, "maxReplicas": 10}})`)
	if v := getField(t, r, hpaGVR, "shop", "api", "spec", "minReplicas"); toI(v) != 1 {
		t.Fatalf("hpa min not restored: %v", v)
	}
	if v := getField(t, r, hpaGVR, "shop", "api", "spec", "maxReplicas"); toI(v) != 5 {
		t.Fatalf("hpa max not restored: %v", v)
	}
	// scaleTargetRef (untouched sibling) must survive the round-trip
	if v := getField(t, r, hpaGVR, "shop", "api", "spec", "scaleTargetRef", "name"); v != "api" {
		t.Fatalf("hpa sibling field lost: %v", v)
	}
}

// annotate: add a metadata annotation → restore removes it, existing meta intact.
func TestParityAnnotate(t *testing.T) {
	dp := dep("shop", "api", 2)
	meta := dp.Object["metadata"].(map[string]any)
	meta["annotations"] = map[string]any{"owner": "team-a"}
	r := snapRunner(t, dp)
	// annotate = a key ADDITION → field-scoped so restore REMOVES it (a whole-
	// object merge-patch cannot remove an added key).
	runParity(t, r, `k8s.set_field("shop", "Deployment", "api", ["metadata", "annotations", "note"], "maint")`)
	anns, _ := nestedGet(mustGet(t, r, depGVR, "shop", "api"), []string{"metadata", "annotations"})
	m, _ := anns.(map[string]any)
	if _, added := m["note"]; added {
		t.Fatalf("added annotation not removed on restore: %v", m)
	}
	if m["owner"] != "team-a" {
		t.Fatalf("existing annotation lost: %v", m)
	}
}

// set_image: patch a container's image (array replace) → restore to before.
func TestParitySetImage(t *testing.T) {
	dp := dep("shop", "api", 2)
	spec := dp.Object["spec"].(map[string]any)
	spec["template"] = map[string]any{"spec": map[string]any{"containers": []any{
		map[string]any{"name": "app", "image": "nginx:1.25"},
	}}}
	r := snapRunner(t, dp)
	runParity(t, r, `k8s.patch("shop", "Deployment", "api", {"spec": {"template": {"spec": {"containers": [{"name": "app", "image": "nginx:1.27"}]}}}})`)
	c, _ := nestedGet(mustGet(t, r, depGVR, "shop", "api"), []string{"spec", "template", "spec", "containers"})
	arr, _ := c.([]any)
	if len(arr) != 1 || arr[0].(map[string]any)["image"] != "nginx:1.25" {
		t.Fatalf("image not restored: %v", arr)
	}
}

// delete_pod/delete_configmap/delete_job shape: k8s.delete snapshots the whole
// object then deletes; the generic restore RECREATES it from the capture.
func TestParityDeleteRecreate(t *testing.T) {
	r := snapRunner(t, cm("shop", "cfg", map[string]any{"mode": "prod", "keep": "x"}))
	runParity(t, r, `k8s.delete("shop", "ConfigMap", "cfg")`)
	d := getCMData(t, r, "shop", "cfg")
	if d["mode"] != "prod" || d["keep"] != "x" {
		t.Fatalf("configmap not recreated to before-state on restore: %v", d)
	}
}

// create_configmap shape: k8s.create snapshots the ABSENT target then creates;
// the generic restore DELETES what the run created.
func TestParityCreateDelete(t *testing.T) {
	r := snapRunner(t)
	runParity(t, r, `k8s.create({"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "new", "namespace": "shop"}, "data": {"k": "v"}})`)
	_, err := r.dyn.Resource(cmGVR).Namespace("shop").Get(context.Background(), "new", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("created configmap not deleted on restore: err=%v", err)
	}
}

func mustGet(t *testing.T, r *Runner, gvr schema.GroupVersionResource, ns, name string) map[string]any {
	t.Helper()
	u, err := r.dyn.Resource(gvr).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return u.Object
}
func toI(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return -1
}
