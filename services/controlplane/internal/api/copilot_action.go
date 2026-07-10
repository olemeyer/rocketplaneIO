package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// copilot_action.go — der HUMAN-IN-THE-LOOP-Gate des Copilots. Wenn das LLM eine
// Safe-Action ausführen will, HÄLT der Loop an (der SSE-Stream bleibt offen) und
// wartet auf die Freigabe des Menschen über einen separaten Decision-Endpoint.
// Bei Approve führt der Loop die Action aus, streamt ihren Status live und speist
// das Ergebnis zurück ins Modell. Bei Reject/Timeout läuft er ohne Ausführung weiter.

// gateMsg trägt die menschliche Entscheidung: Approve/Reject für Actions ODER
// eine strukturierte Antwort (ask_user). Answer läuft NUR über diesen In-Memory-
// Kanal — sie wird nie per SSE geechot und nie serverseitig persistiert.
type gateMsg struct {
	Approve  bool
	Answered bool
	Answer   json.RawMessage
}

// copilotGate hält je (runId/callId) einen Freigabe-Kanal. Der Chat-Handler
// (eine Goroutine) blockiert darauf; der Decision-Handler (andere Goroutine)
// sendet die Entscheidung. Nur der Kanal wird geteilt — der ResponseWriter nie.
var copilotGate = struct {
	mu sync.Mutex
	m  map[string]chan gateMsg
}{m: map[string]chan gateMsg{}}

func gateKey(runID, callID string) string { return runID + "/" + callID }

func copilotRegister(key string) chan gateMsg {
	ch := make(chan gateMsg, 1)
	copilotGate.mu.Lock()
	copilotGate.m[key] = ch
	copilotGate.mu.Unlock()
	return ch
}

func copilotResolve(key string, msg gateMsg) bool {
	copilotGate.mu.Lock()
	ch, ok := copilotGate.m[key]
	copilotGate.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- msg:
	default:
	}
	return true
}

func copilotUnregister(key string) {
	copilotGate.mu.Lock()
	delete(copilotGate.m, key)
	copilotGate.mu.Unlock()
}

// handleCopilotActionDecision — POST …/copilot/action
// {runId, callId, decision: approve|reject|answer, answer?}.
// Gibt die pausierte Action frei, lehnt sie ab, oder beantwortet eine
// ask_user-Frage (answer wird nur in-memory an den wartenden Loop gereicht).
func (s *Server) handleCopilotActionDecision(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.resolveOrg(w, r); !ok {
		return
	}
	var req struct {
		RunID    string          `json:"runId"`
		CallID   string          `json:"callId"`
		Decision string          `json:"decision"` // approve | reject | answer
		Answer   json.RawMessage `json:"answer,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	msg := gateMsg{Approve: req.Decision == "approve"}
	if req.Decision == "answer" {
		if len(req.Answer) == 0 {
			writeErr(w, http.StatusBadRequest, "decision=answer requires an answer payload")
			return
		}
		msg = gateMsg{Answered: true, Answer: req.Answer}
	}
	if !copilotResolve(gateKey(req.RunID, req.CallID), msg) {
		writeErr(w, http.StatusNotFound, "no pending action for this run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// awaitAndRunAction blockiert bis zur menschlichen Entscheidung, führt die Action
// bei Approve aus (create + poll), streamt action_status und liefert den
// Ergebnistext, den der Loop als tool_result zurück ans Modell gibt.
func (s *Server) awaitAndRunAction(ctx context.Context, runID, callID, scope, org, cluster, cookie string, input map[string]any, emit emitFn) string {
	// HARTES Scope-Gate: eine Action darf nie ausserhalb des gewählten Namespaces
	// laufen — noch VOR der Freigabe geprüft, damit gar nicht erst gewartet wird.
	if v := scopeViolation(scope, orDefStr(strOf(input["targetNamespace"]), "-"), strOf(input["targetKind"]), strOf(input["targetName"])); v != "" {
		emit("action_status", map[string]any{"id": callID, "status": "failed", "detail": v, "terminal": true})
		return "This action is " + v + ". Do NOT retry it; either work within the scoped namespace or tell the human to switch the scope."
	}
	key := gateKey(runID, callID)
	ch := copilotRegister(key)
	defer copilotUnregister(key)

	// Auf Entscheidung warten; währenddessen Keepalive-Pings gegen Proxy-Idle-Timeouts.
	msg, decided := s.waitDecision(ctx, ch, emit)
	if !decided {
		emit("action_status", map[string]any{"id": callID, "status": "cancelled", "terminal": true})
		return "No decision was received (cancelled or timed out); the action was NOT run."
	}
	if !msg.Approve {
		emit("action_status", map[string]any{"id": callID, "status": "rejected", "terminal": true})
		return "The human DECLINED to run this action. Do not run it. Acknowledge briefly and, if useful, suggest a safer alternative."
	}

	// Action anlegen (self-HTTP → Auth/Validierung/User-Auflösung wie in der UI).
	// Secret-Platzhalter ({{secret:…}}) werden erst NACH der Freigabe und nur
	// hier serverseitig aufgelöst — das LLM sieht nie Klartext.
	createBody := map[string]any{
		"kind":            strOf(input["kind"]),
		"targetNamespace": orDefStr(strOf(input["targetNamespace"]), "-"),
		"targetKind":      strOf(input["targetKind"]),
		"targetName":      strOf(input["targetName"]),
		"params":          input["params"],
	}
	// Safe Actions v2 grouping: forward the per-turn linkage the master stamped
	// into the input so the create handler opens/appends ONE group per turn and
	// back-links the run to its investigation node.
	if v := strOf(input["_grpChat"]); v != "" {
		createBody["chatId"] = v
		createBody["turnId"] = strOf(input["_grpTurn"])
		createBody["investigationId"] = strOf(input["_grpInv"])
		createBody["investigationNodeId"] = strOf(input["_grpNode"])
	}
	if strOf(input["kind"]) == "script" {
		// Ad-hoc-Script (propose_script): Source + Args gehen als eigene Felder
		// an den Create-Endpoint, der sie validiert und snapshottet.
		createBody["source"] = strOf(input["source"])
		createBody["title"] = strOf(input["title"])
		createBody["timeoutSeconds"] = input["timeoutSeconds"]
		createBody["scriptArgs"] = input["scriptArgs"]
	}
	body, _ := json.Marshal(createBody)
	body = []byte(vaultResolve(runID, string(body)))
	base := fmt.Sprintf("%s/api/orgs/%s/clusters/%s", copilotSelfURL(), org, cluster)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/actions", bytes.NewReader(body))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	resp, err := copilotClient.Do(req)
	if err != nil {
		emit("action_status", map[string]any{"id": callID, "status": "failed", "detail": err.Error(), "terminal": true})
		return "Failed to start the action: " + err.Error()
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		emit("action_status", map[string]any{"id": callID, "status": "failed", "detail": msg, "terminal": true})
		return "The system rejected the action: " + msg
	}
	var created model.Action
	_ = json.Unmarshal(raw, &created)
	emit("action_status", map[string]any{"id": callID, "actionId": created.ID.String(), "status": orDefStr(created.Status, "pending")})

	// Bis zum Terminalzustand pollen (per Store, kein HTTP), Statuswechsel streamen.
	return s.pollAction(ctx, cluster, callID, created.ID, input, emit)
}

func (s *Server) waitDecision(ctx context.Context, ch chan gateMsg, emit emitFn) (gateMsg, bool) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case msg := <-ch:
			return msg, true
		case <-ctx.Done():
			return gateMsg{}, false
		case <-deadline.C:
			return gateMsg{}, false
		case <-ticker.C:
			emit("ping", map[string]any{})
		}
	}
}

func (s *Server) pollAction(ctx context.Context, cluster, callID string, actionID uuid.UUID, input map[string]any, emit emitFn) string {
	clusterID, err := uuid.Parse(cluster)
	if err != nil {
		return "Action started but its status could not be tracked (bad cluster id)."
	}
	target := strOf(input["targetName"])
	lastStatus, lastProgress, lastSteps := "", "", ""
	// Kurzer Inline-Poll: schnelle Actions lösen sofort auf. Dauert es länger,
	// läuft die Action im HINTERGRUND weiter — das UI setzt einen Listener und
	// reaktiviert den Chat automatisch beim Terminalzustand (kein Blockieren).
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "Cancelled while the action was running."
		case <-time.After(700 * time.Millisecond):
		}
		acts, e := s.store.ListActions(ctx, clusterID, "", target, 40)
		if e != nil {
			continue
		}
		var cur *model.Action
		for i := range acts {
			if acts[i].ID == actionID {
				cur = &acts[i]
				break
			}
		}
		if cur == nil {
			continue
		}
		steps := string(cur.Steps)
		if cur.Status != lastStatus || cur.Progress != lastProgress || steps != lastSteps {
			emit("action_status", map[string]any{
				"id": callID, "actionId": actionID.String(), "status": cur.Status,
				"progress": cur.Progress, "steps": json.RawMessage(cur.Steps),
			})
			lastStatus, lastProgress, lastSteps = cur.Status, cur.Progress, steps
		}
		if cur.Status == "succeeded" || cur.Status == "failed" {
			emit("action_status", map[string]any{
				"id": callID, "actionId": actionID.String(), "status": cur.Status,
				"result": cur.Result, "steps": json.RawMessage(cur.Steps), "terminal": true,
			})
			return fmt.Sprintf("Action %q on %s/%s finished with status=%s. Result: %s",
				strOf(input["kind"]), strOf(input["targetKind"]), target, cur.Status, orDefStr(cur.Result, "(none)"))
		}
	}
	// Hintergrund: nicht terminal markieren — das UI beobachtet weiter und liefert
	// das Ergebnis als automatischen Folge-Turn nach.
	emit("action_status", map[string]any{"id": callID, "actionId": actionID.String(), "status": lastStatus, "progress": lastProgress, "backgrounded": true})
	return fmt.Sprintf("Action %q on %s/%s is now RUNNING IN THE BACKGROUND (id %s). Do not claim it finished — briefly say it is running and that you will report the outcome, then stop. The system will re-activate this chat automatically when it completes.",
		strOf(input["kind"]), strOf(input["targetKind"]), target, actionID.String())
}
