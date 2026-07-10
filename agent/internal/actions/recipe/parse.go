package recipe

// parse.go turns manifest bytes into a validated Manifest. Validate is where the
// v4 thesis lives: the safety properties are enforced at PARSE, so an unsafe
// recipe cannot exist as a value. The load-bearing checks:
//
//  1. reversibility is declared and valid (full | partial | none);
//  2. a recipe that is NOT `none` MUST declare a `builtin` compensation — a
//     reversible recipe with no inverse does not parse (the structural kill for
//     the silent-nil-undo drop);
//  3. a recipe that mutates MUST contain an owned verify step (a mutation with
//     no convergence check is fire-and-hope);
//  4. `none` reversibility must not also claim a compensation (no lying).
//  5. every risk/when expression parses and references a declared param.

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

var validRisk = map[string]bool{"reversible": true, "destructive": true, "external": true}
var validType = map[string]bool{"int": true, "string": true, "bool": true}
var validRev = map[string]bool{"full": true, "partial": true, "none": true}
var validStepKind = map[string]bool{"read": true, "mutate": true, "observe": true, "verify": true}

// Parse decodes a manifest (YAML or JSON) and validates it. A returned error
// means the recipe is unsafe or malformed and must not be stored or run.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("recipe: parse: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate enforces the parse-time guarantees. Pure and cluster-free.
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Recipe) == "" {
		return fmt.Errorf("recipe: missing `recipe` key")
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("recipe %q: missing `title`", m.Recipe)
	}
	if !validRev[m.Reversibility] {
		return fmt.Errorf("recipe %q: reversibility must be full|partial|none (got %q)", m.Recipe, m.Reversibility)
	}
	if len(m.Steps) == 0 {
		return fmt.Errorf("recipe %q: has no steps", m.Recipe)
	}
	for i, s := range m.Steps {
		if s.Name == "" {
			return fmt.Errorf("recipe %q: step %d has no name", m.Recipe, i)
		}
		if !validStepKind[s.Kind] {
			return fmt.Errorf("recipe %q: step %q has bad kind %q (read|mutate|observe|verify)", m.Recipe, s.Name, s.Kind)
		}
	}

	// THE HEADLINE: a reversible recipe must declare how it compensates; an
	// irreversible one must not pretend it can.
	switch m.Compensation {
	case "builtin":
		if m.Reversibility == "none" {
			return fmt.Errorf("recipe %q: reversibility=none but declares a compensation — refused", m.Recipe)
		}
	case "none", "":
		if m.Reversibility != "none" {
			return fmt.Errorf("recipe %q: reversibility=%s but declares no `compensation` — refused at parse", m.Recipe, m.Reversibility)
		}
		m.Compensation = "none"
	default:
		return fmt.Errorf("recipe %q: compensation must be builtin|none (got %q)", m.Recipe, m.Compensation)
	}

	// A mutation with no owned convergence check is not "done".
	if m.mutating() && !m.hasVerify() {
		return fmt.Errorf("recipe %q: mutates but has no owned verify step", m.Recipe)
	}

	// Params + risk expressions.
	params := map[string]bool{}
	for _, p := range m.Params {
		if p.Name == "" {
			return fmt.Errorf("recipe %q: a param has no name", m.Recipe)
		}
		if !validType[p.Type] {
			return fmt.Errorf("recipe %q: param %q has bad type %q (int|string|bool)", m.Recipe, p.Name, p.Type)
		}
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return fmt.Errorf("recipe %q: param %q min>max", m.Recipe, p.Name)
		}
		params[p.Name] = true
	}
	if m.Risk != nil {
		if m.Risk.Base != "" && !validRisk[m.Risk.Base] {
			return fmt.Errorf("recipe %q: risk.base %q invalid", m.Recipe, m.Risk.Base)
		}
		if m.Risk.When != "" {
			if m.Risk.Then == "" || !validRisk[m.Risk.Then] {
				return fmt.Errorf("recipe %q: risk.when needs a valid risk.then", m.Recipe)
			}
			if err := validateWhen(m.Risk.When, params); err != nil {
				return fmt.Errorf("recipe %q: risk.%w", m.Recipe, err)
			}
		}
	}
	return nil
}
