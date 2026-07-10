package recipe

import (
	"reflect"
	"strings"
	"testing"
)

// mustFail asserts a manifest is REJECTED at parse and the reason mentions want.
func mustFail(t *testing.T, name, yaml, want string) {
	t.Helper()
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatalf("%s: expected parse to FAIL, but it succeeded", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error %q does not mention %q", name, err.Error(), want)
	}
}

// The built-in scale.yaml must parse — if it violated a guarantee, the package
// would fail to load here.
func TestBuiltinScaleParses(t *testing.T) {
	m, ok, err := Builtin("scale")
	if err != nil || !ok {
		t.Fatalf("Builtin(scale): ok=%v err=%v", ok, err)
	}
	if m.Recipe != "scale" || m.Reversibility != "full" || len(m.Steps) != 4 {
		t.Fatalf("unexpected scale manifest: %+v", m)
	}
}

// THE HEADLINE: a recipe that declares itself reversible with no compensation
// does not parse.
func TestReversibleNeedsCompensation(t *testing.T) {
	mustFail(t, "reversible-no-compensation", `
recipe: bad
title: Bad
reversibility: full
compensation: none
steps:
  - { name: trigger, kind: mutate }
  - { name: verify, kind: verify }
`, "declares no `compensation`")
}

// An irreversible recipe must not pretend it can be undone.
func TestIrreversibleRejectsCompensation(t *testing.T) {
	mustFail(t, "irreversible-with-compensation", `
recipe: bad
title: Bad
reversibility: none
compensation: builtin
steps:
  - { name: nuke, kind: mutate }
  - { name: verify, kind: verify }
`, "declares a compensation")
}

// A mutation with no owned verify step is fire-and-hope — rejected.
func TestMutationNeedsVerify(t *testing.T) {
	mustFail(t, "no-verify", `
recipe: bad
title: Bad
reversibility: full
compensation: builtin
steps:
  - { name: trigger, kind: mutate }
`, "no owned verify")
}

// reversibility is mandatory and closed.
func TestBadReversibility(t *testing.T) {
	mustFail(t, "bad-reversibility", `
recipe: bad
title: Bad
reversibility: sometimes
compensation: none
steps:
  - { name: look, kind: observe }
`, "reversibility must be")
}

// A step kind must be one of the four.
func TestBadStepKind(t *testing.T) {
	mustFail(t, "bad-step-kind", `
recipe: bad
title: Bad
reversibility: none
compensation: none
steps:
  - { name: weird, kind: teleport }
`, "bad kind")
}

// classify reads the manifest statically — no execution.
func TestClassifyScaleStatic(t *testing.T) {
	m, _, _ := Builtin("scale")
	c := Classify(m)
	if c.Reversibility != "full" || !c.Mutates {
		t.Fatalf("classify = %+v, want full/mutates", c)
	}
	if !reflect.DeepEqual(c.BlastKinds, []string{"Deployment", "StatefulSet"}) {
		t.Fatalf("blastKinds = %v", c.BlastKinds)
	}
	if c.Risk != "reversible" {
		t.Fatalf("base risk = %q, want reversible", c.Risk)
	}
}

// The grade is parameter-aware WITHOUT execution: scale-to-0 is destructive.
func TestClassifyParameterAware(t *testing.T) {
	m, _, _ := Builtin("scale")
	if r := ClassifyWithParams(m, map[string]any{"replicas": 0}).Risk; r != "destructive" {
		t.Fatalf("scale replicas=0 risk = %q, want destructive", r)
	}
	if r := ClassifyWithParams(m, map[string]any{"replicas": 3}).Risk; r != "reversible" {
		t.Fatalf("scale replicas=3 risk = %q, want reversible", r)
	}
}
