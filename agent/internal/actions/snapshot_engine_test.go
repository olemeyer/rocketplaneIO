package actions

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/apimachinery/pkg/types"
)

var (
	cmGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	depGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	nodeGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	hpaGVR  = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
)

func snapRunner(t *testing.T, objs ...*unstructured.Unstructured) *Runner {
	t.Helper()
	scheme := runtime.NewScheme()
	gvrToList := map[schema.GroupVersionResource]string{
		cmGVR:   "ConfigMapList",
		depGVR:  "DeploymentList",
		nodeGVR: "NodeList",
		hpaGVR:  "HorizontalPodAutoscalerList",
	}
	rt := make([]runtime.Object, len(objs))
	for i, o := range objs {
		rt[i] = o
	}
	r := New("http://localhost:0", "tok", k8sfake.NewSimpleClientset(), nil, nil)
	r.dyn = dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToList, rt...)
	return r
}

func cm(ns, name string, data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"data":     data,
	}}
}
func dep(ns, name string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"replicas": replicas},
	}}
}

func getCMData(t *testing.T, r *Runner, ns, name string) map[string]any {
	t.Helper()
	u, err := r.dyn.Resource(cmGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cm: %v", err)
	}
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	return d
}
func getDepReplicas(t *testing.T, r *Runner, ns, name string) int64 {
	t.Helper()
	u, err := r.dyn.Resource(depGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get dep: %v", err)
	}
	// A snapshot round-tripped through JSON (as the CP/reaper persist it) yields
	// float64 numbers; the real API server coerces to int32. Read tolerantly.
	if n, ok, _ := unstructured.NestedInt64(u.Object, "spec", "replicas"); ok {
		return n
	}
	f, _, _ := unstructured.NestedFloat64(u.Object, "spec", "replicas")
	return int64(f)
}

// mergePatch applies a JSON-merge-patch via the dynamic client (simulates a mutator).
func mergePatch(t *testing.T, r *Runner, gvr schema.GroupVersionResource, ns, name string, patch map[string]any) {
	t.Helper()
	data, _ := json.Marshal(patch)
	if _, err := r.dyn.Resource(gvr).Namespace(ns).Patch(context.Background(), name, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch: %v", err)
	}
}

// THE headline: a field-scoped capture restores ONLY the touched key — a
// concurrent edit to a sibling key is NOT clobbered (the red-team's blocker).
func TestFieldScopedRestoreLeavesSiblingsUntouched(t *testing.T) {
	r := snapRunner(t, cm("shop", "cfg", map[string]any{"timeout": "30", "region": "eu"}))
	ctx := context.Background()
	log := newCaptureLog()

	// capture only data.timeout, then mutate it
	if _, err := r.captureFields(ctx, log, "ConfigMap", "shop", "cfg", [][]string{{"data", "timeout"}}); err != nil {
		t.Fatal(err)
	}
	mergePatch(t, r, cmGVR, "shop", "cfg", map[string]any{"data": map[string]any{"timeout": "5"}})
	// meanwhile a "concurrent" actor changes a sibling key
	mergePatch(t, r, cmGVR, "shop", "cfg", map[string]any{"data": map[string]any{"region": "us"}})

	r.Restore(ctx, log.List())

	d := getCMData(t, r, "shop", "cfg")
	if d["timeout"] != "30" {
		t.Fatalf("timeout not restored: %v", d["timeout"])
	}
	if d["region"] != "us" {
		t.Fatalf("sibling key CLOBBERED by restore: region=%v (want us)", d["region"])
	}
}

// A field that did not exist before is removed on restore (merge null).
func TestFieldScopedRestoreRemovesAddedKey(t *testing.T) {
	r := snapRunner(t, cm("shop", "cfg", map[string]any{"a": "1"}))
	ctx := context.Background()
	log := newCaptureLog()
	if _, err := r.captureFields(ctx, log, "ConfigMap", "shop", "cfg", [][]string{{"data", "new"}}); err != nil {
		t.Fatal(err)
	}
	mergePatch(t, r, cmGVR, "shop", "cfg", map[string]any{"data": map[string]any{"new": "x"}})
	r.Restore(ctx, log.List())
	d := getCMData(t, r, "shop", "cfg")
	if _, ok := d["new"]; ok {
		t.Fatalf("added key not removed on restore: %v", d)
	}
	if d["a"] != "1" {
		t.Fatalf("existing key disturbed: %v", d)
	}
}

// Whole-object capture restores a Deployment's replicas.
func TestObjectRestore(t *testing.T) {
	r := snapRunner(t, dep("shop", "api", 3))
	ctx := context.Background()
	log := newCaptureLog()
	if _, err := r.captureObject(ctx, log, "Deployment", "shop", "api"); err != nil {
		t.Fatal(err)
	}
	mergePatch(t, r, depGVR, "shop", "api", map[string]any{"spec": map[string]any{"replicas": int64(1)}})
	if getDepReplicas(t, r, "shop", "api") != 1 {
		t.Fatal("precondition: scale did not apply")
	}
	r.Restore(ctx, log.List())
	if n := getDepReplicas(t, r, "shop", "api"); n != 3 {
		t.Fatalf("replicas not restored: %d", n)
	}
}

// Multi-mutation restore runs in REVERSE order and reverts every touched object.
func TestMultiMutationReverseRestore(t *testing.T) {
	r := snapRunner(t,
		dep("shop", "api", 4),
		cm("shop", "cfg", map[string]any{"k": "orig"}),
	)
	ctx := context.Background()
	log := newCaptureLog()
	// mutation 1: scale
	r.captureObject(ctx, log, "Deployment", "shop", "api")
	mergePatch(t, r, depGVR, "shop", "api", map[string]any{"spec": map[string]any{"replicas": int64(0)}})
	// mutation 2: configmap key
	r.captureFields(ctx, log, "ConfigMap", "shop", "cfg", [][]string{{"data", "k"}})
	mergePatch(t, r, cmGVR, "shop", "cfg", map[string]any{"data": map[string]any{"k": "changed"}})

	seqs := log.List()
	if len(seqs) != 2 || seqs[0].Seq != 0 || seqs[1].Seq != 1 {
		t.Fatalf("capture order wrong: %+v", seqs)
	}
	r.Restore(ctx, seqs)
	if getDepReplicas(t, r, "shop", "api") != 4 {
		t.Fatal("dep not restored")
	}
	if getCMData(t, r, "shop", "cfg")["k"] != "orig" {
		t.Fatal("cm not restored")
	}
}

// Capturing an ABSENT object then creating it → restore deletes it.
func TestDeleteIfCreated(t *testing.T) {
	r := snapRunner(t) // empty cluster
	ctx := context.Background()
	log := newCaptureLog()
	c, err := r.captureObject(ctx, log, "ConfigMap", "shop", "fresh")
	if err != nil {
		t.Fatal(err)
	}
	if c.Existed {
		t.Fatal("expected Existed=false for absent object")
	}
	// create it
	_, err = r.dyn.Resource(cmGVR).Namespace("shop").Create(ctx, cm("shop", "fresh", map[string]any{"x": "1"}), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.Restore(ctx, log.List())
	_, err = r.dyn.Resource(cmGVR).Namespace("shop").Get(ctx, "fresh", metav1.GetOptions{})
	if err == nil {
		t.Fatal("created object not deleted on restore")
	}
}

// The capture log is JSON-serializable and restores identically after a
// round-trip — proving it is ready to persist for crash-recovery / later revert.
func TestCaptureLogSerializable(t *testing.T) {
	r := snapRunner(t, dep("shop", "api", 2), cm("shop", "cfg", map[string]any{"k": "v0"}))
	ctx := context.Background()
	log := newCaptureLog()
	r.captureObject(ctx, log, "Deployment", "shop", "api")
	r.captureFields(ctx, log, "ConfigMap", "shop", "cfg", [][]string{{"data", "k"}})
	mergePatch(t, r, depGVR, "shop", "api", map[string]any{"spec": map[string]any{"replicas": int64(9)}})
	mergePatch(t, r, cmGVR, "shop", "cfg", map[string]any{"data": map[string]any{"k": "v1"}})

	// serialize → deserialize (as the CP would persist + the reaper reload)
	blob, err := json.Marshal(log.List())
	if err != nil {
		t.Fatal(err)
	}
	var reloaded []Capture
	if err := json.Unmarshal(blob, &reloaded); err != nil {
		t.Fatal(err)
	}
	r.Restore(ctx, reloaded)
	if getDepReplicas(t, r, "shop", "api") != 2 {
		t.Fatal("dep not restored from reloaded log")
	}
	if getCMData(t, r, "shop", "cfg")["k"] != "v0" {
		t.Fatal("cm not restored from reloaded log")
	}
}
