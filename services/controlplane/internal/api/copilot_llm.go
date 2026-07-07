package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const copilotMaxIters = 30 // eine echte Investigation (map→bundle→logs→traces→metrics→propose→verify) braucht viele Runden
const copilotMaxNudges = 2  // wie oft wir einen tool-losen „Next-Step"-Stillstand anschieben

// looksLikeStall erkennt den #1-Fehler: das Modell KÜNDIGT einen Tool-Aufruf an,
// statt ihn zu machen ("Next step: check…", "I'll now…", "Let me look…").
func looksLikeStall(text string) bool {
	t := strings.ToLower(text)
	for _, p := range []string{"next step", "i'll now", "i will now", "let me check", "let me look", "let me pull", "let's check", "let's look", "let us check", "should i ", "want me to", "i'll investigate", "i'll dig", "next, i", "i'll examine", "nächster schritt", "ich prüfe gleich", "ich schaue mir", "soll ich"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

const stallNudge = "You described a next step but did not take it. Call the tool now and keep investigating — do not hand control back until you have an evidence-backed root cause (and, if warranted, one proposed fix), or you have exhausted the read tools."

func convergeNudge(left int) string {
	return fmt.Sprintf("You have about %d tool-call rounds left. Converge now: state your best current diagnosis with the evidence you have, and either propose ONE run_safe_action or name exactly what is still unknown. Do not open a new open-ended thread.", left)
}

// emitFn schreibt EIN SSE-Event ins Copilot-UI (text-delta, tool, action, done, error).
type emitFn func(event string, data any)

func llmStream(ctx context.Context, url string, headers map[string]string, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return copilotClient.Do(req)
}

// sseData zieht aus dem Provider-Stream die reinen `data:`-JSON-Payloads.
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

/* ── Anthropic Messages (streaming) — deckt Anthropic UND GLM/Z.ai ab ─────── */

type antBlk struct{ typ, text, name, id, inputJSON string }

func (s *Server) streamAnthropic(ctx context.Context, runID, org, cluster, cookie string, req copilotReq, emit emitFn) {
	tools := make([]map[string]any, 0)
	for _, t := range copilotTools() {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Desc, "input_schema": t.Schema})
	}
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, map[string]any{"role": m.Role, "content": m.Text})
	}
	url := strings.TrimRight(req.Config.BaseURL, "/") + "/v1/messages"
	headers := map[string]string{"x-api-key": req.Config.APIKey, "anthropic-version": "2023-06-01"}

	nudges := 0
	converged := false
	for iter := 0; iter < copilotMaxIters; iter++ {
		if left := copilotMaxIters - iter; left <= 3 && !converged {
			converged = true
			messages = append(messages, map[string]any{"role": "user", "content": convergeNudge(left)})
		}
		resp, err := llmStream(ctx, url, headers, map[string]any{
			"model": req.Config.Model, "max_tokens": 8192, "system": copilotSystem + scopeSystemNote(req.Scope),
			"messages": messages, "tools": tools, "stream": true,
		})
		if err != nil {
			emit("error", map[string]any{"error": err.Error()})
			return
		}
		if resp.StatusCode >= 300 {
			emit("error", map[string]any{"error": readLLMError(resp)})
			return
		}

		blocks := map[int]*antBlk{}
		var order []int
		stopReason := ""
		_ = sseData(resp, func(raw []byte) bool {
			var e map[string]any
			if json.Unmarshal(raw, &e) != nil {
				return false
			}
			switch e["type"] {
			case "content_block_start":
				idx := intOf(e["index"])
				cb, _ := e["content_block"].(map[string]any)
				b := &antBlk{typ: strOf(cb["type"]), name: strOf(cb["name"]), id: strOf(cb["id"])}
				blocks[idx] = b
				order = append(order, idx)
			case "content_block_delta":
				idx := intOf(e["index"])
				d, _ := e["delta"].(map[string]any)
				b := blocks[idx]
				if b == nil {
					return false
				}
				if t := strOf(d["text"]); t != "" {
					b.text += t
					emit("text", map[string]any{"text": t}) // Live-Token → UI
				}
				if pj := strOf(d["partial_json"]); pj != "" {
					b.inputJSON += pj
				}
			case "message_delta":
				if d, ok := e["delta"].(map[string]any); ok {
					if sr := strOf(d["stop_reason"]); sr != "" {
						stopReason = sr
					}
				}
			}
			return false
		})
		resp.Body.Close()

		// Assistant-Turn (text + tool_use) für den Folge-Call rekonstruieren.
		content := []any{}
		toolUses := []*antBlk{}
		assistantText := ""
		for _, idx := range order {
			b := blocks[idx]
			if b == nil {
				continue
			}
			if b.typ == "tool_use" {
				var input map[string]any
				_ = json.Unmarshal([]byte(orDefStr(b.inputJSON, "{}")), &input)
				content = append(content, map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": input})
				toolUses = append(toolUses, b)
			} else if b.text != "" {
				content = append(content, map[string]any{"type": "text", "text": b.text})
				assistantText += b.text
			}
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": content})

		if stopReason != "tool_use" || len(toolUses) == 0 {
			// Mid-thought abgeschnitten? Weiterlaufen lassen, statt hart zu enden.
			if stopReason == "max_tokens" {
				continue
			}
			// Tool-loser „Next-Step"-Stillstand → einmalig anschieben, sonst enden.
			if nudges < copilotMaxNudges && looksLikeStall(assistantText) {
				nudges++
				messages = append(messages, map[string]any{"role": "user", "content": stallNudge})
				continue
			}
			break
		}
		results := []map[string]any{}
		for _, b := range toolUses {
			var input map[string]any
			_ = json.Unmarshal([]byte(orDefStr(b.inputJSON, "{}")), &input)
			if b.name == "run_safe_action" {
				ab := actionBlock(input).Action
				ab["id"] = b.id
				ab["runId"] = runID
				emit("action", ab) // Loop HÄLT AN bis zur Freigabe (Stream bleibt offen).
				content := s.awaitAndRunAction(ctx, runID, b.id, req.Scope, org, cluster, cookie, input, emit)
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": b.id, "content": content})
				continue
			}
			if b.name == "set_title" {
				emit("title", input)
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": b.id, "content": "Title updated."})
				continue
			}
			emit("tool_call", map[string]any{"id": b.id, "name": b.name, "args": input, "klass": "read"})
			res, err := s.execCopilotTool(ctx, req.Scope, org, cluster, cookie, b.name, input)
			ok := err == nil
			if err != nil {
				res = "error: " + err.Error()
			}
			emit("tool_result", map[string]any{"id": b.id, "name": b.name, "ok": ok, "result": res})
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": b.id, "content": clampForLLM(res)})
		}
		messages = append(messages, map[string]any{"role": "user", "content": results})
	}
	emit("done", map[string]any{})
}

/* ── OpenAI Chat Completions (streaming) — OpenAI/OpenRouter/Ollama/custom ── */

func (s *Server) streamOpenAI(ctx context.Context, runID, org, cluster, cookie string, req copilotReq, emit emitFn) {
	tools := make([]map[string]any, 0)
	for _, t := range copilotTools() {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Desc, "parameters": t.Schema}})
	}
	messages := []map[string]any{{"role": "system", "content": copilotSystem + scopeSystemNote(req.Scope)}}
	for _, m := range req.Messages {
		messages = append(messages, map[string]any{"role": m.Role, "content": m.Text})
	}
	url := strings.TrimRight(req.Config.BaseURL, "/") + "/chat/completions"
	headers := map[string]string{"Authorization": "Bearer " + req.Config.APIKey}

	nudges := 0
	converged := false
	for iter := 0; iter < copilotMaxIters; iter++ {
		if left := copilotMaxIters - iter; left <= 3 && !converged {
			converged = true
			messages = append(messages, map[string]any{"role": "user", "content": convergeNudge(left)})
		}
		resp, err := llmStream(ctx, url, headers, map[string]any{
			"model": req.Config.Model, "messages": messages, "tools": tools, "tool_choice": "auto", "stream": true, "max_tokens": 8192,
		})
		if err != nil {
			emit("error", map[string]any{"error": err.Error()})
			return
		}
		if resp.StatusCode >= 300 {
			emit("error", map[string]any{"error": readLLMError(resp)})
			return
		}

		var textBuf strings.Builder
		finishReason := ""
		type tc struct{ id, name, args string }
		calls := map[int]*tc{}
		var callOrder []int
		_ = sseData(resp, func(raw []byte) bool {
			var e map[string]any
			if json.Unmarshal(raw, &e) != nil {
				return false
			}
			choices, _ := e["choices"].([]any)
			if len(choices) == 0 {
				return false
			}
			c0, _ := choices[0].(map[string]any)
			if fr := strOf(c0["finish_reason"]); fr != "" {
				finishReason = fr
			}
			d, _ := c0["delta"].(map[string]any)
			if t := strOf(d["content"]); t != "" {
				textBuf.WriteString(t)
				emit("text", map[string]any{"text": t})
			}
			if tcs, ok := d["tool_calls"].([]any); ok {
				for _, tcAny := range tcs {
					m, _ := tcAny.(map[string]any)
					idx := intOf(m["index"])
					if calls[idx] == nil {
						calls[idx] = &tc{}
						callOrder = append(callOrder, idx)
					}
					if id := strOf(m["id"]); id != "" {
						calls[idx].id = id
					}
					if fn, ok := m["function"].(map[string]any); ok {
						if n := strOf(fn["name"]); n != "" {
							calls[idx].name = n
						}
						calls[idx].args += strOf(fn["arguments"])
					}
				}
			}
			return false
		})
		resp.Body.Close()

		// Assistant-Turn anhängen.
		asst := map[string]any{"role": "assistant", "content": textBuf.String()}
		if len(callOrder) > 0 {
			tcArr := []any{}
			for _, idx := range callOrder {
				c := calls[idx]
				tcArr = append(tcArr, map[string]any{"id": c.id, "type": "function", "function": map[string]any{"name": c.name, "arguments": orDefStr(c.args, "{}")}})
			}
			asst["tool_calls"] = tcArr
		}
		messages = append(messages, asst)

		if len(callOrder) == 0 {
			// Mid-thought abgeschnitten (length) → weiterlaufen lassen.
			if finishReason == "length" {
				continue
			}
			// Tool-loser „Next-Step"-Stillstand → einmalig anschieben, sonst enden.
			if nudges < copilotMaxNudges && looksLikeStall(textBuf.String()) {
				nudges++
				messages = append(messages, map[string]any{"role": "user", "content": stallNudge})
				continue
			}
			break
		}
		for _, idx := range callOrder {
			c := calls[idx]
			var input map[string]any
			_ = json.Unmarshal([]byte(orDefStr(c.args, "{}")), &input)
			var content string
			if c.name == "run_safe_action" {
				ab := actionBlock(input).Action
				ab["id"] = c.id
				ab["runId"] = runID
				emit("action", ab) // Loop HÄLT AN bis zur Freigabe (Stream bleibt offen).
				content = s.awaitAndRunAction(ctx, runID, c.id, req.Scope, org, cluster, cookie, input, emit)
			} else if c.name == "set_title" {
				emit("title", input)
				content = "Title updated."
			} else {
				emit("tool_call", map[string]any{"id": c.id, "name": c.name, "args": input, "klass": "read"})
				res, err := s.execCopilotTool(ctx, req.Scope, org, cluster, cookie, c.name, input)
				ok := err == nil
				if err != nil {
					res = "error: " + err.Error()
				}
				emit("tool_result", map[string]any{"id": c.id, "name": c.name, "ok": ok, "result": res})
				content = clampForLLM(res)
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": c.id, "content": content})
		}
	}
	emit("done", map[string]any{})
}

/* ── kleine Helfer ───────────────────────────────────────────────────────── */

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
