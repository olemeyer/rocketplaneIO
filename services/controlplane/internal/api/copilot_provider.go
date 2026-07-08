package api

// copilot_provider.go — EINE Turn-Abwicklung für beide LLM-Wire-Formate
// (Anthropic /v1/messages und OpenAI /chat/completions). Der Orchestrator
// (Master/Investigator) spricht nur noch runLLMTurn + llmHistory und ist damit
// providerneutral; Streaming-Deltas gehen als llmDelta-Callbacks nach oben
// (Text sofort ins UI, Tool-Args in den jsonFieldStreamer).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// llmHTTPClient hat bewusst KEINEN Client-Timeout: lange LLM-Turns (große
// Kontexte, langsame Provider) würden sonst hart abreißen. Die Lebensdauer
// steuert der Request-Context (Stream-Abbruch = ctx cancel).
var llmHTTPClient = &http.Client{}

// llmToolCall ist ein vollständiger Tool-Aufruf eines Assistant-Turns.
type llmToolCall struct {
	ID       string
	Name     string
	ArgsJSON string // rohes JSON (leer = "{}")
}

// Args parst die Argumente (nil-sicher, leeres Objekt bei Fehlern).
func (c llmToolCall) Args() map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(orDefStr(c.ArgsJSON, "{}")), &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

type llmUsage struct{ In, Out int }

// llmTurn ist das normalisierte Ergebnis EINES Provider-Roundtrips.
type llmTurn struct {
	Text       string // freie Text-Blöcke (Fallback-Kanal; Orchestrator erzwingt Tools)
	ToolCalls  []llmToolCall
	StopReason string // "tool_use" | "end" | "length" | roher Provider-Wert
	Usage      llmUsage
}

// llmDelta ist ein Streaming-Ereignis während des Turns.
type llmDelta struct {
	Kind  string // "text" | "tool_start" | "tool_args" | "tool_done"
	Index int    // Tool-Call-Index (Reihenfolge im Turn)
	ID    string // Tool-Call-ID (sobald bekannt)
	Name  string // Tool-Name (sobald bekannt)
	Text  string // Delta-Payload (text bzw. partial JSON der Args)
}

type deltaFn func(llmDelta)

// llmToolResult ist die Antwort auf einen Tool-Call fürs Folge-Turn-Feeding.
type llmToolResult struct {
	CallID  string
	Content string
}

/* ── History-Builder (je Wire-Dialekt) ───────────────────────────────────── */

// llmHistory hält die Message-Historie im jeweiligen Provider-Format. Der
// Orchestrator kennt nur Append-Operationen — kein Dialekt-Wissen außerhalb
// dieser Datei.
type llmHistory struct {
	api      string // "anthropic" | "openai"
	messages []map[string]any
}

func newLLMHistory(api, system string) *llmHistory {
	h := &llmHistory{api: api}
	if api == "openai" && system != "" {
		h.messages = append(h.messages, map[string]any{"role": "system", "content": system})
	}
	return h
}

// AppendText hängt eine reine Text-Message an (user/assistant).
func (h *llmHistory) AppendText(role, text string) {
	h.messages = append(h.messages, map[string]any{"role": role, "content": text})
}

// AppendAssistantTurn rekonstruiert den Assistant-Turn (Text + Tool-Calls)
// im Dialekt des Providers, damit der Folge-Call konsistent ist.
func (h *llmHistory) AppendAssistantTurn(t llmTurn) {
	if h.api == "openai" {
		msg := map[string]any{"role": "assistant", "content": t.Text}
		if len(t.ToolCalls) > 0 {
			arr := make([]any, 0, len(t.ToolCalls))
			for _, c := range t.ToolCalls {
				arr = append(arr, map[string]any{"id": c.ID, "type": "function",
					"function": map[string]any{"name": c.Name, "arguments": orDefStr(c.ArgsJSON, "{}")}})
			}
			msg["tool_calls"] = arr
		}
		h.messages = append(h.messages, msg)
		return
	}
	content := []any{}
	if t.Text != "" {
		content = append(content, map[string]any{"type": "text", "text": t.Text})
	}
	for _, c := range t.ToolCalls {
		content = append(content, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Name, "input": c.Args()})
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	h.messages = append(h.messages, map[string]any{"role": "assistant", "content": content})
}

// AppendToolResults hängt die Tool-Antworten an (Anthropic: EIN user-Turn mit
// tool_result-Blöcken; OpenAI: je eine role=tool Message).
func (h *llmHistory) AppendToolResults(results []llmToolResult) {
	if len(results) == 0 {
		return
	}
	if h.api == "openai" {
		for _, r := range results {
			h.messages = append(h.messages, map[string]any{"role": "tool", "tool_call_id": r.CallID, "content": r.Content})
		}
		return
	}
	blocks := make([]any, 0, len(results))
	for _, r := range results {
		blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": r.CallID, "content": r.Content})
	}
	h.messages = append(h.messages, map[string]any{"role": "user", "content": blocks})
}

/* ── runLLMTurn ──────────────────────────────────────────────────────────── */

// runLLMTurn führt EINEN Streaming-Roundtrip aus. toolChoice: "" (auto),
// "required" (Modell MUSS ein Tool rufen), "tool:<name>" (genau dieses Tool).
func runLLMTurn(ctx context.Context, cfg copilotConfig, system string, hist *llmHistory,
	tools []copilotTool, toolChoice string, onDelta deltaFn) (llmTurn, error) {
	if onDelta == nil {
		onDelta = func(llmDelta) {}
	}
	if cfg.API == "openai" {
		return runOpenAITurn(ctx, cfg, hist, tools, toolChoice, onDelta)
	}
	return runAnthropicTurn(ctx, cfg, system, hist, tools, toolChoice, onDelta)
}

func llmStreamReq(ctx context.Context, url string, headers map[string]string, body any) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return llmHTTPClient.Do(req)
}

func runAnthropicTurn(ctx context.Context, cfg copilotConfig, system string, hist *llmHistory,
	tools []copilotTool, toolChoice string, onDelta deltaFn) (llmTurn, error) {
	toolDefs := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, map[string]any{"name": t.Name, "description": t.Desc, "input_schema": t.Schema})
	}
	body := map[string]any{
		"model": cfg.Model, "max_tokens": 8192, "system": system,
		"messages": hist.messages, "tools": toolDefs, "stream": true,
	}
	switch {
	case toolChoice == "required":
		body["tool_choice"] = map[string]any{"type": "any"}
	case strings.HasPrefix(toolChoice, "tool:"):
		body["tool_choice"] = map[string]any{"type": "tool", "name": strings.TrimPrefix(toolChoice, "tool:")}
	}
	resp, err := llmStreamReq(ctx, strings.TrimRight(cfg.BaseURL, "/")+"/v1/messages",
		map[string]string{"x-api-key": cfg.APIKey, "anthropic-version": "2023-06-01"}, body)
	if err != nil {
		return llmTurn{}, err
	}
	if resp.StatusCode >= 300 {
		return llmTurn{}, fmt.Errorf("%s", readLLMError(resp))
	}
	defer resp.Body.Close()

	type blk struct {
		typ, text, name, id, inputJSON string
		callIdx                        int
	}
	blocks := map[int]*blk{}
	var order []int
	turn := llmTurn{}
	callCount := 0
	_ = sseData(resp, func(raw []byte) bool {
		var e map[string]any
		if json.Unmarshal(raw, &e) != nil {
			return false
		}
		switch e["type"] {
		case "message_start":
			if m, ok := e["message"].(map[string]any); ok {
				if u, ok := m["usage"].(map[string]any); ok {
					turn.Usage.In = intOf(u["input_tokens"])
				}
			}
		case "content_block_start":
			idx := intOf(e["index"])
			cb, _ := e["content_block"].(map[string]any)
			b := &blk{typ: strOf(cb["type"]), name: strOf(cb["name"]), id: strOf(cb["id"])}
			if b.typ == "tool_use" {
				b.callIdx = callCount
				callCount++
				onDelta(llmDelta{Kind: "tool_start", Index: b.callIdx, ID: b.id, Name: b.name})
			}
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
				onDelta(llmDelta{Kind: "text", Text: t})
			}
			if pj := strOf(d["partial_json"]); pj != "" {
				b.inputJSON += pj
				onDelta(llmDelta{Kind: "tool_args", Index: b.callIdx, ID: b.id, Name: b.name, Text: pj})
			}
		case "content_block_stop":
			idx := intOf(e["index"])
			if b := blocks[idx]; b != nil && b.typ == "tool_use" {
				onDelta(llmDelta{Kind: "tool_done", Index: b.callIdx, ID: b.id, Name: b.name})
			}
		case "message_delta":
			if d, ok := e["delta"].(map[string]any); ok {
				if sr := strOf(d["stop_reason"]); sr != "" {
					turn.StopReason = sr
				}
			}
			if u, ok := e["usage"].(map[string]any); ok {
				turn.Usage.Out = intOf(u["output_tokens"])
			}
		}
		return false
	})

	for _, idx := range order {
		b := blocks[idx]
		if b == nil {
			continue
		}
		if b.typ == "tool_use" {
			turn.ToolCalls = append(turn.ToolCalls, llmToolCall{ID: b.id, Name: b.name, ArgsJSON: b.inputJSON})
		} else {
			turn.Text += b.text
		}
	}
	turn.StopReason = normalizeStop(turn.StopReason, len(turn.ToolCalls))
	if turn.Usage.Out == 0 {
		turn.Usage.Out = estimateTokens(turn)
	}
	return turn, nil
}

func runOpenAITurn(ctx context.Context, cfg copilotConfig, hist *llmHistory,
	tools []copilotTool, toolChoice string, onDelta deltaFn) (llmTurn, error) {
	toolDefs := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, map[string]any{"type": "function",
			"function": map[string]any{"name": t.Name, "description": t.Desc, "parameters": t.Schema}})
	}
	body := map[string]any{
		"model": cfg.Model, "messages": hist.messages, "tools": toolDefs,
		"stream": true, "max_tokens": 8192,
		"stream_options": map[string]any{"include_usage": true},
	}
	switch {
	case toolChoice == "required":
		body["tool_choice"] = "required"
	case strings.HasPrefix(toolChoice, "tool:"):
		body["tool_choice"] = map[string]any{"type": "function",
			"function": map[string]any{"name": strings.TrimPrefix(toolChoice, "tool:")}}
	default:
		body["tool_choice"] = "auto"
	}
	resp, err := llmStreamReq(ctx, strings.TrimRight(cfg.BaseURL, "/")+"/chat/completions",
		map[string]string{"Authorization": "Bearer " + cfg.APIKey}, body)
	if err != nil {
		return llmTurn{}, err
	}
	if resp.StatusCode >= 300 {
		return llmTurn{}, fmt.Errorf("%s", readLLMError(resp))
	}
	defer resp.Body.Close()

	type tc struct{ id, name, args string }
	calls := map[int]*tc{}
	var callOrder []int
	turn := llmTurn{}
	var textBuf strings.Builder
	_ = sseData(resp, func(raw []byte) bool {
		var e map[string]any
		if json.Unmarshal(raw, &e) != nil {
			return false
		}
		if u, ok := e["usage"].(map[string]any); ok {
			turn.Usage.In = intOf(u["prompt_tokens"])
			turn.Usage.Out = intOf(u["completion_tokens"])
		}
		choices, _ := e["choices"].([]any)
		if len(choices) == 0 {
			return false
		}
		c0, _ := choices[0].(map[string]any)
		if fr := strOf(c0["finish_reason"]); fr != "" {
			turn.StopReason = fr
		}
		d, _ := c0["delta"].(map[string]any)
		if t := strOf(d["content"]); t != "" {
			textBuf.WriteString(t)
			onDelta(llmDelta{Kind: "text", Text: t})
		}
		if tcs, ok := d["tool_calls"].([]any); ok {
			for _, tcAny := range tcs {
				m, _ := tcAny.(map[string]any)
				idx := intOf(m["index"])
				if calls[idx] == nil {
					calls[idx] = &tc{}
					callOrder = append(callOrder, idx)
				}
				c := calls[idx]
				if id := strOf(m["id"]); id != "" {
					c.id = id
				}
				if fn, ok := m["function"].(map[string]any); ok {
					if n := strOf(fn["name"]); n != "" && c.name == "" {
						c.name = n
						onDelta(llmDelta{Kind: "tool_start", Index: idx, ID: c.id, Name: c.name})
					}
					if a := strOf(fn["arguments"]); a != "" {
						c.args += a
						onDelta(llmDelta{Kind: "tool_args", Index: idx, ID: c.id, Name: c.name, Text: a})
					}
				}
			}
		}
		return false
	})

	turn.Text = textBuf.String()
	for _, idx := range callOrder {
		c := calls[idx]
		onDelta(llmDelta{Kind: "tool_done", Index: idx, ID: c.id, Name: c.name})
		turn.ToolCalls = append(turn.ToolCalls, llmToolCall{ID: c.id, Name: c.name, ArgsJSON: c.args})
	}
	turn.StopReason = normalizeStop(turn.StopReason, len(turn.ToolCalls))
	if turn.Usage.Out == 0 {
		turn.Usage.Out = estimateTokens(turn)
	}
	return turn, nil
}

// normalizeStop vereinheitlicht die Stop-Gründe beider Dialekte:
// tool_use | end | length.
func normalizeStop(raw string, toolCalls int) string {
	switch raw {
	case "tool_use", "tool_calls":
		return "tool_use"
	case "end_turn", "stop", "":
		if toolCalls > 0 {
			return "tool_use"
		}
		return "end"
	case "max_tokens", "length":
		return "length"
	}
	if toolCalls > 0 {
		return "tool_use"
	}
	return raw
}

// estimateTokens ist der Fallback für Provider ohne Usage-Reporting (~4 Zeichen/Token).
func estimateTokens(t llmTurn) int {
	n := len(t.Text)
	for _, c := range t.ToolCalls {
		n += len(c.ArgsJSON) + len(c.Name)
	}
	return n / 4
}
