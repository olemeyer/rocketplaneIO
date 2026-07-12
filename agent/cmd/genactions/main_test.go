package main

import (
	"strings"
	"testing"
)

// Locks the front-matter grammar the generator understands — it must stay in sync
// with the web parser (apps/web/lib/workflow-frontmatter.ts).
func TestParseScript(t *testing.T) {
	src := strings.Join([]string{
		"# @name scale-workload",
		"# @summary Scale it",
		"# @risk medium",
		"# @reversible snapshot",
		"# @targets Deployment,StatefulSet",
		"#",
		"# prose comment stays",
		`snapshot(ns, "Deployment", name)`,
		`k8s.scale(ns, "Deployment", name, 2)`,
	}, "\n")

	m, err := parseScript("scale", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "scale-workload" || m.Summary != "Scale it" || m.Risk != "medium" || m.Reversible != "snapshot" {
		t.Fatalf("front-matter mismatch: %+v", m)
	}
	if len(m.Targets) != 2 || m.Targets[0] != "Deployment" || m.Targets[1] != "StatefulSet" {
		t.Fatalf("targets: %v", m.Targets)
	}
	// Source strips the @-directive lines but keeps prose + code…
	if strings.Contains(m.Source, "@name") {
		t.Fatalf("Source still carries front-matter directives:\n%s", m.Source)
	}
	if !strings.Contains(m.Source, "prose comment stays") || !strings.Contains(m.Source, "k8s.scale") {
		t.Fatalf("Source lost prose/code:\n%s", m.Source)
	}
	// …while Full keeps the verbatim file (incl. front-matter) for forking.
	if !strings.Contains(m.Full, "@name scale-workload") || !strings.Contains(m.Full, "k8s.scale") {
		t.Fatalf("Full not verbatim:\n%s", m.Full)
	}
}

func TestParseScriptRequiresFrontmatter(t *testing.T) {
	if _, err := parseScript("x", "k8s.scale(a,b,c,1)\n"); err == nil {
		t.Fatal("expected an error for missing front-matter")
	}
}
