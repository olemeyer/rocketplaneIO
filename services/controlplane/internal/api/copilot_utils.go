package api

// copilot_utils.go — Result-Cache + lokale Utility-Tools des Copilots.
// Tool-Ergebnisse gehen ans LLM geclamped (12 kB); das VOLLE Ergebnis liegt
// hier im Cache. Die Utilities (grep/jq/base64/yaml/diff/time) arbeiten lokal
// auf dem vollen Ergebnis — kein LLM-Roundtrip, keine Cluster-Interaktion.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
	"sigs.k8s.io/yaml"
)

/* ── Result-Cache ────────────────────────────────────────────────────────── */

const (
	resultCacheTTL    = 30 * time.Minute
	resultCacheMaxRun = 64 << 20 // 64 MiB pro Run
)

type runCache struct {
	entries map[string]string // callID → volles Ergebnis
	order   []string          // Einfüge-Reihenfolge (LRU-Eviction)
	size    int
	touched time.Time
}

var runResults = struct {
	mu sync.Mutex
	m  map[string]*runCache
}{m: map[string]*runCache{}}

// cacheToolResult legt das VOLLE Tool-Ergebnis für spätere grep/jq-Refs ab.
func cacheToolResult(runID, callID, content string) {
	if runID == "" || callID == "" || content == "" {
		return
	}
	runResults.mu.Lock()
	defer runResults.mu.Unlock()
	cut := time.Now().Add(-resultCacheTTL)
	for id, c := range runResults.m {
		if c.touched.Before(cut) {
			delete(runResults.m, id)
		}
	}
	c := runResults.m[runID]
	if c == nil {
		c = &runCache{entries: map[string]string{}}
		runResults.m[runID] = c
	}
	if old, ok := c.entries[callID]; ok {
		c.size -= len(old)
	} else {
		c.order = append(c.order, callID)
	}
	c.entries[callID] = content
	c.size += len(content)
	c.touched = time.Now()
	for c.size > resultCacheMaxRun && len(c.order) > 1 {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.size -= len(c.entries[oldest])
		delete(c.entries, oldest)
	}
}

func cachedResult(runID, callID string) (string, bool) {
	runResults.mu.Lock()
	defer runResults.mu.Unlock()
	c := runResults.m[runID]
	if c == nil {
		return "", false
	}
	c.touched = time.Now()
	v, ok := c.entries[callID]
	return v, ok
}

/* ── Utility-Tools ───────────────────────────────────────────────────────── */

// copilotUtilTools sind die lokalen Werkzeuge (für Master UND Investigator).
func copilotUtilTools() []copilotTool {
	sp := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	ip := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	bp := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	o := func(props map[string]any, req ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			m["required"] = req
		}
		return m
	}
	return []copilotTool{
		{"grep_result", "Grep (RE2 regex) over the FULL result of an earlier tool call — earlier results are often truncated for you, but the complete payload is cached. Use the tool call id as ref. Returns matching lines with optional context. Local, instant, no cluster access.", o(map[string]any{"ref": sp("tool call id whose full result to search"), "pattern": sp("RE2 regex (prefix (?i) for case-insensitive)"), "invert": bp("return NON-matching lines instead"), "context": ip("lines of context around each match (0-5)"), "maxMatches": ip("cap matches (default 50, max 200)")}, "ref", "pattern")},
		{"json_query", "Extract values from the FULL JSON result of an earlier tool call using a gjson path, e.g. 'lines.#.body', 'spans.#(durationMs>500)#.serviceName', 'kinds.#(kind==\"Ingress\").items'. Use instead of re-fetching when the data is already there. Local, instant.", o(map[string]any{"ref": sp("tool call id whose full result to query"), "path": sp("gjson path expression")}, "ref", "path")},
		{"base64", "Encode or decode base64 (e.g. Kubernetes Secret values). Provide data inline. Secret placeholders ({{secret:…}}) are resolved server-side and the output is re-masked — you never see the plaintext.", o(map[string]any{"op": sp("encode | decode"), "data": sp("the input string")}, "op", "data")},
		{"parse_yaml", "Parse YAML into JSON (e.g. a ConfigMap value that contains YAML). Provide data inline or ref an earlier result.", o(map[string]any{"data": sp("YAML text to parse"), "ref": sp("tool call id whose full result is YAML")})},
		{"diff_text", "Line-by-line diff of two texts (like kubectl diff). Provide inline (a,b) or reference two earlier tool results (refA,refB) — e.g. two get_resource reads before/after a change.", o(map[string]any{"a": sp("first text"), "b": sp("second text"), "refA": sp("tool call id for the first text"), "refB": sp("tool call id for the second text")})},
		{"time_calc", "Time arithmetic: now (current UTC time), diff (b-a as duration), add (a + duration b, negative to subtract). Timestamps RFC3339, durations like 90m/2h30m.", o(map[string]any{"op": sp("now | diff | add"), "a": sp("first timestamp (RFC3339) — for diff/add"), "b": sp("second timestamp (diff) or duration (add)")}, "op")},
	}
}

func isUtilTool(name string) bool {
	switch name {
	case "grep_result", "json_query", "base64", "parse_yaml", "diff_text", "time_calc":
		return true
	}
	return false
}

// execUtilTool führt ein Utility lokal aus. runID bindet refs + Vault an den Run.
func execUtilTool(runID, name string, args map[string]any) (string, error) {
	getStr := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	getInt := func(k string, def int) int {
		if f, ok := args[k].(float64); ok {
			return int(f)
		}
		return def
	}
	deref := func(refKey, dataKey string) (string, error) {
		if ref := getStr(refKey); ref != "" {
			v, ok := cachedResult(runID, ref)
			if !ok {
				return "", fmt.Errorf("no cached result for ref %q (expired or wrong id)", ref)
			}
			return v, nil
		}
		if d := getStr(dataKey); d != "" {
			return d, nil
		}
		return "", fmt.Errorf("provide %q or %q", refKey, dataKey)
	}

	switch name {
	case "grep_result":
		src, ok := cachedResult(runID, getStr("ref"))
		if !ok {
			return "", fmt.Errorf("no cached result for ref %q (expired or wrong id)", getStr("ref"))
		}
		pattern := getStr("pattern")
		if len(pattern) > 512 {
			return "", fmt.Errorf("pattern too long (max 512)")
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regex: %v", err)
		}
		invert, _ := args["invert"].(bool)
		ctxLines := getInt("context", 0)
		if ctxLines < 0 {
			ctxLines = 0
		}
		if ctxLines > 5 {
			ctxLines = 5
		}
		maxM := getInt("maxMatches", 50)
		if maxM <= 0 || maxM > 200 {
			maxM = 200
		}
		lines := strings.Split(src, "\n")
		var out []string
		matches := 0
		for i, l := range lines {
			if re.MatchString(l) != invert {
				matches++
				if matches > maxM {
					out = append(out, fmt.Sprintf("…(stopped after %d matches)", maxM))
					break
				}
				lo := i - ctxLines
				if lo < 0 {
					lo = 0
				}
				hi := i + ctxLines
				if hi >= len(lines) {
					hi = len(lines) - 1
				}
				for j := lo; j <= hi; j++ {
					prefix := "  "
					if j == i {
						prefix = "> "
					}
					out = append(out, fmt.Sprintf("%s%d: %s", prefix, j+1, lines[j]))
				}
				if ctxLines > 0 {
					out = append(out, "--")
				}
			}
		}
		if matches == 0 {
			return fmt.Sprintf("0 matches for %q in %d lines", pattern, len(lines)), nil
		}
		return fmt.Sprintf("%d match(es):\n%s", matches, strings.Join(out, "\n")), nil

	case "json_query":
		src, ok := cachedResult(runID, getStr("ref"))
		if !ok {
			return "", fmt.Errorf("no cached result for ref %q (expired or wrong id)", getStr("ref"))
		}
		if !gjson.Valid(src) {
			return "", fmt.Errorf("cached result is not valid JSON — use grep_result instead")
		}
		res := gjson.Get(src, getStr("path"))
		if !res.Exists() {
			return fmt.Sprintf("path %q matched nothing", getStr("path")), nil
		}
		return res.Raw, nil

	case "base64":
		data := getStr("data")
		hadSecret := vaultHasPlaceholder(data)
		data = vaultResolve(runID, data)
		switch getStr("op") {
		case "encode":
			enc := base64.StdEncoding.EncodeToString([]byte(data))
			if hadSecret {
				// Ergebnis eines Secret-Inputs ist selbst geheim → re-maskieren.
				return vaultPut(runID, enc), nil
			}
			return enc, nil
		case "decode":
			dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
			if err != nil {
				return "", fmt.Errorf("invalid base64: %v", err)
			}
			if hadSecret {
				return vaultPut(runID, string(dec)), nil
			}
			return string(dec), nil
		}
		return "", fmt.Errorf(`op must be "encode" or "decode"`)

	case "parse_yaml":
		src, err := deref("ref", "data")
		if err != nil {
			return "", err
		}
		if len(src) > 1<<20 {
			return "", fmt.Errorf("yaml too large (max 1 MiB)")
		}
		j, err := yaml.YAMLToJSON([]byte(src))
		if err != nil {
			return "", fmt.Errorf("yaml parse: %v", err)
		}
		return string(j), nil

	case "diff_text":
		a, err := deref("refA", "a")
		if err != nil {
			return "", err
		}
		b, err := deref("refB", "b")
		if err != nil {
			return "", err
		}
		return diffLines(a, b), nil

	case "time_calc":
		switch getStr("op") {
		case "now":
			return time.Now().UTC().Format(time.RFC3339), nil
		case "diff":
			ta, err := time.Parse(time.RFC3339, getStr("a"))
			if err != nil {
				return "", fmt.Errorf("a: %v", err)
			}
			tb, err := time.Parse(time.RFC3339, getStr("b"))
			if err != nil {
				return "", fmt.Errorf("b: %v", err)
			}
			return tb.Sub(ta).String(), nil
		case "add":
			ta, err := time.Parse(time.RFC3339, getStr("a"))
			if err != nil {
				return "", fmt.Errorf("a: %v", err)
			}
			d, err := time.ParseDuration(getStr("b"))
			if err != nil {
				return "", fmt.Errorf("b: %v", err)
			}
			return ta.Add(d).UTC().Format(time.RFC3339), nil
		}
		return "", fmt.Errorf(`op must be "now", "diff" or "add"`)
	}
	return "", fmt.Errorf("unknown utility %q", name)
}

// diffLines ist ein einfacher LCS-freier Zeilen-Diff (unified-artig): gemeinsame
// Präfix-/Suffix-Zeilen werden gefaltet, der Rest als -/+ gezeigt. Für
// Config-Vergleiche (get_resource vorher/nachher) mehr als ausreichend.
func diffLines(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	// gemeinsamen Präfix/Suffix abschneiden
	p := 0
	for p < len(al) && p < len(bl) && al[p] == bl[p] {
		p++
	}
	s := 0
	for s < len(al)-p && s < len(bl)-p && al[len(al)-1-s] == bl[len(bl)-1-s] {
		s++
	}
	da := al[p : len(al)-s]
	db := bl[p : len(bl)-s]
	if len(da) == 0 && len(db) == 0 {
		return "no differences"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "@@ line %d (−%d +%d) @@\n", p+1, len(da), len(db))
	const maxShow = 200
	for i, l := range da {
		if i >= maxShow {
			fmt.Fprintf(&out, "…(%d more removed lines)\n", len(da)-maxShow)
			break
		}
		out.WriteString("- " + l + "\n")
	}
	for i, l := range db {
		if i >= maxShow {
			fmt.Fprintf(&out, "…(%d more added lines)\n", len(db)-maxShow)
			break
		}
		out.WriteString("+ " + l + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// marshalJSON kompakt (Helfer für Tool-Antworten).
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
