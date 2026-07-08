package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sseServer spielt vorbereitete SSE-Zeilen ab und fängt den Request-Body.
func sseServer(t *testing.T, lines []string, capture *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			_ = json.NewDecoder(r.Body).Decode(capture)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
	}))
}

func TestRunAnthropicTurnToolUse(t *testing.T) {
	lines := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":120}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"text":"thinking"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tc_1","name":"respond"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"partial_json":"{\"message\":\"Ha"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"partial_json":"llo\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
	}
	var captured map[string]any
	srv := sseServer(t, lines, &captured)
	defer srv.Close()

	cfg := copilotConfig{API: "anthropic", BaseURL: srv.URL, Model: "m", APIKey: "k"}
	hist := newLLMHistory("anthropic", "sys")
	hist.AppendText("user", "hi")

	var deltas []llmDelta
	turn, err := runLLMTurn(context.Background(), cfg, "sys", hist, copilotTools(), "required", func(d llmDelta) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.StopReason != "tool_use" || len(turn.ToolCalls) != 1 {
		t.Fatalf("turn = %+v", turn)
	}
	tc := turn.ToolCalls[0]
	if tc.ID != "tc_1" || tc.Name != "respond" || tc.Args()["message"] != "Hallo" {
		t.Fatalf("tool call = %+v args=%v", tc, tc.Args())
	}
	if turn.Usage.In != 120 || turn.Usage.Out != 42 {
		t.Fatalf("usage = %+v", turn.Usage)
	}
	if turn.Text != "thinking" {
		t.Fatalf("text = %q", turn.Text)
	}
	// tool_choice=required muss als {"type":"any"} ankommen.
	if tcRaw, ok := captured["tool_choice"].(map[string]any); !ok || tcRaw["type"] != "any" {
		t.Fatalf("tool_choice = %v", captured["tool_choice"])
	}
	// Delta-Reihenfolge: tool_start vor tool_args vor tool_done.
	var kinds []string
	for _, d := range deltas {
		if d.Name == "respond" || d.Kind == "text" {
			kinds = append(kinds, d.Kind)
		}
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "tool_start,tool_args") || !strings.Contains(joined, "tool_done") {
		t.Fatalf("delta order: %v", joined)
	}
}

func TestRunAnthropicTurnForcedTool(t *testing.T) {
	var captured map[string]any
	srv := sseServer(t, []string{`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`}, &captured)
	defer srv.Close()
	cfg := copilotConfig{API: "anthropic", BaseURL: srv.URL, Model: "m", APIKey: "k"}
	hist := newLLMHistory("anthropic", "")
	hist.AppendText("user", "x")
	_, err := runLLMTurn(context.Background(), cfg, "", hist, nil, "tool:write_verdict", nil)
	if err != nil {
		t.Fatal(err)
	}
	tc, _ := captured["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "write_verdict" {
		t.Fatalf("tool_choice = %v", captured["tool_choice"])
	}
}

func TestRunOpenAITurnToolCalls(t *testing.T) {
	lines := []string{
		`{"choices":[{"delta":{"content":"hi "}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"spawn_investigator","arguments":"{\"hypo"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"thesis\":\"x\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":80,"completion_tokens":21}}`,
	}
	var captured map[string]any
	srv := sseServer(t, lines, &captured)
	defer srv.Close()

	cfg := copilotConfig{API: "openai", BaseURL: srv.URL, Model: "m", APIKey: "k"}
	hist := newLLMHistory("openai", "sys")
	hist.AppendText("user", "hi")
	turn, err := runLLMTurn(context.Background(), cfg, "sys", hist, copilotTools(), "required", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.StopReason != "tool_use" || len(turn.ToolCalls) != 1 {
		t.Fatalf("turn = %+v", turn)
	}
	c := turn.ToolCalls[0]
	if c.ID != "call_9" || c.Name != "spawn_investigator" || c.Args()["hypothesis"] != "x" {
		t.Fatalf("call = %+v args=%v", c, c.Args())
	}
	if turn.Usage.In != 80 || turn.Usage.Out != 21 {
		t.Fatalf("usage = %+v", turn.Usage)
	}
	if captured["tool_choice"] != "required" {
		t.Fatalf("tool_choice = %v", captured["tool_choice"])
	}
	// System-Prompt muss als erste Message drin sein.
	msgs, _ := captured["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first message = %v", first)
	}
}

func TestRunOpenAITurnLengthStop(t *testing.T) {
	lines := []string{
		`{"choices":[{"delta":{"content":"partial"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"length"}]}`,
	}
	srv := sseServer(t, lines, nil)
	defer srv.Close()
	cfg := copilotConfig{API: "openai", BaseURL: srv.URL, Model: "m", APIKey: "k"}
	hist := newLLMHistory("openai", "")
	hist.AppendText("user", "x")
	turn, err := runLLMTurn(context.Background(), cfg, "", hist, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.StopReason != "length" || turn.Text != "partial" {
		t.Fatalf("turn = %+v", turn)
	}
	if turn.Usage.Out == 0 {
		t.Fatal("fallback usage estimate expected")
	}
}

func TestHistoryBuilderRoundtrip(t *testing.T) {
	turn := llmTurn{Text: "t", ToolCalls: []llmToolCall{{ID: "1", Name: "respond", ArgsJSON: `{"message":"m"}`}}}

	a := newLLMHistory("anthropic", "sys")
	a.AppendText("user", "hi")
	a.AppendAssistantTurn(turn)
	a.AppendToolResults([]llmToolResult{{CallID: "1", Content: "ok"}})
	if len(a.messages) != 3 {
		t.Fatalf("anthropic messages = %d", len(a.messages))
	}
	last := a.messages[2]
	blocks, _ := last["content"].([]any)
	b0, _ := blocks[0].(map[string]any)
	if last["role"] != "user" || b0["type"] != "tool_result" || b0["tool_use_id"] != "1" {
		t.Fatalf("anthropic tool result = %v", last)
	}

	o := newLLMHistory("openai", "sys")
	o.AppendText("user", "hi")
	o.AppendAssistantTurn(turn)
	o.AppendToolResults([]llmToolResult{{CallID: "1", Content: "ok"}})
	if len(o.messages) != 4 { // system + user + assistant + tool
		t.Fatalf("openai messages = %d", len(o.messages))
	}
	if o.messages[3]["role"] != "tool" || o.messages[3]["tool_call_id"] != "1" {
		t.Fatalf("openai tool result = %v", o.messages[3])
	}
}

func TestNormalizeStop(t *testing.T) {
	cases := []struct {
		raw   string
		calls int
		want  string
	}{
		{"tool_use", 1, "tool_use"},
		{"tool_calls", 2, "tool_use"},
		{"end_turn", 0, "end"},
		{"stop", 0, "end"},
		{"", 1, "tool_use"}, // mancher Provider lässt finish_reason weg
		{"max_tokens", 0, "length"},
		{"length", 0, "length"},
	}
	for _, c := range cases {
		if got := normalizeStop(c.raw, c.calls); got != c.want {
			t.Errorf("normalizeStop(%q,%d) = %q want %q", c.raw, c.calls, got, c.want)
		}
	}
}
