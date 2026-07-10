package actions

import (
	"context"
	"encoding/json"
	"testing"
)

// End-to-end over the REAL Starlark interpreter: a script snapshots + mutates
// two resources, then fails; the caller replays the capture log via the ONE
// generic Restore and every mutation is undone. This is the whole snapshot
// substrate — script → auto-capture → failure → generic rollback.
func TestSnapshotScriptAutoRollbackOnFailure(t *testing.T) {
	r := snapRunner(t,
		dep("shop", "api", 5),
		cm("shop", "cfg", map[string]any{"mode": "prod", "keep": "yes"}),
	)
	ctx := context.Background()

	src := `
step("scale down")
k8s.scale("shop", "Deployment", "api", 1)
step("flip config")
k8s.patch_configmap("shop", "cfg", "mode", "maint")
step("boom")
fail("verification failed")
`
	log, err := r.runCapturedScript(ctx, src, nil, nil)
	if err == nil {
		t.Fatal("expected the script to fail")
	}

	// mid-run state: mutations applied
	if getDepReplicas(t, r, "shop", "api") != 1 {
		t.Fatal("precondition: scale did not apply before failure")
	}
	if getCMData(t, r, "shop", "cfg")["mode"] != "maint" {
		t.Fatal("precondition: configmap patch did not apply")
	}

	// the caller rolls back on failure — the one generic mechanism
	res := r.Restore(ctx, log.List())
	for _, x := range res {
		if !x.OK {
			t.Fatalf("restore failed for %s/%s: %s", x.Kind, x.Name, x.Detail)
		}
	}

	if n := getDepReplicas(t, r, "shop", "api"); n != 5 {
		t.Fatalf("deployment not rolled back: replicas=%d", n)
	}
	d := getCMData(t, r, "shop", "cfg")
	if d["mode"] != "prod" {
		t.Fatalf("configmap key not rolled back: mode=%v", d["mode"])
	}
	if d["keep"] != "yes" {
		t.Fatalf("sibling key clobbered by rollback: keep=%v", d["keep"])
	}
}

// The durable path: a script's capture log is persisted as JSON (as the CP
// stores it), then the reaper-enqueued snapshot_restore replays it from params —
// proving crash-recovery + manual-revert work off the persisted list alone.
func TestSnapshotRestoreFromDurableParams(t *testing.T) {
	r := snapRunner(t,
		dep("shop", "api", 6),
		cm("shop", "cfg", map[string]any{"k": "orig"}),
	)
	ctx := context.Background()
	src := `
k8s.scale("shop", "Deployment", "api", 2)
k8s.patch_configmap("shop", "cfg", "k", "changed")
`
	log, err := r.runCapturedScript(ctx, src, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// persist the list exactly as the CP would (snapshots jsonb array)
	listJSON, _ := json.Marshal(log.List())
	params, _ := json.Marshal(map[string]any{"snapshots": json.RawMessage(listJSON)})

	// a "peer agent" replays it purely from the durable params
	results, err := r.restoreFromParams(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range results {
		if !x.OK {
			t.Fatalf("restore failed: %s/%s %s", x.Kind, x.Name, x.Detail)
		}
	}
	if getDepReplicas(t, r, "shop", "api") != 6 {
		t.Fatal("deployment not restored from durable list")
	}
	if getCMData(t, r, "shop", "cfg")["k"] != "orig" {
		t.Fatal("configmap not restored from durable list")
	}
}

// Each capture fires onAdd the instant it is recorded — the durable-report hook.
// This proves captures are reportable BEFORE the run ends (crash-safe), in order.
func TestCaptureOnAddFiresPerCapture(t *testing.T) {
	r := snapRunner(t, dep("shop", "api", 3), cm("shop", "cfg", map[string]any{"k": "v"}))
	ctx := context.Background()
	log := newCaptureLog()
	var reported []Capture
	log.onAdd = func(c Capture) { reported = append(reported, c) } // execute() would POST each to the CP

	if _, err := r.captureObject(ctx, log, "Deployment", "shop", "api"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.captureFields(ctx, log, "ConfigMap", "shop", "cfg", [][]string{{"data", "k"}}); err != nil {
		t.Fatal(err)
	}
	if len(reported) != 2 {
		t.Fatalf("onAdd fired %d times, want 2", len(reported))
	}
	if reported[0].Seq != 0 || reported[0].Kind != "Deployment" || reported[1].Seq != 1 || reported[1].Scope != "field" {
		t.Fatalf("captures reported out of order/shape: %+v", reported)
	}
}

// The happy path: a successful script leaves its mutations in place (no restore).
func TestSnapshotScriptSuccessKeepsMutations(t *testing.T) {
	r := snapRunner(t, dep("shop", "web", 2))
	ctx := context.Background()
	src := `
k8s.scale("shop", "Deployment", "web", 4)
`
	log, err := r.runCapturedScript(ctx, src, nil, nil)
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	if len(log.List()) != 1 {
		t.Fatalf("expected 1 capture, got %d", len(log.List()))
	}
	if getDepReplicas(t, r, "shop", "web") != 4 {
		t.Fatal("successful mutation should persist")
	}
}
