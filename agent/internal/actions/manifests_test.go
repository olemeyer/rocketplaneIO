package actions

import (
	"encoding/json"
	"testing"

	"github.com/rocketplaneio/rocketplane/agent/internal/actions/recipe"
)

// This file is the per-kind conformance suite: every built-in action kind must
// (1) ship a manifest that parses, (2) declare a reversibility consistent with
// whether the code actually has an inverse, (3) route through the manifest
// engine (execute → executeManifest), and (4) resolve to a real pipeline in
// plan() rather than the "reject" default. Each kind is its own subtest, so a
// regression names the exact action.

// reversibleKinds have a prepareRevert arm → manifest reversibility=full,
// compensation=builtin.
var reversibleKinds = map[string]bool{
	"scale": true, "set_image": true, "rollout_pause": true, "rollout_resume": true,
	"rollout_undo": true, "rollout_to_revision": true, "hpa_set": true, "hpa_toggle": true,
	"cordon": true, "uncordon": true, "drain": true, "cronjob_suspend": true, "cronjob_resume": true,
	"node_taint": true, "node_untaint": true, "annotate": true, "set_label": true,
	"patch_configmap": true, "set_env": true, "set_resources": true, "statefulset_partition": true,
	"patch_secret": true, "create_configmap": true, "delete_configmap": true, "pdb_set": true,
	"patch_resource": true, "restore_resource": true,
}

// readKinds are observe-only (no mutation).
var readKinds = map[string]bool{
	"pod_events": true, "debug_bundle": true, "rollout_history": true, "drain_preview": true,
	"get_resource": true, "describe_resource": true, "get_secret": true, "helm_releases": true,
	"exec_readonly": true, "pod_logs": true, "list_events": true, "net_probe": true,
}

// irreversibleKinds mutate but have no clean inverse.
var irreversibleKinds = map[string]bool{
	"rollout_restart": true, "delete_pod": true, "evict_pod": true, "cleanup_pods": true,
	"cleanup_jobs": true, "cronjob_trigger": true, "delete_job": true, "run_debug_pod": true,
	"pvc_expand": true,
}

// genericParams satisfies the param reads of any plan* builder, so plan() can be
// exercised for every kind without a bespoke fixture.
var genericParams = json.RawMessage(`{"replicas":1,"image":"nginx:1","container":"c","key":"k","value":"v","name":"n","revision":1,"partition":0,"minReplicas":1,"maxReplicas":2,"enabled":true,"effect":"NoSchedule","size":"1Gi","command":"ls","patch":{}}`)

func allKinds() []string {
	out := []string{}
	for k := range reversibleKinds {
		out = append(out, k)
	}
	for k := range readKinds {
		out = append(out, k)
	}
	for k := range irreversibleKinds {
		out = append(out, k)
	}
	return out
}

func TestEveryKindHasConsistentManifest(t *testing.T) {
	r := manifestRunner(t, "http://localhost:0")
	for _, kind := range allKinds() {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			// 1) manifest exists + parses.
			m, ok, err := recipe.Builtin(kind)
			if err != nil {
				t.Fatalf("manifest load error: %v", err)
			}
			if !ok {
				t.Fatalf("no manifest for kind %q — it would not route through the v4 engine", kind)
			}

			// 2) reversibility consistent with the code's actual inverse.
			wantReversible := reversibleKinds[kind]
			gotReversible := m.Reversibility != "none"
			if gotReversible != wantReversible {
				t.Fatalf("reversibility=%q (compensation=%q) but reversibleKinds says %v", m.Reversibility, m.Compensation, wantReversible)
			}
			if wantReversible && m.Compensation != "builtin" {
				t.Fatalf("reversible kind must declare compensation=builtin, got %q", m.Compensation)
			}

			// 3) classify is consistent: read kinds do not mutate; the rest do.
			c := recipe.Classify(m)
			wantMutates := !readKinds[kind]
			if c.Mutates != wantMutates {
				t.Fatalf("classify.Mutates=%v, want %v", c.Mutates, wantMutates)
			}

			// 4) the kind resolves to a real pipeline (not the reject default),
			//    so execute→executeManifest→plan actually does something.
			target := "Deployment"
			if len(m.Targets) > 0 {
				target = m.Targets[0]
			}
			a := Action{Kind: kind, TargetNamespace: "ns", TargetKind: target, TargetName: "obj", Params: genericParams}
			steps := safePlan(t, r, a)
			if len(steps) == 0 {
				t.Fatalf("plan(%q) produced no steps", kind)
			}
			if len(steps) == 1 && steps[0].name == "reject" {
				t.Fatalf("plan(%q) hit the reject default — kind not handled", kind)
			}
		})
	}
}

// safePlan calls plan() but converts a panic (a plan* builder that dereferences
// an unexpected param) into a test failure naming the kind, rather than crashing
// the whole suite.
func safePlan(t *testing.T, r *Runner, a Action) (steps []step) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("plan(%q) panicked: %v", a.Kind, rec)
		}
	}()
	return r.plan(a)
}
