// Package recipe is the Safe Actions v4 substrate: an action is typed DATA — a
// declarative Effect manifest — not authored code. Every built-in kind ships a
// manifest that declares, up front and machine-checkably:
//
//   - its risk grade (parameter-aware) and reversibility,
//   - its typed params,
//   - its pipeline as an ordered list of steps (trigger → observe → verify),
//   - how it compensates (a built-in inverse, or honestly `none`).
//
// The guarantee is structural: a manifest that declares itself reversible but
// names no compensation does NOT parse. Reversibility becomes a parse-time
// property instead of a runtime hope — the class of bug where a mutation runs
// and its undo is silently dropped cannot be expressed.
//
// The agent executes the manifest through the audited pipeline/inverse helpers;
// a per-kind drift test asserts the manifest's steps faithfully describe that
// pipeline, so the declaration can never quietly diverge from what runs. The
// package is pure (no Kubernetes client) so parse, validate and classify are
// unit-testable with no cluster.
package recipe

// Manifest is one action recipe. A built-in and a user fork are the same shape.
type Manifest struct {
	Recipe        string      `json:"recipe" yaml:"recipe"`
	Title         string      `json:"title" yaml:"title"`
	Targets       []string    `json:"targets,omitempty" yaml:"targets,omitempty"`
	Reversibility string      `json:"reversibility" yaml:"reversibility"` // full | partial | none
	Compensation  string      `json:"compensation" yaml:"compensation"`   // builtin | none
	Risk          *RiskRule   `json:"risk,omitempty" yaml:"risk,omitempty"`
	Params        []ParamSpec `json:"params,omitempty" yaml:"params,omitempty"`
	Steps         []Step      `json:"steps" yaml:"steps"`
}

// RiskRule declares the base grade and one optional parameter-aware escalation,
// so classify can derive a truthful grade WITHOUT executing anything (scale is
// reversible, but scale-to-0 is destructive). `when` is a bounded comparison
// over params only — see expr.go.
type RiskRule struct {
	Base string `json:"base" yaml:"base"` // reversible | destructive | external
	When string `json:"when,omitempty" yaml:"when,omitempty"`
	Then string `json:"then,omitempty" yaml:"then,omitempty"`
}

// ParamSpec is a typed input. params ARE the manifest: the form, the validation
// and the risk expression all read this one list.
type ParamSpec struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type" yaml:"type"` // int | string | bool
	Min      *int   `json:"min,omitempty" yaml:"min,omitempty"`
	Max      *int   `json:"max,omitempty" yaml:"max,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Default  any    `json:"default,omitempty" yaml:"default,omitempty"`
}

// Step is one link of the executed pipeline. `kind` classifies it so classify()
// can read the effect surface statically; a `mutate` step is what makes a recipe
// need a compensation and an owned verify.
type Step struct {
	Name string `json:"name" yaml:"name"`
	Kind string `json:"kind" yaml:"kind"` // read | mutate | observe | verify
}

// mutating reports whether the recipe changes cluster state (and therefore owes
// a compensation + an owned verify).
func (m *Manifest) mutating() bool {
	for _, s := range m.Steps {
		if s.Kind == "mutate" {
			return true
		}
	}
	return false
}

func (m *Manifest) hasVerify() bool {
	for _, s := range m.Steps {
		if s.Kind == "verify" {
			return true
		}
	}
	return false
}
