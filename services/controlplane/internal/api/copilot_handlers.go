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
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
)

// copilot_handlers.go — Tool-Katalog + Tool-Ausführung + Chat-Endpoint des
// Copilots. Die Orchestrierung (Master + Investigator-Subagenten, Verdicts,
// Investigation-Graph) lebt in copilot_master.go / copilot_investigator.go.
// Read-Tools laufen automatisch (execCopilotTool proxied auf die eigene REST-
// API); Mutationen sind IMMER Vorschläge über das Approval-Gate
// (copilot_action.go). Anthropic- UND OpenAI-kompatibel (copilot_provider.go).


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

// actionCatalogDesc ist der EINE Katalog-Text (run_safe_action im Legacy-/MCP-
// Pfad und propose_action im Master teilen ihn — eine Quelle der Wahrheit).
const actionCatalogDesc = "Catalog (kind -> target kinds, params): scale -> Deployment,StatefulSet {replicas:0..50}; rollout_restart -> Deployment,StatefulSet,DaemonSet; rollout_undo|rollout_pause|rollout_resume -> Deployment; set_image -> Deployment,StatefulSet,DaemonSet {image}; delete_pod -> Pod; hpa_set -> HorizontalPodAutoscaler {minReplicas?,maxReplicas:1..200}; cronjob_trigger|cronjob_suspend|cronjob_resume -> CronJob; cleanup_pods -> Namespace (targetName = namespace); cordon|uncordon|drain -> Node; node_taint -> Node {key,effect}; node_untaint -> Node {key}; set_resources -> Deployment,StatefulSet,DaemonSet {container?,requestsCpu?,requestsMemory?,limitsCpu?,limitsMemory?}; set_env -> Deployment,StatefulSet,DaemonSet {container?,name,value,remove?}; rollout_to_revision -> Deployment {revision}; rollout_history -> Deployment,StatefulSet (READ) {limit?}; statefulset_partition -> StatefulSet {partition}; hpa_toggle -> HorizontalPodAutoscaler {enabled}; annotate -> most kinds {key,value,remove?}; set_label -> Node,Namespace {key,value,remove?}; patch_configmap -> ConfigMap {key,value,remove?} (needs rollout_restart to take effect); evict_pod -> Pod {gracePeriodSeconds?} (PDB-aware); cleanup_jobs -> Namespace {states?,olderThanHours?}; drain_preview -> Node (READ, blast-radius); exec_readonly -> Pod {command:argv array (cat|ls|head|tail|df|du|stat|wc|env|id|uname|find, no shell), container?} — read a file INSIDE a container (e.g. an error log only written to disk); patch_secret -> Secret {key, value, remove?}; create_configmap -> ConfigMap {data:{k:v,...}}; delete_configmap -> ConfigMap (snapshot kept for restore); pdb_set -> PodDisruptionBudget {minAvailable? XOR maxUnavailable?, int or percent string}; patch_resource -> Service,Ingress,NetworkPolicy,PodDisruptionBudget {patch: JSON merge patch} (generic spec edit, snapshot + restorable); pvc_expand -> PersistentVolumeClaim {size, only GROW}; delete_job -> Job; restore_resource -> {snapshot} (re-apply a before-snapshot from a prior action). For Node/Namespace targets set targetNamespace to '-'. Propose ONE at a time; never invent a kind or param outside this list."

// actionProposalProps sind die gemeinsamen Properties von run_safe_action und
// propose_action.
func actionProposalProps(sp func(string) map[string]any) map[string]any {
	return map[string]any{
		"kind":            sp("catalog action kind (see list)"),
		"targetNamespace": sp("namespace, or '-' for Node/cluster-scoped"),
		"targetKind":      sp("Deployment|StatefulSet|DaemonSet|Pod|HorizontalPodAutoscaler|CronJob|Namespace|Node"),
		"targetName":      sp("object name (for cleanup_pods: the namespace)"),
		"params":          map[string]any{"type": "object", "description": "typed params for the kind, e.g. {\"replicas\":3} or {\"image\":\"nginx:1.27\"} or {\"maxReplicas\":8}"},
	}
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
	ap := func(d string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": d}
	}
	tools := []copilotTool{
		{"query_logs", "Read container logs (severity, workload, pod, body) plus a per-bucket volume histogram. Correlate across services by passing several workloads and a tight time window. since/until accept a duration like 15m/2h (relative to now); minSeverity filters by OTel number (9=INFO, 13=WARN, 17=ERROR, 21=FATAL) — use 17 for errors only. Powerful body matching: regexes (RE2, case-sensitive — prefix (?i) for insensitive) combined with regexMode any|all, exclude terms (NOT), fuzzy (typo-tolerant). Example: regexes:[\"(?i)oom|out of memory\",\"exit code 137\"] regexMode:\"any\" exclude:[\"health check\"].", o(map[string]any{"namespace": sp("namespace filter"), "workload": sp("single workload name filter"), "workloads": sp("comma-separated workloads to correlate across services"), "pod": sp("exact pod name filter"), "search": sp("case-insensitive substring in the log body"), "regexes": ap("up to 5 RE2 regex patterns matched against the log body; combine with regexMode"), "regexMode": sp("how to combine regexes: any (OR, default) | all (AND)"), "exclude": ap("up to 5 case-insensitive substrings that must NOT appear (filters noise)"), "fuzzy": sp("typo-tolerant ngram search term (finds near-matches of this string)"), "minSeverity": ip("min OTel severity (17 = errors only)"), "since": sp("start of window, duration like 15m/1h (default 15m)"), "until": sp("end of window, duration like 5m ago; omit for now"), "limit": ip("max lines (default 100, cap 1000)")})},
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
		{"get_resource", "Read the FULL live spec of one Kubernetes object as YAML (like kubectl get -o yaml, managedFields/status stripped): Deployment, StatefulSet, DaemonSet, Pod, ConfigMap (full data!), Service, Ingress, NetworkPolicy, PodDisruptionBudget, HPA, CronJob, Job, PVC, Node, Namespace, ServiceAccount, ResourceQuota, LimitRange. Secrets show keys + sha256 hashes only. THE tool to inspect configuration.", o(map[string]any{"namespace": sp("namespace ('-' for cluster-scoped)"), "kind": sp("exact resource kind, e.g. ConfigMap"), "name": sp("object name")}, "kind", "name")},
		{"describe_resource", "kubectl-describe-like view of one object: status, conditions, and its recent events. Use when you need the CURRENT STATE + why (conditions/events), not the spec.", o(map[string]any{"namespace": sp("namespace ('-' for cluster-scoped)"), "kind": sp("exact resource kind"), "name": sp("object name")}, "kind", "name")},
		{"get_secret", "Inspect a Secret WITHOUT revealing values: keys, value lengths and sha256 hashes (compare hashes to spot changed/identical values). Values never reach you or the database.", o(map[string]any{"namespace": sp("namespace"), "name": sp("secret name")}, "namespace", "name")},
		{"helm_releases", "List Helm releases (from sh.helm.release.v1 secrets): name, namespace, chart version, status, revision. Use to see what Helm manages and which release a broken object belongs to.", o(map[string]any{"namespace": sp("filter to one namespace (omit for all)")})},
		{"run_safe_action", "PROPOSE one safe, reversible, verified action from the fixed catalog. It is NOT executed — it pauses for human approval, then runs and returns its real result. This is the ONLY way to change the cluster; there is no YAML/kubectl/helm. " + actionCatalogDesc, o(actionProposalProps(sp), "kind", "targetKind", "targetName")},
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
	// getStrs liest ein String-Array-Argument; ein einzelner String zählt als
	// Ein-Element-Liste (LLMs schicken gern beides).
	getStrs := func(k string) []string {
		var out []string
		switch v := args[k].(type) {
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			}
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, v)
			}
		}
		return out
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

	// Scope-ERZWINGUNG (symmetrisch zum Write-Gate scopeViolation): ist ein
	// Namespace gewählt, laufen Reads AUSSCHLIESSLICH darin — ein explizit vom
	// LLM genannter Fremd-Namespace wird ignoriert, sonst könnte ein
	// namespace-scoped Chat jeden anderen Namespace auslesen (nur Namen nennen).
	scopedNS := func(k string) string {
		if scope != "" {
			return scope
		}
		return getStr(k)
	}

	switch name {
	case "query_logs":
		q := url.Values{}
		put(q, "namespace", scopedNS("namespace"))
		put(q, "workload", getStr("workload"))
		put(q, "workloads", getStr("workloads"))
		put(q, "pod", getStr("pod"))
		put(q, "search", getStr("search"))
		for _, rx := range getStrs("regexes") {
			q.Add("regex", rx)
		}
		put(q, "regexMode", getStr("regexMode"))
		for _, ex := range getStrs("exclude") {
			q.Add("exclude", ex)
		}
		put(q, "fuzzy", getStr("fuzzy"))
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
		return s.runReadAction(ctx, post, get, map[string]any{"kind": "debug_bundle", "targetNamespace": scopedNS("namespace"), "targetKind": kind, "targetName": getStr("name")})
	case "get_resource", "describe_resource":
		ns := scopedNS("namespace")
		kind := getStr("kind")
		if kind == "Node" || kind == "Namespace" || kind == "PersistentVolume" {
			ns = "-"
		}
		return s.runReadAction(ctx, post, get, map[string]any{"kind": name, "targetNamespace": ns, "targetKind": kind, "targetName": getStr("name")})
	case "get_secret":
		return s.runReadAction(ctx, post, get, map[string]any{"kind": "get_secret", "targetNamespace": scopedNS("namespace"), "targetKind": "Secret", "targetName": getStr("name")})
	case "helm_releases":
		ns := scopedNS("namespace")
		if ns == "" {
			ns = "-" // alle Namespaces
		}
		return s.runReadAction(ctx, post, get, map[string]any{"kind": "helm_releases", "targetNamespace": "-", "targetKind": "Namespace", "targetName": orDefStr(ns, "-")})
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// runReadAction legt eine read-only Katalog-Action an und wartet SYNCHRON auf
// ihr Ergebnis (kurzer Poll) — Investigatoren haben ein knappes Tool-Budget und
// sollen das Ergebnis im selben Call bekommen, nicht via wait_for_action.
func (s *Server) runReadAction(ctx context.Context, post func(string, any) (string, error), get func(string, url.Values) (string, error), body map[string]any) (string, error) {
	created, err := post("/actions", body)
	if err != nil {
		return "", err
	}
	var act struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(created), &act) != nil || act.ID == "" {
		return created, nil
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(700 * time.Millisecond):
		}
		res, err := get("/actions", url.Values{"limit": {"40"}})
		if err != nil {
			continue
		}
		var lst struct {
			Actions []struct {
				ID     string          `json:"id"`
				Status string          `json:"status"`
				Result string          `json:"result"`
				Steps  json.RawMessage `json:"steps"`
			} `json:"actions"`
		}
		if json.Unmarshal([]byte(res), &lst) != nil {
			continue
		}
		for _, a := range lst.Actions {
			if a.ID != act.ID {
				continue
			}
			if a.Status == "succeeded" || a.Status == "failed" || a.Status == "cancelled" {
				out := map[string]any{"status": a.Status, "result": a.Result}
				if len(a.Steps) > 0 {
					out["steps"] = a.Steps
				}
				b, _ := json.Marshal(out)
				return string(b), nil
			}
			break
		}
	}
	return fmt.Sprintf(`{"status":"running","actionId":%q,"note":"still running — use wait_for_action"}`, act.ID), nil
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
	API      string `json:"api"` // anthropic | openai
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	SubModel string `json:"subModel"` // optionales Modell für Investigator-Subagenten ("" = Model)
}
type copilotReq struct {
	Messages []copilotMsg  `json:"messages"`
	Config   copilotConfig `json:"config"`
	Scope    string        `json:"scope"`  // aktiver Namespace ("" = alle) — hartes Gate + Prompt
	ChatID   string        `json:"chatId"` // Client-seitige Chat-UUID — bindet den Investigation-Graph
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
	userID := uuid.Nil
	if user, ok := auth.UserFrom(r.Context()); ok {
		userID = user.ID
	}

	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform verbietet Proxies (inkl. Next dev/prod) das gzip-Komprimieren —
	// gzip puffert den ganzen Body und zerstört sonst das Token-für-Token-Streaming.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Content-Encoding", "identity")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// emit ist Mutex-geschützt: parallele Investigator-Goroutinen streamen
	// gleichzeitig in denselben ResponseWriter.
	var emitMu sync.Mutex
	emit := func(event string, data any) {
		b, _ := json.Marshal(data)
		emitMu.Lock()
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
		emitMu.Unlock()
	}

	// runID identifiziert diesen Stream für den Freigabe-Endpoint (Human-in-the-Loop).
	runID := uuid.NewString()
	emit("meta", map[string]any{"runId": runID})

	s.runMasterLoop(r.Context(), runID, userID, org, cluster, cookie, req, emit)
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
