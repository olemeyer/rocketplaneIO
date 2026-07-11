package actions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		cmGVR:     "ConfigMapList",
		depGVR:    "DeploymentList",
		nodeGVR:   "NodeList",
		hpaGVR:    "HorizontalPodAutoscalerList",
		secretGVR: "SecretList",
	}
	rt := make([]runtime.Object, len(objs))
	for i, o := range objs {
		rt[i] = o
	}
	r := New(cpURL, "tok", k8sfake.NewSimpleClientset(), nil, nil)
	r.dyn = dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToList, rt...)
	return r
}

var secretGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}

func secret(ns, name string, data map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"data":     data,
	}}
}

// Secret data must NEVER be durably reported to the control plane (no plaintext at
// rest). The mutation still happens; auto-rollback-on-failure still works from the
// in-memory log — only the durable copy is withheld.
func TestSecretCapturesNotDurablyReported(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL, secret("shop", "creds", map[string]any{"token": "b2xk"}))
	// patch the secret, then fail → in-memory auto-rollback must restore it.
	src := `
k8s.patch("shop", "Secret", "creds", {"stringData": {"token": "new"}})
fail("boom")
`
	params, _ := json.Marshal(map[string]any{"source": src})
	r.execute(context.Background(), Action{ID: "act-sec", Kind: "snapshot_script", Params: params})

	// no durable report may carry a Secret capture
	for _, rep := range cp.snapshot() {
		if len(rep.Snapshots) > 0 && string(rep.Snapshots) != "null" {
			if strings.Contains(string(rep.Snapshots), "\"Secret\"") || strings.Contains(string(rep.Snapshots), "\"token\"") {
				t.Fatalf("secret data leaked into a durable snapshot report: %s", rep.Snapshots)
			}
		}
	}
	// auto-rollback (in-memory) restored the original value
	u, _ := r.dyn.Resource(secretGVR).Namespace("shop").Get(context.Background(), "creds", metav1.GetOptions{})
	d, _, _ := unstructured.NestedMap(u.Object, "data")
	if d["token"] != "b2xk" {
		t.Fatalf("secret not rolled back in-memory: %v", d)
	}
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

// A custom "script" workflow (and thus a fork of a built-in) runs on the SAME
// snapshot surface as the built-ins: k8s.patch is a snapshot-surface primitive the
// legacy raw surface does NOT have, so this only passes because kind=script now
// routes to executeSnapshotScript. Proves fork → edit → run works + is revertible.
func TestExecuteScriptRunsOnSnapshotSurface(t *testing.T) {
	old := actionsSnapshotDispatch
	actionsSnapshotDispatch = true
	defer func() { actionsSnapshotDispatch = old }()

	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL, dep("shop", "api", 2))
	src := `step("patch"); k8s.patch("shop", "Deployment", "api", {"spec": {"replicas": 5}})`
	params, _ := json.Marshal(map[string]any{"source": src, "args": map[string]string{}})
	r.execute(context.Background(), Action{ID: "act-fork", Kind: "script", Params: params})

	if getDepReplicas(t, r, "shop", "api") != 5 {
		t.Fatalf("custom script did not run on the snapshot surface (k8s.patch): replicas=%d", getDepReplicas(t, r, "shop", "api"))
	}
	durable, succeeded := false, false
	for _, rep := range cp.snapshot() {
		if rep.Status == "running" && len(rep.Snapshots) > 0 && string(rep.Snapshots) != "null" {
			durable = true
		}
		if rep.Status == "succeeded" {
			succeeded = true
		}
	}
	if !durable || !succeeded {
		t.Fatalf("custom script not revertible/succeeded on snapshot surface (durable=%v succeeded=%v)", durable, succeeded)
	}
}

// The script's step() calls become a structured step timeline on the run — the
// same pipeline the UI shows (fixes "0/0 steps · no steps recorded").
func TestExecuteSnapshotScriptRecordsSteps(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL, dep("shop", "api", 2))
	src := `
step("scale")
k8s.scale("shop", "Deployment", "api", 3)
report("scaled to 3")
step("verify")
report("looks good")
`
	params, _ := json.Marshal(map[string]any{"source": src})
	r.execute(context.Background(), Action{ID: "act-steps", Kind: "snapshot_script", Params: params})

	// the terminal succeeded report must carry both declared steps, marked ok.
	var names []string
	sawSucceeded := false
	for _, rep := range cp.snapshot() {
		if rep.Status == "succeeded" && len(rep.Steps) > 0 {
			sawSucceeded = true
			var steps []struct{ Name, Status, Detail string }
			_ = json.Unmarshal(rep.Steps, &steps)
			names = nil
			for _, s := range steps {
				names = append(names, s.Name+":"+s.Status)
			}
		}
	}
	if !sawSucceeded {
		t.Fatal("no terminal succeeded report with steps")
	}
	if len(names) != 2 || names[0] != "scale:ok" || names[1] != "verify:ok" {
		t.Fatalf("expected [scale:ok verify:ok], got %v", names)
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
