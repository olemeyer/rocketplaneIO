package api

import (
	"strings"
	"testing"
)

func TestGrepResult(t *testing.T) {
	cacheToolResult("run1", "call1", "line one\nERROR: oom killed\nline three\nerror: retry\n")
	out, err := execUtilTool("run1", "grep_result", map[string]any{"ref": "call1", "pattern": "(?i)error"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 match(es)") || !strings.Contains(out, "oom killed") {
		t.Fatalf("out = %q", out)
	}
	// invert
	out, err = execUtilTool("run1", "grep_result", map[string]any{"ref": "call1", "pattern": "(?i)error", "invert": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "oom killed") || !strings.Contains(out, "line one") {
		t.Fatalf("invert out = %q", out)
	}
	// unbekannte ref
	if _, err = execUtilTool("run1", "grep_result", map[string]any{"ref": "nope", "pattern": "x"}); err == nil {
		t.Fatal("expected error for unknown ref")
	}
	// kaputte Regex
	if _, err = execUtilTool("run1", "grep_result", map[string]any{"ref": "call1", "pattern": "("}); err == nil {
		t.Fatal("expected error for bad regex")
	}
}

func TestJSONQuery(t *testing.T) {
	cacheToolResult("run2", "c1", `{"lines":[{"body":"a","sev":17},{"body":"b","sev":9}]}`)
	out, err := execUtilTool("run2", "json_query", map[string]any{"ref": "c1", "path": "lines.#(sev>=17)#.body"})
	if err != nil {
		t.Fatal(err)
	}
	if out != `["a"]` {
		t.Fatalf("out = %q", out)
	}
	out, err = execUtilTool("run2", "json_query", map[string]any{"ref": "c1", "path": "nothing.here"})
	if err != nil || !strings.Contains(out, "matched nothing") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBase64WithVault(t *testing.T) {
	// Klartext-Roundtrip
	enc, err := execUtilTool("run3", "base64", map[string]any{"op": "encode", "data": "hello"})
	if err != nil || enc != "aGVsbG8=" {
		t.Fatalf("enc=%q err=%v", enc, err)
	}
	dec, err := execUtilTool("run3", "base64", map[string]any{"op": "decode", "data": "aGVsbG8="})
	if err != nil || dec != "hello" {
		t.Fatalf("dec=%q err=%v", dec, err)
	}
	// Secret-Input → Ergebnis bleibt maskiert
	ph := vaultPut("run3", "s3cr3t")
	out, err := execUtilTool("run3", "base64", map[string]any{"op": "encode", "data": ph})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "s3cr3t") || !vaultHasPlaceholder(out) {
		t.Fatalf("secret leaked or not re-masked: %q", out)
	}
	// Aufgelöst muss der neue Platzhalter das encodierte Secret ergeben.
	if got := vaultResolve("run3", out); got != "czNjcjN0" {
		t.Fatalf("resolved = %q", got)
	}
}

func TestVaultResolveUnknownToken(t *testing.T) {
	s := "prefix {{secret:aaaaaaaaaaaaaaaaaaaaaaaa}} suffix"
	if got := vaultResolve("unknown-run", s); got != s {
		t.Fatalf("unknown tokens must stay masked, got %q", got)
	}
}

func TestParseYAMLAndDiff(t *testing.T) {
	out, err := execUtilTool("r", "parse_yaml", map[string]any{"data": "a: 1\nb:\n  - x\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"a":1`) {
		t.Fatalf("yaml→json = %q", out)
	}
	d, err := execUtilTool("r", "diff_text", map[string]any{"a": "one\ntwo\nthree", "b": "one\nTWO\nthree"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "- two") || !strings.Contains(d, "+ TWO") {
		t.Fatalf("diff = %q", d)
	}
	same, err := execUtilTool("r", "diff_text", map[string]any{"a": "x", "b": "x"})
	if err != nil || same != "no differences" {
		t.Fatalf("same=%q err=%v", same, err)
	}
}

func TestResultCacheEviction(t *testing.T) {
	big := strings.Repeat("x", 40<<20)
	cacheToolResult("run-ev", "old", big)
	cacheToolResult("run-ev", "new", big) // 80 MiB > 64 MiB Cap → "old" fliegt
	if _, ok := cachedResult("run-ev", "old"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := cachedResult("run-ev", "new"); !ok {
		t.Fatal("newest entry must survive")
	}
}
