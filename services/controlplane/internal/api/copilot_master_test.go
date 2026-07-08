package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

func testNode(id, hypothesis string) *model.InvestigationNode {
	task, _ := json.Marshal(map[string]any{"hypothesis": hypothesis, "objective": "settle it", "context": "test"})
	return &model.InvestigationNode{ID: uuid.New(), Kind: "hypothesis", Hypothesis: hypothesis, Task: task, Status: "running"}
}

// scriptedLLM spielt je nach System-Prompt (Master vs. Investigator) ein
// Anthropic-SSE-Drehbuch ab.
type scriptedLLM struct {
	mu          sync.Mutex
	masterTurn  int
	investTurns int
}

func (sc *scriptedLLM) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System   string           `json:"system"`
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")

		toolUse := func(id, name, args string) {
			fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q}}\n\n", id, name)
			b, _ := json.Marshal(args)
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"partial_json\":%s}}\n\n", b)
			fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		}
		second := func(id, name, args string) {
			fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q}}\n\n", id, name)
			b, _ := json.Marshal(args)
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"partial_json\":%s}}\n\n", b)
			fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		}
		stop := func() {
			fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":10}}\n\n")
		}

		if strings.Contains(body.System, "INVESTIGATOR") {
			sc.mu.Lock()
			sc.investTurns++
			sc.mu.Unlock()
			toolUse("iv_1", "write_verdict", `{"hypothesis":"pods crash on bad config","verdict":"confirmed","summary":"cluster.xml is malformed; pods exit right after config merge.","confidence":0.9,"evidence":[{"source":"query_logs","ref":"c1","quote":"Merging configuration file cluster.xml"}]}`)
			stop()
			return
		}

		sc.mu.Lock()
		sc.masterTurn++
		turn := sc.masterTurn
		sc.mu.Unlock()
		switch turn {
		case 1:
			toolUse("m_1", "respond", `{"message":"Prüfe die ClickHouse-Crashes.","status":"working","reasoning":"one hypothesis first"}`)
			second("m_2", "spawn_investigator", `{"hypothesis":"pods crash on bad config","objective":"find the crash cause","context":"ns modelstudio, sts clickhouse"}`)
			stop()
		default:
			toolUse("m_3", "respond", `{"message":"Root cause: kaputte cluster.xml.","status":"done"}`)
			stop()
		}
	}
}

func TestMasterLoopSpawnVerdictRespond(t *testing.T) {
	sc := &scriptedLLM{}
	srv := httptest.NewServer(sc.handler(t))
	defer srv.Close()

	req := copilotReq{
		Messages: []copilotMsg{{Role: "user", Text: "was ist mit clickhouse los?"}},
		Config:   copilotConfig{API: "anthropic", BaseURL: srv.URL, Model: "m", APIKey: "k"},
	}

	var mu sync.Mutex
	events := []string{}
	texts := strings.Builder{}
	verdicts := 0
	nodesStarted := 0
	emit := func(event string, data any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
		switch event {
		case "text":
			m, _ := data.(map[string]any)
			texts.WriteString(strOf(m["text"]))
		case "verdict":
			verdicts++
		case "node_started":
			nodesStarted++
		}
	}

	s := &Server{}
	s.runMasterLoop(context.Background(), "run-test", uuid.Nil, "org", "not-a-uuid", "", req, emit)

	if sc.masterTurn != 2 || sc.investTurns != 1 {
		t.Fatalf("master turns = %d, investigator turns = %d", sc.masterTurn, sc.investTurns)
	}
	got := texts.String()
	if !strings.Contains(got, "Prüfe die ClickHouse-Crashes.") || !strings.Contains(got, "Root cause: kaputte cluster.xml.") {
		t.Fatalf("streamed text = %q", got)
	}
	// Zwischen zwei respond-Messages MUSS ein Absatz liegen (sonst kleben Turns).
	if !strings.Contains(got, "Crashes.\n\nRoot cause") {
		t.Fatalf("missing paragraph separator between responds: %q", got)
	}
	if nodesStarted != 1 || verdicts != 1 {
		t.Fatalf("nodes=%d verdicts=%d (want 1/1)", nodesStarted, verdicts)
	}
	// reasoning-Event muss ankommen, done zum Schluss.
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "reasoning") || !strings.HasSuffix(joined, "done") {
		t.Fatalf("events = %v", events)
	}
}

func TestInvestigatorForcedVerdictAfterBudget(t *testing.T) {
	// Provider, der NIE write_verdict ruft → nach Budget + Zwangs-Turn muss ein
	// synthetisches inconclusive-Verdict kommen.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		// time_calc ist lokal + instant — der Investigator dreht Runden ohne Verdict.
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c%d\",\"name\":\"time_calc\"}}\n\n", calls)
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"partial_json\":\"{\\\"op\\\":\\\"now\\\"}\"}}\n\n")
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":5}}\n\n")
	}))
	defer srv.Close()

	node := testNode("h1", "does it crash?")
	req := copilotReq{Config: copilotConfig{API: "anthropic", BaseURL: srv.URL, Model: "m", APIKey: "k"}}
	s := &Server{}
	verdict, usage := s.runInvestigator(context.Background(), "run-x", node, req, "org", "cluster", "", func(string, any) {})

	var v struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(verdict, &v); err != nil || v.Verdict != "inconclusive" {
		t.Fatalf("verdict = %s (err %v)", verdict, err)
	}
	if usage.Out == 0 {
		t.Fatal("usage should accumulate")
	}
}
