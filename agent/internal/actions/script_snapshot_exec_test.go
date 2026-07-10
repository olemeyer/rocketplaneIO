package actions

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// snapExecRunner points a Runner at a stub control plane (to record durable
// snapshot reports) AND a dynamic fake cluster (to observe mutations + restore).
func snapExecRunner(t *testing.T, cpURL string, objs ...*unstructured.Unstructured) *Runner {
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
	r := New(cpURL, "tok", k8sfake.NewSimpleClientset(), nil, nil)
	r.dyn = dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToList, rt...)
	return r
}

// The full execute() path for a snapshot_script action: captures are reported
// DURABLY as they happen (so a crash mid-run is restorable), and a failure
// auto-rolls-back via the generic Restore. This is the whole substrate wired end
// to end through the real action dispatch.
func TestExecuteSnapshotScriptDurableReportAndRollback(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL,
		dep("shop", "api", 7),
		cm("shop", "cfg", map[string]any{"mode": "prod", "keep": "x"}),
	)

	src := `
k8s.scale("shop", "Deployment", "api", 1)
k8s.patch_configmap("shop", "cfg", "mode", "maint")
fail("verify failed")
`
	params, _ := json.Marshal(map[string]any{"source": src})
	a := Action{ID: "act-1", Kind: "snapshot_script", Params: params}

	r.execute(context.Background(), a) // real dispatch → executeSnapshotScript

	reports := cp.snapshot()

	// 1) captures were reported DURABLY during the run (running ticks with a
	//    snapshot batch) — before the terminal report.
	durable := 0
	sawTerminalFailed := false
	for _, rep := range reports {
		if rep.Status == "running" && len(rep.Snapshots) > 0 && string(rep.Snapshots) != "null" {
			durable++
		}
		if rep.Status == "failed" {
			sawTerminalFailed = true
		}
	}
	if durable < 2 {
		t.Fatalf("expected >=2 durable snapshot reports during the run, got %d", durable)
	}
	if !sawTerminalFailed {
		t.Fatal("expected a terminal failed report")
	}

	// 2) the failure auto-rolled-back every mutation (generic Restore).
	if getDepReplicas(t, r, "shop", "api") != 7 {
		t.Fatalf("deployment not auto-rolled-back: replicas=%d", getDepReplicas(t, r, "shop", "api"))
	}
	d := getCMData(t, r, "shop", "cfg")
	if d["mode"] != "prod" {
		t.Fatalf("configmap key not auto-rolled-back: mode=%v", d["mode"])
	}
	if d["keep"] != "x" {
		t.Fatalf("sibling key clobbered by rollback: keep=%v", d["keep"])
	}
}

// A successful snapshot_script leaves mutations in place; the durable list still
// exists on the CP for a later manual revert.
func TestExecuteSnapshotScriptSuccessReportsSnapshots(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL, dep("shop", "web", 2))
	params, _ := json.Marshal(map[string]any{"source": `k8s.scale("shop", "Deployment", "web", 5)`})
	r.execute(context.Background(), Action{ID: "act-2", Kind: "snapshot_script", Params: params})

	if getDepReplicas(t, r, "shop", "web") != 5 {
		t.Fatal("successful mutation should persist")
	}
	var durable, succeeded bool
	for _, rep := range cp.snapshot() {
		if rep.Status == "running" && len(rep.Snapshots) > 0 && string(rep.Snapshots) != "null" {
			durable = true
		}
		if rep.Status == "succeeded" {
			succeeded = true
		}
	}
	if !durable || !succeeded {
		t.Fatalf("expected a durable snapshot report + a succeeded terminal (durable=%v succeeded=%v)", durable, succeeded)
	}
}
