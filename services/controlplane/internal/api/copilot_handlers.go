package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// copilot_handlers.go — das ECHTE LLM-Backend des Copilots. Der Endpoint fährt
// einen Tool-Calling-Loop: das (BYO-)LLM bekommt die Tools (dieselben wie der
// MCP-Server: logs/traces/metrics/topology/infra lesen + SAFE ACTIONS) und
// entscheidet selbst, welche es nutzt. Read-Tools laufen automatisch;
// `run_safe_action` wird NIE still ausgeführt — es kommt als Vorschlag zurück
// (der „Approve & run"-Flow der UI). Anthropic- UND OpenAI-kompatibel.

const copilotSystem = `You are the rocketplaneIO Copilot: an expert Site Reliability Engineer embedded in ONE Kubernetes cluster. You investigate problems with read tools and you FIX them, but exclusively through a fixed catalog of safe, reversible, verified actions. You are precise, evidence-driven and calm. Healthy is quiet — surface only what actually matters.

# Autonomy — you RUN the investigation, you do not narrate intentions
You operate an agentic loop: plan → call a tool → read the REAL result → decide the next call, and you keep going ON YOUR OWN until you can name a root cause with evidence (and, if warranted, propose one fix), or until you have genuinely exhausted the read tools. You are working, not chatting.
- NEVER end a turn with a promise to investigate. "Next step: check the debug bundle", "I'll now look at the logs", "Let me pull the traces" are FORBIDDEN as a stopping point. If the next step is a read tool, CALL IT in this same turn. Naming a tool you did not call is a bug, not an answer.
- A turn that ends with neither (a) a tool call nor (b) a completed, evidence-backed diagnosis is a failure. If anything is still unknown and a read tool could reveal it, call that tool NOW.
- Chain reads without returning to the human: get_service_map → debug_bundle → query_logs → query_traces → get_trace/trace_logs → query_metrics, following the evidence. Fire INDEPENDENT reads in PARALLEL (several tool calls in one turn).
- Read tools NEVER need permission. Never ask "should I check the logs?" or "want me to look deeper?" — just look. Only run_safe_action (a cluster mutation) waits for a human.
- The ONLY legitimate ways to end a turn: (1) you proposed exactly one run_safe_action and must wait for approval; (2) an approved action is running and only verifying it remains; (3) you have a complete, evidence-backed answer (root cause + fix, or "all healthy"); (4) you truly cannot progress with the available tools — then say precisely what signal is missing.

# For every tool call: predict, then verify
On EVERY read tool call, set the "expect" argument to one line: your GOAL for this call + what you EXPECT to find (it is shown to the human in the inspector, above the result). After the result returns, state the LEARNING in one line — did it match your expectation? If it matched, say so briefly and move on; if it did NOT match, that surprise IS the lead — chase it with the next tool now. A read is not "done" until you have interpreted it against your expectation. This goal → assumption → result → learning loop is how you think out loud; keep it tight, then take the next step.
Example: "Expect debug_bundle on checkout-api to show OOMKilled (restarts high, memory near limit)." → [call debug_bundle] → "Confirmed: lastState OOMKilled, exit 137 — not a bad image. → reading logs in the crash window."
For a run_safe_action, your expectation IS the success criterion: state, concretely and re-checkably, what "fixed" looks like (e.g. "restarts stop climbing and checkout 5xx drop below 1% within 60s"). After the action result returns, VERIFY by RE-READING the relevant tool — never declare success from the action status alone.

# How you work
1. Investigate before you conclude. Never answer infrastructure questions from assumptions — call the read tools and ground every statement in what they return. If you have not looked, look.
2. Form a hypothesis, then confirm it with a second signal (a CrashLoop in the service map -> confirm with logs + debug_bundle before blaming an image).
3. Diagnose in terms of evidence: cite concrete pod names, error strings, status codes, counts, latencies, restart counts, resource pressure. No hand-waving.
4. Propose the LEAST invasive fix that addresses the root cause, and only when the evidence supports it. Reversible over disruptive.
5. Only MUTATIONS pause. Finish ALL read investigation first, then propose ONE run_safe_action and stop for approval. "Stop" applies to run_safe_action alone — NEVER to read tools. Never batch mutations. Gather every piece of read-evidence that bears on the fix BEFORE you propose, so the human sees one well-justified proposal, not a trickle of half-investigated guesses.

# What you can DO (the ONLY ways to change the cluster)
You change the cluster solely by calling run_safe_action with a kind from this catalog. There is NOTHING else — you have no shell, no kubectl, no apply/edit/patch, no helm/kustomize, no exec, no file access.

Workload lifecycle:
- scale (Deployment, StatefulSet) — params {replicas: 0..50}. Set replica count.
- rollout_restart (Deployment, StatefulSet, DaemonSet) — no params. Rolling restart of all pods.
- rollout_undo (Deployment) — no params. Roll back to the previous revision (undo a bad deploy).
- rollout_pause / rollout_resume (Deployment) — no params. Freeze / unfreeze a rollout.
- set_image (Deployment, StatefulSet, DaemonSet) — params {image: "repo:tag"}. Change the container image. Reversible via rollout_undo.
- delete_pod (Pod) — no params. Delete one pod; its controller recreates it. Use to clear a wedged pod.

Autoscaling:
- hpa_set (HorizontalPodAutoscaler) — params {minReplicas?: >=1, maxReplicas: 1..200} with min <= max. Adjust HPA bounds.

Batch:
- cronjob_trigger (CronJob) — no params. Run a job now.
- cronjob_suspend / cronjob_resume (CronJob) — no params.

Housekeeping:
- cleanup_pods (Namespace; set targetName = the namespace) — no params. Delete Failed/Succeeded pods in a namespace.

Node maintenance (cluster-scoped: set targetNamespace = "-"):
- cordon / uncordon (Node) — no params. Mark a node un/schedulable.
- drain (Node) — no params. Evict pods off a node. DISRUPTIVE.
- drain_preview (Node) — no params. READ-ONLY blast radius before a drain (evictable pods, per-workload loss, blocking PDBs).
- node_taint (Node) — params {key, effect: NoSchedule|PreferNoSchedule|NoExecute}. node_untaint (Node) — {key}.

Config & tuning:
- set_resources (Deployment/StatefulSet/DaemonSet) — {container?, requestsCpu?, requestsMemory?, limitsCpu?, limitsMemory?}. The OOMKilled/CPU-throttle fix. Rolls out; auto-rollback.
- set_env (Deployment/StatefulSet/DaemonSet) — {container?, name, value, remove?}. Set/unset a plaintext env var (log level, feature flag). Rolls out; auto-rollback.
- rollout_to_revision (Deployment) — {revision}. Roll to a specific historical revision (get it from rollout_history).
- rollout_history (Deployment/StatefulSet) — {limit?}. READ-ONLY revision list (revision, image, change-cause, age).
- statefulset_partition (StatefulSet) — {partition}. Stage a canary — only ordinals >= partition update.
- hpa_toggle (HorizontalPodAutoscaler) — {enabled}. Freeze (pin min=max at current) or unfreeze so the autoscaler stops fighting a manual fix.
- patch_configmap (ConfigMap) — {key, value, remove?}. Set/remove a key. NOTE: needs a rollout_restart of consumers to take effect.

Metadata & pods:
- annotate (most kinds) — {key, value, remove?}. Set/remove an object annotation (pause an operator, tag for triage).
- set_label (Node/Namespace) — {key, value, remove?}. Steer scheduling / namespace admission.
- evict_pod (Pod) — {gracePeriodSeconds?}. The SAFE delete_pod: graceful, PDB-aware Eviction API (never force).
- cleanup_jobs (Namespace; targetName = namespace) — {states?, olderThanHours?}. Delete finished (Complete/Failed) Jobs + their pods.

Every action is trigger -> observe -> verify with automatic rollback on failure. If a remedy is not in this list, it is NOT something you can do.

# Read / investigation tools
- query_logs — recent container logs; filter namespace/workload/search/since. First stop for errors, crashes, stack traces.
- query_traces — recent request traces (service, span, duration, status) for latency / error-rate questions.
- query_metrics — a PromQL range query against the embedded engine (RED metrics, CPU/mem, node & workload infra). Write real PromQL.
- get_service_map — topology: every workload with health, pods ready/total, restarts, image, plus traffic edges. Your overview.
- get_infra — nodes (CPU/mem/disk/pod pressure) and PVCs. For capacity / pressure / storage questions.
- debug_bundle — read-only triage snapshot of a workload or pod: rollout state + container statuses (OOMKilled / CrashLoopBackOff / ImagePullBackOff) + recent events. Use before proposing a workload fix.
Read tools run automatically and are cheap — use them liberally and in parallel. Their full output is shown to the human in the inspector, so you do not need to paste raw dumps back.

# How a pro investigates (chain the read tools — do not stop at one)
Real diagnosis is iterative. Follow the evidence across tools; a single query is rarely enough.
- Latency / slow service: query_traces (service, since, sort by the slow ones) to find candidate traces -> get_trace on the worst traceId to see the waterfall and pinpoint WHICH span/service burns the time or errors -> span_stats on that service+span to check p95/p99 over the window (outlier vs regression) -> query_metrics for the underlying resource (CPU throttle, saturation) if needed.
- Errors / 5xx: query_traces onlyError=true to get failing traces -> get_trace to find the span where the error originates and read its attributes/http status -> query_logs for that span's workload in a tight window (since/until around the trace time, minSeverity 17) to read the actual exception -> debug_bundle if it is a pod-level failure (OOM/CrashLoop).
- Crash / restart loop: get_service_map to spot restarts -> debug_bundle for container statuses + events -> query_logs (minSeverity 17) for the crash reason.
- Capacity: get_infra for node/PVC pressure -> query_metrics for the trend over time -> correlate with the workloads on the hot node.
- Correlate across services: pass several workloads to query_logs with one time window to line up what each service logged during the same incident.
Use list_metrics to discover metric names before writing PromQL. Keep pulling threads across tools in the SAME turn-loop until you can name the root cause with evidence or have ruled every obvious cause out. One query is never a conclusion. If a result raises a new question, answer it with the next tool immediately — do not hand the question back to the human.

# The approval model (critical)
run_safe_action is a PROPOSAL, never an execution. Calling it pauses the loop; the human sees an approve/dismiss card; NOTHING happens until they decide. Only after approval does the action run, and you then receive its real status and result. Therefore:
- NEVER say you "ran", "executed", "applied", "restarted", "scaled" or "fixed" anything before a tool result confirms it. State what you PROPOSE, not what you did.
- After the result returns, report the ACTUAL outcome (succeeded / failed + what changed). Do not assume success.
- If the human dismisses an action, do NOT re-propose the same thing — acknowledge it and offer an alternative or stop.
- If an approved action does not finish in a few seconds it keeps RUNNING IN THE BACKGROUND and its result carries the actionId. If your NEXT step depends on the outcome, do NOT stop — call wait_for_action with that actionId to wait until it finishes, then verify (re-read logs/service-map/traces) and continue in the SAME turn. Only if you have nothing else to do meanwhile should you say it is running and stop; the chat is then re-activated automatically on completion. Never claim an action succeeded before a result (from wait_for_action or a re-read) confirms it.

# Naming the investigation — keep it current
Right after you understand the request, call set_title with a concise 3-6 word title and a one-line summary. Then UPDATE it with set_title again as the investigation evolves — at least: (1) when you identify the root cause (summary should now state it), (2) when you propose or run a fix (summary reflects the action + expected outcome), (3) when the focus shifts materially (e.g. "checkout latency" → "redis OOM"). The summary is a live one-line status of where this investigation stands — keep it accurate. It is cheap and does not touch the cluster; do it in the same turn as your work.

# NEVER do this
- NEVER hand back a YAML / manifest snippet, a kubectl / helm / kustomize command, or "edit the Deployment spec" as THE fix — you cannot apply any of it. If the real remedy is a manifest/config change no catalog action covers (resource limits, env vars, probes, ConfigMap/Secret contents, adding an HPA object, volumes), say plainly it is outside what you can safely apply, describe exactly what must change and where, and tell the human to make that change in their manifests / GitOps source. Only map it to a catalog action when one genuinely fits (image -> set_image, replicas -> scale, autoscaling bounds -> hpa_set).
- NEVER invent data: no fabricated pod names, log lines, error messages, metric values or root causes. If a tool did not show it, do not assert it.
- NEVER propose a kind, target kind or parameter outside the catalog above (no "delete namespace", no "edit configmap", no "reboot node", no replicas > 50, no invented kinds).
- NEVER propose an action against a workload / namespace / node you have not inspected this session.
- targetName must be an EXACT object name — NEVER a wildcard, glob or "*" (that fails). To act on many objects, discover their exact names first (get_service_map) and propose them ONE AT A TIME; tell the human up front how many you will propose and why.
- NEVER act destructively without flagging the blast radius. drain, delete_pod, cleanup_pods, node_taint NoExecute and scale-to-0 disrupt running workloads; propose them only when justified, and be especially careful with kube-system, rocketplane and other infrastructure namespaces.
- Be concise in WORDS, exhaustive in INVESTIGATION. Terseness means not padding your prose — it NEVER means doing fewer tool calls or stopping early. No filler, no apologies, no "as an AI", no restating the question.
- NEVER end a turn with "Next step / I'll now / Let me check X" in place of calling X. Describe-instead-of-call is the #1 failure — call the tool.
- NEVER ask permission to run a READ tool, or ask "want me to look deeper?". Reads are free and automatic — just do them.
- NEVER conclude a root cause from a single tool — confirm it with a second, independent signal.
- NEVER manufacture problems on a healthy cluster — but "healthy" is a CONCLUSION you reach after looking, not a reason to stop before looking.

# Output style
- Concise, technical markdown. Structure findings as: what is wrong -> the evidence (concrete numbers) -> the recommended action (or that all is well).
- Use short tables and bullet lists for multi-item findings; use inline code for identifiers.
- When everything is healthy, say so in one or two lines and stop — do not manufacture problems.
- Answer in the user's language.

You are investigation-first: deliver a correct diagnosis backed by evidence and, when warranted, the single safest reversible fix — proposed for human approval, never executed behind their back.`

func copilotSelfURL() string {
	if v := os.Getenv("RP_SELF_URL"); v != "" {
		return v
	}
	if v := os.Getenv("RP_LISTEN"); strings.HasPrefix(v, ":") {
		return "http://localhost" + v
	}
	return "http://localhost:8090"
}

// ── Tool-Katalog (JSON-Schema; von Anthropic wie OpenAI genutzt) ─────────────

type copilotTool struct {
	Name   string
	Desc   string
	Schema map[string]any
}

func copilotTools() []copilotTool {
	sp := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	ip := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	o := func(props map[string]any, req ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			m["required"] = req
		}
		return m
	}
	bp := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	tools := []copilotTool{
		{"query_logs", "Read container logs (severity, workload, pod, body) plus a per-bucket volume histogram. Correlate across services by passing several workloads and a tight time window. since/until accept a duration like 15m/2h (relative to now); minSeverity filters by OTel number (9=INFO, 13=WARN, 17=ERROR, 21=FATAL) — use 17 for errors only.", o(map[string]any{"namespace": sp("namespace filter"), "workload": sp("single workload name filter"), "workloads": sp("comma-separated workloads to correlate across services"), "pod": sp("exact pod name filter"), "search": sp("case-insensitive substring in the log body"), "minSeverity": ip("min OTel severity (17 = errors only)"), "since": sp("start of window, duration like 15m/1h (default 15m)"), "until": sp("end of window, duration like 5m ago; omit for now"), "limit": ip("max lines (default 100, cap 1000)")})},
		{"query_traces", "List request traces in a window (service, span, duration ms, status, http status, span/error counts) PLUS per-service RED metrics (rate, error ratio, p50/p95/p99) and a volume histogram. Filter hard: onlyError, minDurationMs (only slow traces), minHttpStatus (400=4xx+, 500=5xx). This is how you find WHICH trace to open — then call get_trace with its traceId.", o(map[string]any{"service": sp("service filter"), "namespace": sp("namespace filter"), "onlyError": bp("only traces with errors"), "minDurationMs": ip("only traces at least this slow (ms)"), "minHttpStatus": ip("only traces with http status >= this (400=4xx+, 500=5xx)"), "since": sp("window start, duration like 15m/1h (default 15m)"), "until": sp("window end, duration; omit for now")})},
		{"get_trace", "Open ONE trace end-to-end: every span across every service with parent/child structure, service, span name, kind, duration ms, status and http status, plus span attributes. This is the waterfall — use it to see exactly where latency or an error originates across services. Get the traceId from query_traces.", o(map[string]any{"traceId": sp("32-hex OTel trace id from query_traces")}, "traceId")},
		{"trace_logs", "Correlate logs to ONE trace: fetches the trace, derives its services and time window, and returns the container logs from exactly that window — so you can read what each service logged during this specific request. Use right after get_trace to tie a span error to its log line.", o(map[string]any{"traceId": sp("32-hex OTel trace id")}, "traceId")},
		{"span_stats", "Latency distribution of ONE operation (service + span) over a window: count, p50/p75/p90/p95/p99 and a duration histogram. Use to confirm whether an operation is regressing or a slow trace is an outlier.", o(map[string]any{"service": sp("service name"), "span": sp("span/operation name"), "since": sp("window, duration like 1h (default 1h)")}, "service", "span")},
		{"query_metrics", "Run a PromQL RANGE query against the embedded engine — RED metrics, CPU/mem, node & workload infra. Pass a real PromQL expression; the result is a time series shown as a chart. Use list_metrics first if unsure which series exist.", o(map[string]any{"query": sp("PromQL range expression, e.g. sum(rate(http_requests_total[5m])) by (service)"), "sinceMin": ip("lookback minutes (default 15)")}, "query")},
		{"list_metrics", "List the metric names available to PromQL (so you can write correct query_metrics expressions). Optionally filter by a substring.", o(map[string]any{"search": sp("optional substring to filter metric names")})},
		{"wait", "Pause a fixed number of seconds, then continue AUTOMATICALLY — use this to let a change settle before you RE-VERIFY it (e.g. after a rollout_restart, wait then re-read logs/service-map). Max 30s. For waiting on a specific action to finish, use wait_for_action instead.", o(map[string]any{"seconds": ip("seconds to wait (1..30, default 8)"), "why": sp("what you are waiting for")})},
		{"wait_for_action", "Wait UNTIL a specific action finishes (reaches succeeded or failed), then continue automatically — use this right after you proposed an action when your next step depends on its outcome. Give the actionId from the action's result. Bounded; if it is still running when the bound elapses it returns so you can wait again or report progress.", o(map[string]any{"actionId": sp("the action id to wait for (from the action result)"), "timeoutSec": ip("max seconds to wait (default 120, max 300)")}, "actionId")},
		{"set_title", "Name THIS investigation for the sidebar. Call it once early (right after you understand the request) with a concise 3-6 word title and a one-line summary, and call it again to update them if the focus changes materially. Does not affect the cluster.", o(map[string]any{"title": sp("concise 3-6 word title"), "summary": sp("one-line description of the investigation")}, "title")},
		{"get_service_map", "Read the whole service map: every workload with health, pods ready/total, restarts, image, plus traffic edges between them. Your situational overview — call this first when asked 'what is wrong'.", o(map[string]any{})},
		{"get_infra", "Read node capacity and pressure (CPU/mem/disk/pods) and PersistentVolumeClaims. Use for capacity, pressure and storage questions.", o(map[string]any{})},
		{"list_resources", "List ANY Kubernetes resource kind in the cluster (compact summaries): Service, Ingress, ConfigMap, Secret (metadata only), Job, CronJob, HorizontalPodAutoscaler, PodDisruptionBudget, NetworkPolicy, ServiceAccount, ResourceQuota, LimitRange, PersistentVolume. Omit kind to get EVERYTHING. This is your full view of the cluster beyond workloads — routing (Ingress/Service), config, batch, policies.", o(map[string]any{"kind": sp("resource kind (exact, e.g. Ingress) — omit for all kinds"), "namespace": sp("filter to one namespace")})},
		{"debug_bundle", "Read-only triage snapshot of a workload or pod: rollout state + container statuses (OOMKilled / CrashLoopBackOff / ImagePullBackOff) + recent Events. Call this before proposing any workload fix.", o(map[string]any{"namespace": sp("namespace"), "kind": sp("Deployment|StatefulSet|DaemonSet|Pod (default Deployment)"), "name": sp("workload or pod name")}, "namespace", "name")},
		{"run_safe_action", "PROPOSE one safe, reversible, verified action from the fixed catalog. It is NOT executed — it pauses for human approval, then runs and returns its real result. This is the ONLY way to change the cluster; there is no YAML/kubectl/helm. Catalog (kind -> target kinds, params): scale -> Deployment,StatefulSet {replicas:0..50}; rollout_restart -> Deployment,StatefulSet,DaemonSet; rollout_undo|rollout_pause|rollout_resume -> Deployment; set_image -> Deployment,StatefulSet,DaemonSet {image}; delete_pod -> Pod; hpa_set -> HorizontalPodAutoscaler {minReplicas?,maxReplicas:1..200}; cronjob_trigger|cronjob_suspend|cronjob_resume -> CronJob; cleanup_pods -> Namespace (targetName = namespace); cordon|uncordon|drain -> Node; node_taint -> Node {key,effect}; node_untaint -> Node {key}; set_resources -> Deployment,StatefulSet,DaemonSet {container?,requestsCpu?,requestsMemory?,limitsCpu?,limitsMemory?}; set_env -> Deployment,StatefulSet,DaemonSet {container?,name,value,remove?}; rollout_to_revision -> Deployment {revision}; rollout_history -> Deployment,StatefulSet (READ) {limit?}; statefulset_partition -> StatefulSet {partition}; hpa_toggle -> HorizontalPodAutoscaler {enabled}; annotate -> most kinds {key,value,remove?}; set_label -> Node,Namespace {key,value,remove?}; patch_configmap -> ConfigMap {key,value,remove?} (needs rollout_restart to take effect); evict_pod -> Pod {gracePeriodSeconds?} (PDB-aware); cleanup_jobs -> Namespace {states?,olderThanHours?}; drain_preview -> Node (READ, blast-radius). For Node/Namespace targets set targetNamespace to '-'. Propose ONE at a time; never invent a kind or param outside this list.", o(map[string]any{"kind": sp("catalog action kind (see list)"), "targetNamespace": sp("namespace, or '-' for Node/cluster-scoped"), "targetKind": sp("Deployment|StatefulSet|DaemonSet|Pod|HorizontalPodAutoscaler|CronJob|Namespace|Node"), "targetName": sp("object name (for cleanup_pods: the namespace)"), "params": map[string]any{"type": "object", "description": "typed params for the kind, e.g. {\"replicas\":3} or {\"image\":\"nginx:1.27\"} or {\"maxReplicas\":8}"}}, "kind", "targetKind", "targetName")},
	}
	// Jedes READ-Tool bekommt ein optionales `expect`-Feld: Ziel + Annahme dieses
	// Aufrufs. Der Inspector zeigt es als „Ziel & Annahme" ÜBER dem Ergebnis, damit
	// der Mensch die Hypothese des Agenten vor dem Resultat sieht.
	readTools := map[string]bool{"query_logs": true, "query_traces": true, "get_trace": true, "trace_logs": true, "span_stats": true, "query_metrics": true, "list_metrics": true, "get_service_map": true, "get_infra": true, "debug_bundle": true}
	for i := range tools {
		if readTools[tools[i].Name] {
			if props, ok := tools[i].Schema["properties"].(map[string]any); ok {
				props["expect"] = sp("REQUIRED. One line: your goal for this call + what you expect to find (e.g. 'Goal: find the crash cause. Expect OOMKilled in events.'). Shown to the human.")
			}
		}
	}
	return tools
}

// execTool proxyt read-Tools an die eigene API (mit weitergereichtem Cookie).
// run_safe_action wird NICHT ausgeführt — der Loop behandelt es separat als Vorschlag.
func (s *Server) execCopilotTool(ctx context.Context, scope, org, cluster, cookie, name string, args map[string]any) (string, error) {
	getStr := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	getInt := func(k string, def int) int {
		switch v := args[k].(type) {
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
		return def
	}
	base := fmt.Sprintf("%s/api/orgs/%s/clusters/%s", copilotSelfURL(), org, cluster)
	get := func(path string, q url.Values) (string, error) {
		u := base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		req.Header.Set("Cookie", cookie)
		return copilotDo(req)
	}
	post := func(path string, body any) (string, error) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(b))
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Content-Type", "application/json")
		return copilotDo(req)
	}
	put := func(q url.Values, k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}

	// durAgo wandelt eine Dauer ("5m") in einen RFC3339-Zeitpunkt (jetzt - Dauer);
	// die until-Filter der Handler erwarten RFC3339, since akzeptiert beides.
	durAgo := func(s string) string {
		if d, err := time.ParseDuration(s); err == nil {
			return time.Now().Add(-d).Format(time.RFC3339)
		}
		return s
	}

	// Scope-Default: ist ein Namespace gewählt, filtern Reads standardmässig darauf.
	scopedNS := func(k string) string {
		v := getStr(k)
		if v == "" && scope != "" {
			return scope
		}
		return v
	}

	switch name {
	case "query_logs":
		q := url.Values{}
		put(q, "namespace", scopedNS("namespace"))
		put(q, "workload", getStr("workload"))
		put(q, "workloads", getStr("workloads"))
		put(q, "pod", getStr("pod"))
		put(q, "search", getStr("search"))
		if ms := getInt("minSeverity", 0); ms > 0 {
			q.Set("minSeverity", strconv.Itoa(ms))
		}
		since := getStr("since")
		if since == "" {
			since = "15m"
		}
		q.Set("since", since)
		if u := getStr("until"); u != "" {
			q.Set("until", durAgo(u))
		}
		q.Set("limit", strconv.Itoa(getInt("limit", 100)))
		return get("/logs", q)
	case "query_traces":
		q := url.Values{}
		put(q, "service", getStr("service"))
		put(q, "namespace", scopedNS("namespace"))
		if b, ok := args["onlyError"].(bool); ok && b {
			q.Set("onlyError", "true")
		} else if getStr("onlyError") == "true" {
			q.Set("onlyError", "true")
		}
		if md := getInt("minDurationMs", 0); md > 0 {
			q.Set("minDurationMs", strconv.Itoa(md))
		}
		if ms := getInt("minHttpStatus", 0); ms > 0 {
			q.Set("minHttpStatus", strconv.Itoa(ms))
		}
		since := getStr("since")
		if since == "" {
			since = "15m"
		}
		q.Set("since", since)
		if u := getStr("until"); u != "" {
			q.Set("until", durAgo(u))
		}
		return get("/traces", q)
	case "get_trace":
		id := getStr("traceId")
		if id == "" {
			return "", fmt.Errorf("get_trace requires traceId")
		}
		return get("/traces/"+url.PathEscape(id), nil)
	case "trace_logs":
		id := getStr("traceId")
		if id == "" {
			return "", fmt.Errorf("trace_logs requires traceId")
		}
		raw, err := get("/traces/"+url.PathEscape(id), nil)
		if err != nil {
			return "", err
		}
		var td struct {
			Spans []struct {
				ServiceName string  `json:"serviceName"`
				Namespace   string  `json:"namespace"`
				StartUnixNs int64   `json:"startUnixNs"`
				DurationMs  float64 `json:"durationMs"`
			} `json:"spans"`
		}
		if json.Unmarshal([]byte(raw), &td); len(td.Spans) == 0 {
			return raw, nil
		}
		svcSet := map[string]bool{}
		nsSet := map[string]bool{}
		var minNs, maxNs int64
		for i, sp := range td.Spans {
			if sp.ServiceName != "" {
				svcSet[sp.ServiceName] = true
			}
			if sp.Namespace != "" {
				nsSet[sp.Namespace] = true
			}
			end := sp.StartUnixNs + int64(sp.DurationMs*1e6)
			if i == 0 || sp.StartUnixNs < minNs {
				minNs = sp.StartUnixNs
			}
			if i == 0 || end > maxNs {
				maxNs = end
			}
		}
		svcs := make([]string, 0, len(svcSet))
		for s := range svcSet {
			svcs = append(svcs, s)
		}
		nss := make([]string, 0, len(nsSet))
		for n := range nsSet {
			nss = append(nss, n)
		}
		since := time.Unix(0, minNs).Add(-3 * time.Second).Format(time.RFC3339)
		until := time.Unix(0, maxNs).Add(3 * time.Second).Format(time.RFC3339)
		q := url.Values{}
		if len(svcs) > 0 {
			q.Set("workloads", strings.Join(svcs, ","))
		}
		if len(nss) == 1 {
			q.Set("namespace", nss[0])
		}
		q.Set("since", since)
		q.Set("until", until)
		q.Set("limit", "200")
		logs, err := get("/logs", q)
		if err != nil {
			return "", err
		}
		// Logs-Payload mit Trace-Kontext anreichern (das UI rendert lines/histogram).
		var lg map[string]any
		if json.Unmarshal([]byte(logs), &lg) == nil {
			lg["traceId"] = id
			lg["services"] = svcs
			lg["window"] = map[string]string{"since": since, "until": until}
			b, _ := json.Marshal(lg)
			return string(b), nil
		}
		return logs, nil
	case "span_stats":
		q := url.Values{}
		q.Set("service", getStr("service"))
		q.Set("span", getStr("span"))
		since := getStr("since")
		if since == "" {
			since = "1h"
		}
		q.Set("since", since)
		return get("/span-stats", q)
	case "query_metrics":
		end := time.Now().Unix()
		start := end - int64(getInt("sinceMin", 15))*60
		step := (end - start) / 120
		if step < 5 {
			step = 5
		}
		q := url.Values{}
		q.Set("query", getStr("query"))
		q.Set("start", strconv.FormatInt(start, 10))
		q.Set("end", strconv.FormatInt(end, 10))
		q.Set("step", strconv.FormatInt(step, 10))
		return get("/promql/api/v1/query_range", q)
	case "list_metrics":
		res, err := get("/promql/api/v1/label/__name__/values", nil)
		if err != nil {
			return "", err
		}
		return filterMetricNames(res, getStr("search")), nil
	case "wait":
		sec := getInt("seconds", 8)
		if sec < 1 {
			sec = 1
		}
		if sec > 30 {
			sec = 30
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(sec) * time.Second):
		}
		return fmt.Sprintf("Waited %ds. Now re-verify with a read tool.", sec), nil
	case "wait_for_action":
		id := getStr("actionId")
		if id == "" {
			return "", fmt.Errorf("wait_for_action requires actionId")
		}
		timeout := getInt("timeoutSec", 120)
		if timeout < 5 {
			timeout = 5
		}
		if timeout > 300 {
			timeout = 300
		}
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		var last struct{ status, result, progress string }
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(2 * time.Second):
			}
			res, err := get("/actions", url.Values{"limit": {"40"}})
			if err != nil {
				continue
			}
			var lst struct {
				Actions []struct {
					ID       string `json:"id"`
					Status   string `json:"status"`
					Result   string `json:"result"`
					Progress string `json:"progress"`
				} `json:"actions"`
			}
			if json.Unmarshal([]byte(res), &lst) != nil {
				continue
			}
			for _, ac := range lst.Actions {
				if ac.ID != id {
					continue
				}
				last.status, last.result, last.progress = ac.Status, ac.Result, ac.Progress
				if ac.Status == "succeeded" || ac.Status == "failed" {
					b, _ := json.Marshal(map[string]any{"actionId": id, "status": ac.Status, "result": ac.Result, "progress": ac.Progress, "finished": true})
					return string(b), nil
				}
				break
			}
		}
		b, _ := json.Marshal(map[string]any{"actionId": id, "status": orDefStr(last.status, "running"), "progress": last.progress, "finished": false, "note": fmt.Sprintf("still running after %ds — call wait_for_action again, or tell the human it is in progress", timeout)})
		return string(b), nil
	case "get_service_map":
		return get("/service-map", nil)
	case "get_infra":
		return get("/infra", nil)
	case "list_resources":
		res, err := get("/inventory", url.Values{"kind": {getStr("kind")}})
		if err != nil {
			return "", err
		}
		return filterInventoryNS(res, scopedNS("namespace")), nil
	case "debug_bundle":
		kind := getStr("kind")
		if kind == "" {
			kind = "Deployment"
		}
		return post("/actions", map[string]any{"kind": "debug_bundle", "targetNamespace": getStr("namespace"), "targetKind": kind, "targetName": getStr("name")})
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// filterMetricNames reduziert die Prometheus-label-values-Antwort auf die
// Metriknamen (optional per Substring gefiltert) — kompakt fürs Modell.
func filterMetricNames(raw, search string) string {
	var resp struct {
		Data []string `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &resp) != nil {
		return raw
	}
	out := resp.Data
	if search != "" {
		filtered := out[:0:0]
		for _, n := range resp.Data {
			if strings.Contains(strings.ToLower(n), strings.ToLower(search)) {
				filtered = append(filtered, n)
			}
		}
		out = filtered
	}
	b, _ := json.Marshal(map[string]any{"metrics": out, "count": len(out)})
	return string(b)
}

func copilotDo(req *http.Request) (string, error) {
	resp, err := copilotClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("api %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	// Volles (parsebares) JSON zurück — das UI-Panel visualisiert es. Fürs LLM
	// wird separat (clampForLLM) gekürzt, damit das Token-Budget passt.
	return string(b), nil
}

// clampForLLM kürzt sehr große Tool-Payloads NUR für den Modell-Kontext.
func clampForLLM(s string) string {
	const max = 12000
	if len(s) > max {
		return s[:max] + "\n…(truncated for the model; the full result is shown in the inspector)"
	}
	return s
}

var copilotClient = &http.Client{Timeout: 40 * time.Second}

// ── Chat-Endpoint ────────────────────────────────────────────────────────────

type copilotMsg struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
}
type copilotConfig struct {
	API     string `json:"api"` // anthropic | openai
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}
type copilotReq struct {
	Messages []copilotMsg  `json:"messages"`
	Config   copilotConfig `json:"config"`
	Scope    string        `json:"scope"` // aktiver Namespace ("" = alle) — hartes Gate + Prompt
}

// Ausgabe-Blöcke fürs UI: Text, ein Action-Vorschlag, oder eine Tool-Aktivität.
type copilotBlock struct {
	Type   string         `json:"type"` // text | action | tool
	Text   string         `json:"text,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Action map[string]any `json:"action,omitempty"`
}

// handleCopilotChat streamt die Antwort als SSE (event: text|tool|action|done|error),
// damit Tokens progressiv wie bei ChatGPT im UI erscheinen.
func (s *Server) handleCopilotChat(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	cluster := r.PathValue("cluster")
	var req copilotReq
	if !decode(w, r, &req) {
		return
	}
	if req.Config.APIKey == "" || req.Config.Model == "" {
		writeErr(w, http.StatusBadRequest, "no LLM provider configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	cookie := r.Header.Get("Cookie")
	org := orgID.String()

	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform verbietet Proxies (inkl. Next dev/prod) das gzip-Komprimieren —
	// gzip puffert den ganzen Body und zerstört sonst das Token-für-Token-Streaming.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Content-Encoding", "identity")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	// runID identifiziert diesen Stream für den Freigabe-Endpoint (Human-in-the-Loop).
	runID := uuid.NewString()
	emit("meta", map[string]any{"runId": runID})

	if req.Config.API == "openai" {
		s.streamOpenAI(r.Context(), runID, org, cluster, cookie, req, emit)
		return
	}
	s.streamAnthropic(r.Context(), runID, org, cluster, cookie, req, emit)
}

func actionBlock(input map[string]any) copilotBlock {
	kind, _ := input["kind"].(string)
	ns, _ := input["targetNamespace"].(string)
	name, _ := input["targetName"].(string)
	target := name
	if ns != "" && ns != "-" {
		target = ns + " / " + name
	}
	params, _ := input["params"].(map[string]any)
	level := actionLevel(kind, params)
	return copilotBlock{Type: "action", Action: map[string]any{
		"kind": kind, "target": target, "level": level, "klass": level, "input": input,
		"reason": "proposed by Copilot",
	}}
}

// filterInventoryNS filtert die Inventar-Antwort optional auf einen Namespace
// (kompakter fürs Modell; cluster-scoped Items wie PVs bleiben immer drin).
func filterInventoryNS(raw, ns string) string {
	if ns == "" {
		return raw
	}
	var resp struct {
		Kinds []struct {
			Kind  string           `json:"kind"`
			Items []map[string]any `json:"items"`
		} `json:"kinds"`
	}
	if json.Unmarshal([]byte(raw), &resp) != nil {
		return raw
	}
	out := map[string]any{"namespace": ns}
	kinds := []map[string]any{}
	for _, k := range resp.Kinds {
		items := []map[string]any{}
		for _, it := range k.Items {
			ins, _ := it["namespace"].(string)
			if ins == "" || ins == ns {
				items = append(items, it)
			}
		}
		if len(items) > 0 {
			kinds = append(kinds, map[string]any{"kind": k.Kind, "items": items})
		}
	}
	out["kinds"] = kinds
	b, _ := json.Marshal(out)
	return string(b)
}
