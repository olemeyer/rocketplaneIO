package telemetry

import (
	"net/url"
	"strings"
	"testing"
)

func TestLogsQueryValidate(t *testing.T) {
	cases := []struct {
		name    string
		q       LogsQuery
		wantErr bool
	}{
		{"empty", LogsQuery{}, false},
		{"valid regex", LogsQuery{Regexes: []string{"(?i)oom|out of memory"}}, false},
		{"invalid regex", LogsQuery{Regexes: []string{"("}}, true},
		{"too many regexes", LogsQuery{Regexes: []string{"a", "b", "c", "d", "e", "f"}}, true},
		{"empty regex", LogsQuery{Regexes: []string{""}}, true},
		{"regex too long", LogsQuery{Regexes: []string{strings.Repeat("a", 513)}}, true},
		{"bad mode", LogsQuery{RegexMode: "sometimes"}, true},
		{"mode any", LogsQuery{RegexMode: "any"}, false},
		{"mode all", LogsQuery{RegexMode: "all"}, false},
		{"too many excludes", LogsQuery{Exclude: []string{"a", "b", "c", "d", "e", "f"}}, true},
		{"empty exclude", LogsQuery{Exclude: []string{""}}, true},
		{"fuzzy too long", LogsQuery{Fuzzy: strings.Repeat("x", 513)}, true},
		{"fuzzy ok", LogsQuery{Fuzzy: "conection refused"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.q.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestSearchCondsRegexAnyAll(t *testing.T) {
	params := url.Values{}
	q := LogsQuery{Regexes: []string{"oom", "exit code 137"}}
	sql := q.searchConds(params)
	if !strings.Contains(sql, "match(Body, {rx0:String}) OR match(Body, {rx1:String})") {
		t.Fatalf("any-mode should OR the patterns, got %q", sql)
	}
	if params.Get("param_rx0") != "oom" || params.Get("param_rx1") != "exit code 137" {
		t.Fatalf("params not set: %v", params)
	}

	params = url.Values{}
	q.RegexMode = "all"
	sql = q.searchConds(params)
	if !strings.Contains(sql, "match(Body, {rx0:String}) AND match(Body, {rx1:String})") {
		t.Fatalf("all-mode should AND the patterns, got %q", sql)
	}
}

func TestSearchCondsExcludeAndFuzzy(t *testing.T) {
	params := url.Values{}
	q := LogsQuery{Exclude: []string{"health check", "probe"}, Fuzzy: "conection"}
	sql := q.searchConds(params)
	if !strings.Contains(sql, "positionCaseInsensitive(Body, {ex0:String}) = 0") ||
		!strings.Contains(sql, "positionCaseInsensitive(Body, {ex1:String}) = 0") {
		t.Fatalf("exclude conditions missing: %q", sql)
	}
	if !strings.Contains(sql, "ngramSearchCaseInsensitive(Body, {fz:String}) > 0.4") {
		t.Fatalf("fuzzy condition missing: %q", sql)
	}
	if params.Get("param_ex0") != "health check" || params.Get("param_fz") != "conection" {
		t.Fatalf("params not set: %v", params)
	}
}

func TestSearchCondsEmpty(t *testing.T) {
	params := url.Values{}
	if got := (LogsQuery{}).searchConds(params); got != "" {
		t.Fatalf("empty query should add no conditions, got %q", got)
	}
	if len(params) != 0 {
		t.Fatalf("empty query should set no params, got %v", params)
	}
}

// A missing table means "the optional eBPF pipeline was never installed", not
// "the query is broken" — the distinction is what an agent acts on.
func TestMissingTableDetection(t *testing.T) {
	ch := "Code: 60. DB::Exception: Unknown table expression identifier 'otel.otel_metrics_gauge' in scope SELECT"
	tbl, ok := missingTable(ch)
	if !ok || tbl != "otel.otel_metrics_gauge" {
		t.Fatalf("got %q, %v", tbl, ok)
	}
	if _, ok := missingTable("Code: 62. Syntax error"); ok {
		t.Fatal("syntax errors must not be reported as a missing pipeline")
	}
	if tbl, ok := missingTable("Code: 60. UNKNOWN_TABLE"); !ok || tbl != "unknown table" {
		t.Fatalf("unnamed table: got %q, %v", tbl, ok)
	}
}
