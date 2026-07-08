package api

// copilot_llm.go — geteilte LLM-Plumbing-Helfer. Die eigentliche Orchestrierung
// lebt in copilot_master.go (Master) + copilot_investigator.go (Subagenten) auf
// dem Provider-Adapter copilot_provider.go. Die alten Single-Agent-Loops
// (streamAnthropic/streamOpenAI mit Stall-/Converge-Nudges) sind ersetzt: der
// Master spricht strukturiert (respond-Tool) — Gelaber hat keinen Kanal mehr.

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
)

// emitFn schreibt EIN SSE-Event ins Copilot-UI (text, tool_call, tool_result,
// action, action_status, ask_user, node_started, verdict, reasoning, title,
// error, done). Der Handler serialisiert Aufrufe (parallele Investigatoren).
type emitFn func(event string, data any)

// sseData zieht aus einem Provider-Stream die reinen `data:`-JSON-Payloads.
func sseData(resp *http.Response, onData func(raw []byte) bool) error {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if stop := onData([]byte(payload)); stop {
			break
		}
	}
	return sc.Err()
}

// readLLMError extrahiert die Fehlermeldung aus einer non-2xx-Provider-Antwort.
func readLLMError(resp *http.Response) string {
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	var b strings.Builder
	for sc.Scan() {
		b.WriteString(sc.Text())
	}
	resp.Body.Close()
	var m map[string]any
	if json.Unmarshal([]byte(b.String()), &m) == nil {
		if em, ok := m["error"].(map[string]any); ok {
			if msg := strOf(em["message"]); msg != "" {
				return msg
			}
		}
	}
	s := b.String()
	if len(s) > 300 {
		s = s[:300]
	}
	return orDefStr(s, resp.Status)
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}
func intOf(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
func orDefStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
