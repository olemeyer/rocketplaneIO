package api

// copilot_master.go — der Master-Agent des Copilot-Orchestrators. Er spricht
// mit dem Nutzer AUSSCHLIESSLICH über strukturierte Tools (respond), plant die
// Investigation und delegiert jede Hypothese als Task-JSON an einen
// Investigator-Subagenten (copilot_investigator.go). NUR die Verdicts fließen
// zurück in seinen Kontext — die rohen Tool-Ergebnisse sieht das UI im
// Inspector, nie der Master. Jede Hypothese/Action/Frage/Fazit ist ein Knoten
// im Investigation-Graph (Postgres), Branching über parent_node_id.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

const (
	masterMaxIters           = 40
	maxParallelInvestigators = 3
)

const masterSystem = `You are the rocketplaneIO Copilot MASTER — the incident commander for ONE Kubernetes cluster. You do not run raw investigations yourself: you form falsifiable hypotheses and delegate each one to an investigator sub-agent (spawn_investigator). Investigators do the tool work and return a structured VERDICT. You reason over verdicts, decide the next step, and talk to the human — precisely.

# The loop you run
1. Understand the request. Call set_title early. For a broad "what is wrong?" you may call get_service_map ONCE yourself for orientation — everything deeper goes to investigators.
2. Formulate hypotheses. Each spawn_investigator carries: a falsifiable hypothesis (one sentence), an objective (what evidence settles it), and context (namespace, workload, timeframe, prior findings the investigator needs — investigators know NOTHING about this conversation). Spawn several investigators IN ONE TURN when hypotheses are independent — they run in parallel.
3. After each verdict decide explicitly: (a) go DEEPER on this path (spawn with parent_node_id = that node), (b) BRANCH back to an earlier node (set parent_node_id to it) when a path is exhausted, (c) propose ONE action (propose_action) when evidence supports a fix, (d) ask the human (ask_user) when only they know (image tags, secrets, intent), or (e) conclude (conclude) with the root cause and evidence node ids.
4. After an approved action finishes: spawn a VERIFICATION investigator to re-read the affected signals (never trust the action status alone), then conclude or continue.

# Talking to the human — respond
respond{message, status} is your ONLY channel to the human. Rules for message:
- Lead with the finding, not the process. Cite concrete evidence (pod names, counts, error strings) from verdicts.
- SHORT: a few sentences or a compact list. No narration ("I will now check…"), no restating the question, no apologies. The investigation progress is visible in the UI graph — do not describe it.
- status:"working" = you continue this turn chain; status:"done" = the turn is complete (final answer, or you are waiting for nothing).
- reasoning (optional) is your internal note shown only in the dev view — keep message clean.
- Answer in the user's language.

# Discipline
- One hypothesis = one investigator. Never spawn a vague "look around" task.
- Trust verdicts, but confirm a root cause with TWO independent verdicts (or one verdict + service-map signal) before proposing a fix.
- Propose ONE action at a time; include expected_outcome (a re-checkable success criterion) and evidence_node_ids.
- If an investigator returns inconclusive, refine the hypothesis or branch — do not repeat the same task.
- If the human dismisses an action, do not re-propose it; offer an alternative or conclude with the recommendation.
- Use ask_user for anything only the human knows (correct image tag, credentials, whether traffic loss is acceptable). Use kind:"secret" for credentials — you will receive a masked placeholder; pass it into action params as-is.
- propose_script (Starlark) is the ABSOLUTE last resort when no catalog action fits — always explain in rollback_note what the script mutates and how it rolls back.
- NEVER invent data. Everything you tell the human must come from a verdict, an action result, or the human.`

// masterTools — das Tool-Set des Masters (kein roher Read-Zugriff außer
// get_service_map; Utility-Tools für Verdict-Nacharbeit inklusive).
func masterTools() []copilotTool {
	sp := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	ip := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	bp := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	np := func(d string) map[string]any { return map[string]any{"type": "number", "description": d} }
	ap := func(d string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": d}
	}
	o := func(props map[string]any, req ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			m["required"] = req
		}
		return m
	}

	actionProps := actionProposalProps(sp)
	actionProps["evidence_node_ids"] = ap("node ids of the verdicts that justify this action")
	actionProps["expected_outcome"] = sp("re-checkable success criterion, e.g. 'restarts stop climbing within 60s'")

	tools := []copilotTool{
		{"respond", "Deliver a message to the human. This is your ONLY way to speak — keep it short, evidence-based, no narration. status:'working' while you continue, status:'done' when this turn is complete.", o(map[string]any{
			"message":   sp("markdown for the human — lead with the finding, cite concrete evidence"),
			"status":    sp("working | done"),
			"reasoning": sp("optional internal note (dev view only) — why you do what you do next"),
		}, "message", "status")},
		{"spawn_investigator", "Delegate ONE falsifiable hypothesis to an investigator sub-agent. It runs read tools (logs incl. regex search, traces, metrics/PromQL, service map, infra, resource YAML, events, utilities) in its own context and returns a structured verdict (confirmed/refuted/inconclusive + evidence + confidence). Spawn several IN ONE TURN for independent hypotheses — they run in parallel. Give it ALL the context it needs; it knows nothing about this conversation.", o(map[string]any{
			"hypothesis":      sp("one falsifiable sentence, e.g. 'clickhouse-1 crashes because cluster.xml is malformed'"),
			"objective":       sp("what evidence would settle this hypothesis"),
			"context":         sp("everything the investigator needs: namespace, workload, timeframe, prior findings, exact names"),
			"parent_node_id":  sp("node id to attach to: omit to continue the current path; set an EARLIER node id to branch back"),
			"suggested_tools": ap("optional tool names to start with"),
		}, "hypothesis", "objective", "context")},
		{"propose_action", "PROPOSE one safe, reversible, verified action from the fixed catalog. It is NOT executed — it pauses for human approval, then runs and returns its real result. This is the ONLY way to change the cluster; there is no YAML/kubectl/helm. " + actionCatalogDesc, o(actionProps, "kind", "targetKind", "targetName")},
		{"propose_script", "LAST RESORT: author a Starlark action script for a remedy no catalog action covers. It is validated, then proposed to the human as a DESTRUCTIVE action (source shown, arm-to-fire) — never executed silently. The script runs hermetically with k8s.* builtins and automatic LIFO undo. Use only when the catalog genuinely cannot express the fix.", o(map[string]any{
			"title":          sp("short name for this script, e.g. 'reset-stuck-finalizer'"),
			"source":         sp("the Starlark source. Available: step(name), report(detail), fail(msg), sleep(s), args dict, k8s.get/pods/events, k8s.scale/rollout_restart/set_image/rollout_undo/pause/resume/delete_pod/cordon/uncordon/hpa_set/cronjob_trigger/taint/untaint, wait_rollout(ns,kind,name,timeout?), wait_ready(...). GENERIC (kubectl-equivalent, auto-snapshot + undo): k8s.raw_get(api_version,kind,namespace,name), k8s.raw_list(api_version,kind,namespace,selector=''), k8s.raw_apply(manifest_dict), k8s.raw_patch(api_version,kind,namespace,name,patch_dict), k8s.raw_delete(...) — RBAC/webhook/CRD/Node/Namespace mutations are denied, max 20 mutations per script. All mutations auto-undo on failure/cancel."),
			"timeoutSeconds": ip("script timeout 30..1800 (default 600)"),
			"rollback_note":  sp("REQUIRED: what the script mutates and how it rolls back — shown to the human"),
		}, "title", "source", "rollback_note")},
		{"ask_user", "Ask the human ONE structured question when only they can answer (image tag, credential, acceptable blast radius). kind: choice (radio), multi (checkboxes), text (free text), secret (masked input — the value is stored in a server-side vault and you receive a {{secret:…}} placeholder to pass into action params). The loop pauses until they answer.", o(map[string]any{
			"question":      sp("the question, one line"),
			"kind":          sp("choice | multi | text | secret"),
			"options":       ap("options for choice/multi"),
			"allowFreeText": bp("allow a free-text answer in addition to options"),
			"secretHint":    sp("for kind=secret: what the value is, e.g. 'GHCR pull token'"),
		}, "question", "kind")},
		{"conclude", "Record the final conclusion of this investigation: root cause, the verdict nodes that prove it, and the recommendation. Call once, then respond with status done.", o(map[string]any{
			"root_cause":        sp("the root cause, one or two sentences"),
			"evidence_node_ids": ap("node ids of the verdicts that prove it"),
			"recommendation":    sp("the recommended fix or next step"),
			"confidence":        np("0..1 how certain you are"),
		}, "root_cause", "recommendation")},
	}
	// Geteilte Tools aus dem Basis-Katalog übernehmen (Schema-Wiederverwendung).
	for _, t := range copilotTools() {
		switch t.Name {
		case "set_title", "wait", "wait_for_action", "get_service_map":
			tools = append(tools, t)
		}
	}
	return append(tools, copilotUtilTools()...)
}

// investigationContextBlock serialisiert vorhandene Verdicts kompakt für den
// Master-Kontext beim Resume (Browser-Reload, Folgefrage im selben Chat).
func investigationContextBlock(nodes []model.InvestigationNode) string {
	var b strings.Builder
	b.WriteString("[investigation state — verdicts so far]\n")
	for _, n := range nodes {
		parent := "-"
		if n.ParentID != nil {
			parent = shortID(n.ParentID.String())
		}
		fmt.Fprintf(&b, "node %s (parent %s, %s, %s): %s", shortID(n.ID.String()), parent, n.Kind, n.Status, n.Hypothesis)
		if len(n.Verdict) > 0 {
			v := string(n.Verdict)
			if len(v) > 600 {
				v = v[:600] + "…"
			}
			b.WriteString(" → " + v)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// masterGraph kapselt die (optionale) Graph-Persistenz — fällt die DB aus,
// läuft die Investigation mit In-Memory-Knoten weiter.
type masterGraph struct {
	s     *Server
	invID uuid.UUID
	ok    bool
	seq   int
	mu    sync.Mutex
}

func (g *masterGraph) createNode(ctx context.Context, parentID *uuid.UUID, kind, hypothesis string, task json.RawMessage) *model.InvestigationNode {
	if g.ok {
		n, err := g.s.store.CreateNode(ctx, g.invID, parentID, kind, hypothesis, task)
		if err == nil {
			return n
		}
	}
	// Fallback: flüchtiger Knoten (UI bekommt ihn trotzdem via SSE).
	g.mu.Lock()
	g.seq++
	seq := g.seq
	g.mu.Unlock()
	return &model.InvestigationNode{ID: uuid.New(), ParentID: parentID, Seq: seq, Kind: kind, Hypothesis: hypothesis, Task: task, Status: "running"}
}

func (g *masterGraph) finishNode(ctx context.Context, nodeID uuid.UUID, status string, verdict json.RawMessage, confidence *float32, usage llmUsage) {
	if g.ok {
		_ = g.s.store.FinishNode(ctx, nodeID, status, verdict, confidence, usage.In, usage.Out)
	}
}

// runMasterLoop ersetzt die alten Single-Agent-Loops vollständig.
func (s *Server) runMasterLoop(ctx context.Context, runID string, userID uuid.UUID, org, cluster, cookie string, req copilotReq, emit emitFn) {
	cfg := req.Config
	system := masterSystem + scopeSystemNote(req.Scope)
	hist := newLLMHistory(cfg.API, system)
	for _, m := range req.Messages {
		hist.AppendText(m.Role, m.Text)
	}

	// Graph-Persistenz aufsetzen (best effort — nie den Chat blockieren).
	graph := &masterGraph{s: s}
	clusterID, cErr := uuid.Parse(cluster)
	chatID, chErr := uuid.Parse(req.ChatID)
	if cErr == nil && chErr == nil && userID != uuid.Nil {
		if err := s.store.EnsureCopilotChatRow(ctx, chatID, clusterID, userID); err == nil {
			if invID, err := s.store.EnsureInvestigation(ctx, chatID, clusterID); err == nil {
				graph.invID, graph.ok = invID, true
				// Resume: vorhandene Verdicts kompakt in den Kontext geben.
				if nodes, err := s.store.ListNodes(ctx, invID); err == nil && len(nodes) > 0 {
					hist.AppendText("user", investigationContextBlock(nodes))
					graph.seq = len(nodes)
				}
			}
		}
	}

	tools := masterTools()
	var lastNodeID *uuid.UUID // Default-Parent für spawn_investigator
	textEmitted := false      // ob in diesem Turn schon message-Text gestreamt wurde
	anyText := false          // ob im RUN schon Text floss — Absatz zwischen respond-Messages

	// emitText streamt Nutzertext; sep=true fügt vor der ERSTEN Zeile einer
	// neuen respond-Message einen Absatz ein (sonst kleben Turns aneinander).
	emitText := func(t string, sep bool) {
		if t == "" {
			return
		}
		if sep && anyText {
			emit("text", map[string]any{"text": "\n\n"})
		}
		anyText = true
		textEmitted = true
		emit("text", map[string]any{"text": t})
	}

	for iter := 0; iter < masterMaxIters; iter++ {
		streamers := map[int]*jsonFieldStreamer{}
		textEmitted = false
		turn, err := runLLMTurn(ctx, cfg, system, hist, tools, "required", func(d llmDelta) {
			switch d.Kind {
			case "text":
				// Fallback-Kanal (Provider ignoriert required): direkt streamen.
				emitText(d.Text, false)
			case "tool_start":
				if d.Name == "respond" {
					first := true
					streamers[d.Index] = newJSONFieldStreamer("message", func(t string) {
						emitText(t, first)
						first = false
					})
				}
			case "tool_args":
				if st := streamers[d.Index]; st != nil {
					st.Feed(d.Text)
				}
			case "tool_done":
				if st := streamers[d.Index]; st != nil {
					st.Done()
				}
			}
		})
		if err != nil {
			emit("error", map[string]any{"error": err.Error()})
			return
		}
		hist.AppendAssistantTurn(turn)

		if len(turn.ToolCalls) == 0 {
			if turn.StopReason == "length" {
				continue // mid-thought abgeschnitten — weiterlaufen lassen
			}
			// Provider hat trotz required frei geantwortet: Text wurde bereits
			// gestreamt — als Antwort akzeptieren und enden.
			break
		}

		results := map[string]string{}
		wantStop := false

		// spawn_investigator-Calls des Turns parallel ausführen (Semaphore).
		type spawnJob struct {
			call llmToolCall
			node *model.InvestigationNode
		}
		var spawns []spawnJob

		for _, c := range turn.ToolCalls {
			args := c.Args()
			switch c.Name {
			case "respond":
				msg := strOf(args["message"])
				if !textEmitted && msg != "" {
					// Streaming griff nicht (z.B. Provider ohne Args-Deltas) —
					// message komplett emittieren.
					emitText(msg, true)
				}
				if rsn := strOf(args["reasoning"]); rsn != "" {
					emit("reasoning", map[string]any{"text": rsn})
				}
				results[c.ID] = "Delivered."
				if strOf(args["status"]) == "done" {
					wantStop = true
				}

			case "spawn_investigator":
				hypothesis := strOf(args["hypothesis"])
				parentID := lastNodeID
				if p := strOf(args["parent_node_id"]); p != "" {
					if pid, err := uuid.Parse(p); err == nil {
						parentID = &pid
					}
				}
				task, _ := json.Marshal(args)
				node := graph.createNode(ctx, parentID, "hypothesis", hypothesis, task)
				emit("node_started", map[string]any{
					"nodeId": node.ID.String(), "parentId": idStr(node.ParentID), "seq": node.Seq,
					"kind": node.Kind, "hypothesis": hypothesis, "task": json.RawMessage(task),
				})
				spawns = append(spawns, spawnJob{call: c, node: node})
				nid := node.ID
				lastNodeID = &nid

			case "propose_action":
				actionInput := map[string]any{
					"kind": args["kind"], "targetNamespace": args["targetNamespace"],
					"targetKind": args["targetKind"], "targetName": args["targetName"],
					"params": args["params"],
				}
				ab := actionBlock(actionInput).Action
				ab["id"] = c.ID
				ab["runId"] = runID
				if eo := strOf(args["expected_outcome"]); eo != "" {
					ab["reason"] = eo
				}
				task, _ := json.Marshal(args)
				node := graph.createNode(ctx, lastNodeID, "action", fmt.Sprintf("%s %s/%s", strOf(args["kind"]), strOf(args["targetKind"]), strOf(args["targetName"])), task)
				nid := node.ID
				lastNodeID = &nid
				ab["nodeId"] = node.ID.String()
				emit("node_started", map[string]any{"nodeId": node.ID.String(), "parentId": idStr(node.ParentID), "seq": node.Seq, "kind": "action", "hypothesis": node.Hypothesis, "task": json.RawMessage(task)})
				emit("action", ab) // Loop hält an bis zur Freigabe (Stream bleibt offen).
				// Safe Actions v2 grouping: every action from THIS turn lands in one
				// group (a trace), back-linked to its investigation node. The create
				// handler reads these to open/append the per-turn ActionGroup.
				actionInput["_grpChat"] = chatID.String()
				actionInput["_grpTurn"] = runID
				actionInput["_grpNode"] = node.ID.String()
				if graph.ok {
					actionInput["_grpInv"] = graph.invID.String()
				}
				content := s.awaitAndRunAction(ctx, runID, c.ID, req.Scope, org, cluster, cookie, actionInput, emit)
				verdict, _ := json.Marshal(map[string]any{"result": content})
				status := "done"
				if strings.Contains(content, "DECLINED") || strings.Contains(content, "NOT run") {
					status = "abandoned"
				}
				graph.finishNode(ctx, node.ID, status, verdict, nil, llmUsage{})
				emit("verdict", map[string]any{"nodeId": node.ID.String(), "verdict": json.RawMessage(verdict)})
				results[c.ID] = content

			case "propose_script":
				results[c.ID] = s.handleProposeScript(ctx, runID, c.ID, org, cluster, cookie, req.Scope, args, graph, &lastNodeID, emit)

			case "ask_user":
				results[c.ID] = s.handleAskUser(ctx, runID, c.ID, args, graph, &lastNodeID, emit)

			case "conclude":
				task, _ := json.Marshal(args)
				node := graph.createNode(ctx, lastNodeID, "conclusion", strOf(args["root_cause"]), task)
				var conf *float32
				if f, ok := args["confidence"].(float64); ok {
					c32 := float32(f)
					conf = &c32
				}
				graph.finishNode(ctx, node.ID, "done", task, conf, llmUsage{})
				if graph.ok {
					_ = s.store.ConcludeInvestigation(ctx, graph.invID, "concluded")
				}
				emit("node_started", map[string]any{"nodeId": node.ID.String(), "parentId": idStr(node.ParentID), "seq": node.Seq, "kind": "conclusion", "hypothesis": node.Hypothesis, "task": json.RawMessage(task)})
				emit("verdict", map[string]any{"nodeId": node.ID.String(), "verdict": json.RawMessage(task)})
				results[c.ID] = "Conclusion recorded."

			case "set_title":
				emit("title", args)
				results[c.ID] = "Title updated."

			default:
				if isUtilTool(c.Name) {
					emit("tool_call", map[string]any{"id": c.ID, "name": c.Name, "args": args, "klass": "util"})
					res, err := execUtilTool(runID, c.Name, args)
					ok := err == nil
					if err != nil {
						res = "error: " + err.Error()
					}
					cacheToolResult(runID, c.ID, res)
					emit("tool_result", map[string]any{"id": c.ID, "name": c.Name, "ok": ok, "result": res})
					results[c.ID] = clampForLLM(res)
					break
				}
				// wait / wait_for_action / get_service_map
				emit("tool_call", map[string]any{"id": c.ID, "name": c.Name, "args": args, "klass": "read"})
				res, err := s.execCopilotTool(ctx, req.Scope, org, cluster, cookie, c.Name, args)
				ok := err == nil
				if err != nil {
					res = "error: " + err.Error()
				}
				cacheToolResult(runID, c.ID, res)
				emit("tool_result", map[string]any{"id": c.ID, "name": c.Name, "ok": ok, "result": res})
				results[c.ID] = clampForLLM(res)
			}
		}

		// Investigatoren parallel laufen lassen (max N gleichzeitig).
		if len(spawns) > 0 {
			sem := make(chan struct{}, maxParallelInvestigators)
			var wg sync.WaitGroup
			var mu sync.Mutex
			for _, job := range spawns {
				wg.Add(1)
				go func(job spawnJob) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					verdict, usage := s.runInvestigator(ctx, runID, job.node, req, org, cluster, cookie, emit)
					status := "done"
					var conf *float32
					var v struct {
						Verdict    string  `json:"verdict"`
						Confidence float32 `json:"confidence"`
					}
					if json.Unmarshal(verdict, &v) == nil {
						if v.Verdict == "" {
							status = "failed"
						}
						if v.Confidence > 0 {
							conf = &v.Confidence
						}
					}
					graph.finishNode(ctx, job.node.ID, status, verdict, conf, usage)
					emit("verdict", map[string]any{
						"nodeId": job.node.ID.String(), "verdict": json.RawMessage(verdict),
						"tokens": map[string]int{"in": usage.In, "out": usage.Out},
					})
					mu.Lock()
					results[job.call.ID] = fmt.Sprintf("verdict (node %s): %s", shortID(job.node.ID.String()), string(verdict))
					mu.Unlock()
				}(job)
			}
			wg.Wait()
		}

		// Ergebnisse in Call-Reihenfolge zurückgeben (Anthropic verlangt eine
		// Antwort für JEDEN tool_use).
		ordered := make([]llmToolResult, 0, len(turn.ToolCalls))
		for _, c := range turn.ToolCalls {
			content, ok := results[c.ID]
			if !ok {
				content = "(no result)"
			}
			ordered = append(ordered, llmToolResult{CallID: c.ID, Content: content})
		}
		hist.AppendToolResults(ordered)

		if wantStop {
			break
		}
	}
	emit("done", map[string]any{})
}

// handleAskUser pausiert auf die menschliche Antwort und maskiert Secrets.
func (s *Server) handleAskUser(ctx context.Context, runID, callID string, args map[string]any, graph *masterGraph, lastNodeID **uuid.UUID, emit emitFn) string {
	kind := orDefStr(strOf(args["kind"]), "text")
	task, _ := json.Marshal(args)
	node := graph.createNode(ctx, *lastNodeID, "question", strOf(args["question"]), task)
	nid := node.ID
	*lastNodeID = &nid
	emit("node_started", map[string]any{"nodeId": node.ID.String(), "parentId": idStr(node.ParentID), "seq": node.Seq, "kind": "question", "hypothesis": node.Hypothesis, "task": json.RawMessage(task)})
	emit("ask_user", map[string]any{
		"id": callID, "runId": runID, "nodeId": node.ID.String(),
		"question": args["question"], "kind": kind, "options": args["options"],
		"allowFreeText": args["allowFreeText"], "secretHint": args["secretHint"],
	})

	key := gateKey(runID, callID)
	ch := copilotRegister(key)
	defer copilotUnregister(key)
	msg, decided := s.waitDecision(ctx, ch, emit)
	if !decided {
		graph.finishNode(ctx, node.ID, "abandoned", nil, nil, llmUsage{})
		emit("action_status", map[string]any{"id": callID, "status": "cancelled", "terminal": true})
		return "No answer was received (cancelled or timed out)."
	}
	if !msg.Answered {
		graph.finishNode(ctx, node.ID, "abandoned", nil, nil, llmUsage{})
		emit("action_status", map[string]any{"id": callID, "status": "rejected", "terminal": true})
		return "The human dismissed the question without answering."
	}

	// Antwort parsen; bei kind=secret wandert der Klartext in den Vault und das
	// LLM (wie die Persistenz) sieht nur den Platzhalter.
	var ans struct {
		Value  string   `json:"value"`
		Values []string `json:"values"`
	}
	_ = json.Unmarshal(msg.Answer, &ans)
	verdictPayload := map[string]any{"answered": true}
	var result string
	if kind == "secret" {
		if ans.Value == "" {
			result = "The human submitted an empty value."
		} else {
			ph := vaultPut(runID, ans.Value)
			verdictPayload["placeholder"] = ph
			result = "The human provided the secret. Use this placeholder verbatim where the value is needed: " + ph
		}
	} else {
		if len(ans.Values) > 0 {
			verdictPayload["values"] = ans.Values
			result = "Answer: " + strings.Join(ans.Values, ", ")
		} else {
			verdictPayload["value"] = ans.Value
			result = "Answer: " + ans.Value
		}
	}
	v, _ := json.Marshal(verdictPayload)
	graph.finishNode(ctx, node.ID, "done", v, nil, llmUsage{})
	emit("verdict", map[string]any{"nodeId": node.ID.String(), "verdict": json.RawMessage(v)})
	emit("action_status", map[string]any{"id": callID, "status": "succeeded", "terminal": true})
	return result
}

// handleProposeScript validiert Self-Authoring-Scripts und schleust sie als
// destructive Action durch das normale Approval-Gate.
func (s *Server) handleProposeScript(ctx context.Context, runID, callID, org, cluster, cookie, scope string, args map[string]any, graph *masterGraph, lastNodeID **uuid.UUID, emit emitFn) string {
	source := strOf(args["source"])
	title := orDefStr(strOf(args["title"]), "adhoc-script")
	if source == "" {
		return "propose_script requires source."
	}
	if len(source) > 64*1024 {
		return "Script too large (max 64 KiB)."
	}
	// Save-Zeit-Verifikation wie im Workflow-Editor: Fehler gehen zurück ans
	// Modell, das die Source korrigieren kann — nichts Kaputtes erreicht den Menschen.
	if err := checkScriptSource(title, source); err != nil {
		return "Script validation failed — fix and retry: " + err.Error()
	}

	scriptArgs := map[string]string{}
	if raw, ok := args["args"].(map[string]any); ok {
		for k, v := range raw {
			scriptArgs[k] = strOf(v)
		}
	}
	timeout := 600
	if f, ok := args["timeoutSeconds"].(float64); ok && f > 0 {
		timeout = int(f)
	}

	actionInput := map[string]any{
		"kind": "script", "targetNamespace": "-", "targetKind": "Script", "targetName": title,
		"source": source, "title": title, "timeoutSeconds": timeout, "scriptArgs": scriptArgs,
		"params": map[string]any{"source": source, "args": scriptArgs, "timeoutSeconds": timeout},
	}
	task, _ := json.Marshal(args)
	node := graph.createNode(ctx, *lastNodeID, "action", "script: "+title, task)
	nid := node.ID
	*lastNodeID = &nid
	emit("node_started", map[string]any{"nodeId": node.ID.String(), "parentId": idStr(node.ParentID), "seq": node.Seq, "kind": "action", "hypothesis": node.Hypothesis, "task": json.RawMessage(task)})

	ab := actionBlock(actionInput).Action
	ab["id"] = callID
	ab["runId"] = runID
	ab["nodeId"] = node.ID.String()
	ab["level"] = "destructive" // Self-Authoring ist IMMER arm-to-fire
	ab["klass"] = "destructive"
	ab["reason"] = strOf(args["rollback_note"])
	ab["source"] = source
	emit("action", ab)
	content := s.awaitAndRunAction(ctx, runID, callID, scope, org, cluster, cookie, actionInput, emit)
	verdict, _ := json.Marshal(map[string]any{"result": content})
	graph.finishNode(ctx, node.ID, "done", verdict, nil, llmUsage{})
	emit("verdict", map[string]any{"nodeId": node.ID.String(), "verdict": json.RawMessage(verdict)})
	return content
}

func idStr(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
