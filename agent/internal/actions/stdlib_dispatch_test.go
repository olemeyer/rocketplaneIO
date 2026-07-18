package actions

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Every embedded built-in script must be syntactically valid — a typo would
// otherwise only surface when that kind runs in production.
func TestAllStdlibScriptsParse(t *testing.T) {
	if len(stdlibScripts) == 0 {
		t.Fatal("no stdlib scripts embedded")
	}
	opts := scriptFileOptions()
	for kind, src := range stdlibScripts {
		if _, err := opts.Parse(kind+".star", src, 0); err != nil {
			t.Fatalf("stdlib/%s.star does not parse: %v", kind, err)
		}
	}
	// the six generic action kinds must be present
	for _, k := range []string{"k8s_get", "k8s_list", "k8s_patch", "k8s_apply", "k8s_delete", "k8s_exec"} {
		if _, ok := stdlibScripts[k]; !ok {
			t.Fatalf("missing stdlib script for %q", k)
		}
	}
}

// A built-in kind routes through execute() to its embedded .star, which
// snapshots + mutates + reports the capture durably — the single execution path.
func TestBuiltinDispatchViaFlag(t *testing.T) {
	cp := newStubCP()
	defer cp.Close()
	r := snapExecRunner(t, cp.URL, node("worker-7", false))

	// k8s_patch: cordon the node via the generic patch kind
	patchJSON := `{"spec":{"unschedulable":true}}`
	params, _ := json.Marshal(map[string]any{"patch": patchJSON})
	a := Action{
		ID:              "act-patch",
		Kind:            "k8s_patch",
		TargetNamespace: "-",
		TargetKind:      "Node",
		TargetName:      "worker-7",
		Params:          params,
	}
	r.execute(context.Background(), a)

	// the node was cordoned by the embedded k8s_patch script
	u, err := r.dyn.Resource(nodeGVR).Get(context.Background(), "worker-7", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	un, _, _ := nestedBool(u.Object, "spec", "unschedulable")
	if !un {
		t.Fatal("dispatched k8s_patch script did not cordon the node")
	}

	// and its snapshot was reported DURABLY (so a later revert can undo it)
	durable := false
	var captured []Capture
	for _, rep := range cp.snapshot() {
		if rep.Status == "running" && len(rep.Snapshots) > 0 && string(rep.Snapshots) != "null" {
			durable = true
			_ = json.Unmarshal(rep.Snapshots, &captured)
		}
	}
	if !durable {
		t.Fatal("k8s_patch dispatch did not report a durable snapshot")
	}

	// prove the durable snapshot restores the node (uncordons it)
	r.Restore(context.Background(), captured)
	u2, _ := r.dyn.Resource(nodeGVR).Get(context.Background(), "worker-7", metav1.GetOptions{})
	un2, _, _ := nestedBool(u2.Object, "spec", "unschedulable")
	if un2 {
		t.Fatal("restore from the dispatched k8s_patch durable snapshot did not uncordon")
	}
}

func nestedBool(obj map[string]any, path ...string) (bool, bool, error) {
	v, ok := nestedGet(obj, path)
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	return b, ok, nil
}
