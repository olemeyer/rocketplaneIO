package api

// copilot_investigator.go — der Investigator-Subagent: prüft GENAU EINE
// Hypothese in einem eigenen Mini-Kontext (Task-JSON + max. 5 Tool-Runden) und
// terminiert zwingend mit write_verdict. Seine rohen Tool-Ergebnisse gehen per
// SSE (mit nodeId) in den Inspector und in den Result-Cache — in den Master-
// Kontext fließt NUR das Verdict.

import (
	"context"
	"encoding/json"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

const investigatorMaxIters = 5

const investigatorSystem = `You are a rocketplaneIO INVESTIGATOR sub-agent inside ONE Kubernetes cluster. You receive exactly ONE task: a falsifiable hypothesis plus context. Your entire job is to settle THAT hypothesis with the fewest decisive tool calls, then call write_verdict. Nothing else exists for you — no conversation, no other questions, no fixes (you cannot mutate anything).

Rules:
- Plan the ONE most decisive read first (logs with a tight window + regex, the exact debug_bundle, a specific PromQL). Fire independent reads in parallel in the same turn.
- You have a hard budget of ` + "5" + ` tool rounds. Do not browse — every call must be able to change the verdict.
- Big results are truncated for you; use grep_result/json_query with the call id to search the FULL cached payload instead of re-fetching.
- Set "expect" on every read: goal + expectation, one line.
- Evidence means concrete: pod names, exact error strings, counts, timestamps. Quote them in the verdict.
- confirmed = evidence proves the hypothesis. refuted = evidence proves it wrong (say what you saw instead — that is a lead). inconclusive = say exactly which signal is missing and how to get it (e.g. "error only in file log, needs exec cat /var/log/...").
- write_verdict is MANDATORY as your final call — never end without it.`

// investigatorTools — Read-Tools + Utilities + write_verdict. Keine Mutationen,
// kein set_title, kein wait_for_action (Actions sind Master-Sache).
func investigatorTools() []copilotTool {
	sp := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	np := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	o := func(props map[string]any, req ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			m["required"] = req
		}
		return m
	}
	tools := []copilotTool{}
	for _, t := range copilotTools() {
		switch t.Name {
		case "run_safe_action", "set_title", "wait_for_action":
			continue
		}
		tools = append(tools, t)
	}
	tools = append(tools, copilotUtilTools()...)
	tools = append(tools, copilotTool{
		Name: "write_verdict",
		Desc: "Your MANDATORY final call: the structured answer to your hypothesis. After this you are done.",
		Schema: o(map[string]any{
			"hypothesis": sp("the hypothesis you were given (restated)"),
			"verdict":    sp("confirmed | refuted | inconclusive"),
			"summary":    sp("2-4 sentences: what the evidence shows. For refuted: what you saw instead. For inconclusive: which signal is missing."),
			"evidence": map[string]any{"type": "array", "description": "the concrete evidence", "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"source": sp("tool name, e.g. query_logs"),
					"ref":    sp("tool call id (lets the master grep the full payload)"),
					"quote":  sp("the concrete line/number/name that matters"),
				}}},
			"confidence":     np("0..1"),
			"recommendation": sp("optional: the next step or fix candidate this verdict suggests"),
		}, "hypothesis", "verdict", "summary", "confidence"),
	})
	return tools
}

// runInvestigator führt den Mini-Loop eines Subagenten aus und liefert das
// Verdict-JSON + Token-Usage. Fehler enden IMMER in einem (synthetischen)
// inconclusive-Verdict — der Master bekommt nie einen leeren Knoten.
func (s *Server) runInvestigator(ctx context.Context, runID string, node *model.InvestigationNode, req copilotReq, org, cluster, cookie string, emit emitFn) (json.RawMessage, llmUsage) {
	cfg := req.Config
	if cfg.SubModel != "" {
		cfg.Model = cfg.SubModel
	}
	system := investigatorSystem + scopeSystemNote(req.Scope)
	hist := newLLMHistory(cfg.API, system)
	hist.AppendText("user", "Task JSON:\n"+string(node.Task))

	tools := investigatorTools()
	nodeID := node.ID.String()
	var usage llmUsage

	synthetic := func(reason string) json.RawMessage {
		v, _ := json.Marshal(map[string]any{
			"hypothesis": node.Hypothesis, "verdict": "inconclusive",
			"summary": "Investigator did not produce a verdict: " + reason, "confidence": 0,
		})
		return v
	}

	for iter := 0; iter <= investigatorMaxIters; iter++ {
		toolChoice := "required"
		if iter == investigatorMaxIters {
			// Budget erschöpft: das Verdict wird ERZWUNGEN.
			toolChoice = "tool:write_verdict"
			hist.AppendText("user", "Your tool budget is exhausted. Call write_verdict NOW with your best-supported verdict.")
		}
		turn, err := runLLMTurn(ctx, cfg, system, hist, tools, toolChoice, nil)
		if err != nil {
			return synthetic("llm error: " + err.Error()), usage
		}
		usage.In += turn.Usage.In
		usage.Out += turn.Usage.Out
		hist.AppendAssistantTurn(turn)

		if len(turn.ToolCalls) == 0 {
			if turn.StopReason == "length" {
				continue
			}
			hist.AppendText("user", "You must answer through tools; call write_verdict when settled.")
			continue
		}

		results := make([]llmToolResult, 0, len(turn.ToolCalls))
		for _, c := range turn.ToolCalls {
			args := c.Args()
			if c.Name == "write_verdict" {
				v, _ := json.Marshal(args)
				return v, usage
			}
			var res string
			var err error
			if isUtilTool(c.Name) {
				emit("tool_call", map[string]any{"id": c.ID, "name": c.Name, "args": args, "klass": "util", "nodeId": nodeID})
				res, err = execUtilTool(runID, c.Name, args)
			} else {
				emit("tool_call", map[string]any{"id": c.ID, "name": c.Name, "args": args, "klass": "read", "nodeId": nodeID})
				res, err = s.execCopilotTool(ctx, req.Scope, org, cluster, cookie, c.Name, args)
			}
			ok := err == nil
			if err != nil {
				res = "error: " + err.Error()
			}
			cacheToolResult(runID, c.ID, res)
			emit("tool_result", map[string]any{"id": c.ID, "name": c.Name, "ok": ok, "result": res, "nodeId": nodeID})
			results = append(results, llmToolResult{CallID: c.ID, Content: clampForLLM(res)})
		}
		hist.AppendToolResults(results)
	}
	return synthetic("tool budget exhausted without a verdict"), usage
}
