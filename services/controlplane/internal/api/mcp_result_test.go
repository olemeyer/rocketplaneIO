package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Truncation must never hand back a JSON fragment: a model asked to read
// half a document will interpret it rather than reject it.
func TestCapResultKeepsJSONValid(t *testing.T) {
	big := `{"nodes":["` + strings.Repeat("x", mcpResultCap) + `"]}`
	got := capResult(big)
	if !json.Valid([]byte(got)) {
		t.Fatalf("capped result is not valid JSON: %.120s", got)
	}
	var env struct {
		Truncated  bool `json:"truncated"`
		TotalBytes int  `json:"totalBytes"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil || !env.Truncated || env.TotalBytes != len(big) {
		t.Fatalf("envelope wrong: %v %+v", err, env)
	}
	if small := `{"ok":true}`; capResult(small) != small {
		t.Fatal("small payloads must pass through untouched")
	}
}

func TestFilterServiceMap(t *testing.T) {
	payload := `{"namespaces":["a","b"],
	  "nodes":[{"id":"a/Deployment/x","namespace":"a","health":"critical"},
	           {"id":"a/Deployment/y","namespace":"a","health":"healthy"},
	           {"id":"b/Deployment/z","namespace":"b","health":"critical"}],
	  "edges":[{"from":"a/Deployment/x","to":"a/Deployment/y"},
	           {"from":"a/Deployment/x","to":"b/Deployment/z"}]}`

	var out struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(filterServiceMap(payload, "a", "")), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 2 || len(out.Edges) != 1 {
		t.Fatalf("namespace filter: %d nodes, %d edges", len(out.Nodes), len(out.Edges))
	}

	// An edge survives only when BOTH endpoints do: x→y drops because y is
	// healthy, x→z stays because both ends are critical. A dangling edge would
	// point at a node the caller cannot see.
	if err := json.Unmarshal([]byte(filterServiceMap(payload, "", "critical")), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 2 || len(out.Edges) != 1 {
		t.Fatalf("health filter: %d nodes, %d edges", len(out.Nodes), len(out.Edges))
	}
	if out.Edges[0]["to"] != "b/Deployment/z" {
		t.Fatalf("wrong edge survived: %v", out.Edges[0])
	}

	if got := filterServiceMap(payload, "", ""); got != payload {
		t.Fatal("no filter must pass the payload through untouched")
	}
	if got := filterServiceMap("not json", "a", ""); got != "not json" {
		t.Fatal("unparseable payload must pass through, not fail the read")
	}
}
