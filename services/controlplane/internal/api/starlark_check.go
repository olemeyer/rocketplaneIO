package api

// starlark_check.go — Save-Zeit-Verifikation der Workflows: die Source wird
// KOMPILIERT (parse + resolve, ohne Ausführung) gegen exakt die Umgebung, die
// der Agent bereitstellt. Kaputte Workflows (Syntaxfehler, unbekannte Namen,
// while/rekursive defs) können gar nicht erst gespeichert werden — der Editor
// bekommt Fehler mit Datei:Zeile zurück.

import (
	"encoding/json"
	"fmt"
	"regexp"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// scriptEnv MUSS mit den Builtins in agent/internal/actions/script.go
// übereinstimmen — sie ist der Vertrag der Script-Umgebung.
var scriptEnv = []string{
	"args", "step", "report", "fail", "sleep",
	"k8s", "wait_rollout", "wait_ready",
}

// scriptFileOptions spiegelt die Agent-Engine (While/Recursion aus).
func scriptFileOptions() *syntax.FileOptions {
	return &syntax.FileOptions{
		Set:             true,
		GlobalReassign:  true,
		TopLevelControl: true,
		While:           false,
		Recursion:       false,
	}
}

// checkScriptSource kompiliert die Source; Fehler kommen mit Position zurück.
func checkScriptSource(name, source string) error {
	predeclared := make(starlark.StringDict, len(scriptEnv))
	for _, n := range scriptEnv {
		predeclared[n] = starlark.None // nur Namen zählen fürs Resolve
	}
	_, _, err := starlark.SourceProgramOptions(scriptFileOptions(), name+".star", source, predeclared.Has)
	if err != nil {
		return fmt.Errorf("%v", err)
	}
	return nil
}

/* ── Typed Params: Struktur-Validierung beim Save, Args-Prüfung beim Run ── */

// ParamSpec ist die typed Parameter-Definition für die UI.
type ParamSpec struct {
	Name        string   `json:"name"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"` // string|int|bool|enum|namespace|workload
	Default     string   `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
}

var paramNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,39}$`)

func parseParamSpecs(raw json.RawMessage) ([]ParamSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var specs []ParamSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, fmt.Errorf("params must be a JSON array of parameter specs")
	}
	seen := map[string]bool{}
	for i, p := range specs {
		if !paramNamePattern.MatchString(p.Name) {
			return nil, fmt.Errorf("param %d: name must be snake_case (a-z, 0-9, _)", i+1)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("param %q: duplicate name", p.Name)
		}
		seen[p.Name] = true
		switch p.Type {
		case "", "string", "int", "bool", "enum", "namespace", "workload", "node":
		default:
			return nil, fmt.Errorf("param %q: unknown type %q", p.Name, p.Type)
		}
		if p.Type == "enum" && len(p.Options) == 0 {
			return nil, fmt.Errorf("param %q: enum needs options", p.Name)
		}
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return nil, fmt.Errorf("param %q: min > max", p.Name)
		}
	}
	return specs, nil
}

// validateArgs prüft Run-Argumente gegen die Specs — nichts Untypisiertes
// erreicht je den Cluster.
func validateArgs(specs []ParamSpec, args map[string]string) error {
	for _, p := range specs {
		v, ok := args[p.Name]
		if !ok || v == "" {
			if p.Required && p.Default == "" {
				return fmt.Errorf("arg %q is required", p.Name)
			}
			if v == "" && p.Default != "" {
				args[p.Name] = p.Default
				v = p.Default
			}
			if v == "" {
				continue
			}
		}
		switch p.Type {
		case "int":
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return fmt.Errorf("arg %q must be an integer", p.Name)
			}
			if p.Min != nil && n < *p.Min {
				return fmt.Errorf("arg %q must be ≥ %d", p.Name, *p.Min)
			}
			if p.Max != nil && n > *p.Max {
				return fmt.Errorf("arg %q must be ≤ %d", p.Name, *p.Max)
			}
		case "bool":
			if v != "true" && v != "false" {
				return fmt.Errorf("arg %q must be true or false", p.Name)
			}
		case "enum":
			found := false
			for _, o := range p.Options {
				if v == o {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("arg %q must be one of %v", p.Name, p.Options)
			}
		}
	}
	return nil
}
