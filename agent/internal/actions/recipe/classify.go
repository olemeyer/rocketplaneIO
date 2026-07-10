package recipe

// classify.go derives a recipe's blast profile from its DATA, with no execution.
// Because the manifest declares its reversibility, step kinds, targets and risk,
// a reviewer (or the copilot's risk gate) can trust the grade before an action
// is ever approved — the property a dynamic script cannot offer.

import "sort"

// Classification is the static blast profile of a recipe.
type Classification struct {
	Reversibility string   `json:"reversibility"` // full | partial | none
	Mutates       bool     `json:"mutates"`
	BlastKinds    []string `json:"blastKinds"` // target kinds it can touch
	Risk          string   `json:"risk"`       // reversible | destructive | external
}

// Classify computes the base (parameter-independent) profile.
func Classify(m *Manifest) Classification {
	kinds := append([]string(nil), m.Targets...)
	sort.Strings(kinds)
	return Classification{
		Reversibility: m.Reversibility,
		Mutates:       m.mutating(),
		BlastKinds:    kinds,
		Risk:          baseRisk(m),
	}
}

// ClassifyWithParams refines the risk grade with concrete params: e.g. scale is
// reversible, but `scale replicas=0` escalates to destructive via risk.when. The
// structure (reversibility, blast) is parameter-independent; only the grade moves.
func ClassifyWithParams(m *Manifest, params map[string]any) Classification {
	c := Classify(m)
	if m.Risk != nil && m.Risk.When != "" {
		if hit, err := evalWhen(m.Risk.When, params); err == nil && hit {
			c.Risk = m.Risk.Then
		}
	}
	return c
}

func baseRisk(m *Manifest) string {
	if m.Risk != nil && m.Risk.Base != "" {
		return m.Risk.Base
	}
	switch m.Reversibility {
	case "none":
		return "destructive"
	case "partial":
		return "destructive"
	default:
		return "reversible"
	}
}
