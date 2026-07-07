// Command mcp ist der rocketplaneIO MCP-Server: er macht die Observability- und
// Safe-Actions-Fläche als TOOLS über das Model Context Protocol (JSON-RPC 2.0 über
// stdio) zugänglich. Damit kann jeder MCP-Client — Claude Code, Cursor ODER der
// interne Copilot — den Cluster lesen (logs/traces/metrics/topology/infra) und
// über den SICHEREN Action-Katalog handeln (verifiziert, auto-rollback).
//
// Der Server ist ein dünner Adapter: jeder tools/call übersetzt in einen Aufruf
// der Control-Plane-HTTP-API (Session-Cookie) — keine DB-Logik dupliziert, exakt
// dieselben Endpoints und Sicherheits-Gates wie die UI.
//
// Env: RP_API_URL (default http://localhost:8090) · RP_SESSION (rp_session-Cookie)
//      RP_ORG · RP_CLUSTER (Scope). Start:  RP_ORG=… RP_CLUSTER=… RP_SESSION=… mcp
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const protocolVersion = "2024-11-05"

var (
	apiURL  = envOr("RP_API_URL", "http://localhost:8090")
	session = os.Getenv("RP_SESSION")
	org     = os.Getenv("RP_ORG")
	cluster = os.Getenv("RP_CLUSTER")
	client  = &http.Client{Timeout: 30 * time.Second}
)

func base() string {
	return fmt.Sprintf("%s/api/orgs/%s/clusters/%s", strings.TrimRight(apiURL, "/"), org, cluster)
}

// ── HTTP-Proxy zur Control-Plane ─────────────────────────────────────────────

func apiGet(path string, q url.Values) (string, error) {
	u := base() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Cookie", "rp_session="+session)
	return do(req)
}

func apiPost(path string, body any) (string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base()+path, bytes.NewReader(b))
	req.Header.Set("Cookie", "rp_session="+session)
	req.Header.Set("Content-Type", "application/json")
	return do(req)
}

func do(req *http.Request) (string, error) {
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("api %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

// ── Tool-Registry (der „MCP-Werkzeugkasten") ─────────────────────────────────

type tool struct {
	name   string
	desc   string
	schema map[string]any
	call   func(args map[string]any) (string, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func intp(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

func s(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return v
	}
	return ""
}
func i(args map[string]any, k string, def int) int {
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

func tools() []tool {
	return []tool{
		{
			name: "query_logs",
			desc: "Read recent container logs (severity, workload, body). Use to investigate errors. Filter by namespace/workload and a full-text search.",
			schema: obj(map[string]any{
				"namespace": str("namespace filter (optional)"),
				"workload":  str("workload/service name filter (optional)"),
				"search":    str("full-text search in the log body (optional)"),
				"since":     str("lookback window, e.g. 15m, 1h, 6h (default 15m)"),
				"limit":     intp("max lines (default 50)"),
			}),
			call: func(a map[string]any) (string, error) {
				q := url.Values{}
				put(q, "namespace", s(a, "namespace"))
				put(q, "workload", s(a, "workload"))
				put(q, "search", s(a, "search"))
				q.Set("since", orDef(s(a, "since"), "15m"))
				q.Set("limit", strconv.Itoa(i(a, "limit", 50)))
				return apiGet("/logs", q)
			},
		},
		{
			name:   "get_service_map",
			desc:   "Read the live service map: workloads, health (healthy/degraded/critical), and conntrack flow edges between them. The topology at a glance.",
			schema: obj(map[string]any{}),
			call:   func(a map[string]any) (string, error) { return apiGet("/service-map", nil) },
		},
		{
			name:   "get_infra",
			desc:   "Read cluster infrastructure: nodes (CPU/memory/disk pressure, ready/unschedulable) and persistent volume claims.",
			schema: obj(map[string]any{}),
			call:   func(a map[string]any) (string, error) { return apiGet("/infra", nil) },
		},
		{
			name: "query_metrics",
			desc: "Run a PromQL range query against the embedded engine (RED metrics from eBPF spans, log rates, node/workload infra metrics). Returns a time-series matrix.",
			schema: obj(map[string]any{
				"query":    str("PromQL range expression, e.g. sum by (service_name) (rate(http_server_request_duration_count[5m]))"),
				"sinceMin": intp("lookback in minutes (default 15)"),
			}, "query"),
			call: func(a map[string]any) (string, error) {
				end := time.Now().Unix()
				start := end - int64(i(a, "sinceMin", 15))*60
				step := (end - start) / 120
				if step < 5 {
					step = 5
				}
				q := url.Values{}
				q.Set("query", s(a, "query"))
				q.Set("start", strconv.FormatInt(start, 10))
				q.Set("end", strconv.FormatInt(end, 10))
				q.Set("step", strconv.FormatInt(step, 10))
				return apiGet("/promql/api/v1/query_range", q)
			},
		},
		{
			name: "query_traces",
			desc: "Read recent distributed traces (service, span, duration, status). Filter by service/namespace. Use to find slow or failing requests.",
			schema: obj(map[string]any{
				"service":   str("service name filter (optional)"),
				"namespace": str("namespace filter (optional)"),
				"limit":     intp("max traces (default 25)"),
			}),
			call: func(a map[string]any) (string, error) {
				q := url.Values{}
				put(q, "service", s(a, "service"))
				put(q, "namespace", s(a, "namespace"))
				q.Set("limit", strconv.Itoa(i(a, "limit", 25)))
				return apiGet("/traces", q)
			},
		},
		{
			name:   "list_actions",
			desc:   "List recent safe-action runs on this cluster with their status and step timeline (the audit trail of what was done).",
			schema: obj(map[string]any{"limit": intp("max runs (default 20)")}),
			call: func(a map[string]any) (string, error) {
				q := url.Values{}
				q.Set("limit", strconv.Itoa(i(a, "limit", 20)))
				return apiGet("/actions", q)
			},
		},
		{
			name: "debug_bundle",
			desc: "READ-ONLY triage snapshot of a workload or pod: rollout state + container statuses (OOMKilled/CrashLoop via last-state) + recent events. One call instead of five kubectls.",
			schema: obj(map[string]any{
				"namespace": str("namespace"),
				"kind":      str("Deployment | StatefulSet | DaemonSet | Pod (default Deployment)"),
				"name":      str("workload or pod name"),
			}, "namespace", "name"),
			call: func(a map[string]any) (string, error) {
				return apiPost("/actions", map[string]any{
					"kind": "debug_bundle", "targetNamespace": s(a, "namespace"),
					"targetKind": orDef(s(a, "kind"), "Deployment"), "targetName": s(a, "name"),
				})
			},
		},
		{
			name: "run_safe_action",
			desc: "Execute a SAFE, reversible action from the catalog (scale, rollout_restart, rollout_undo, set_image, hpa_set, cordon, drain, cleanup_pods, …). Verified on the pod, auto-rolled-back on failure. Only ever offer this after investigating; destructive kinds should be confirmed by a human.",
			schema: obj(map[string]any{
				"kind":            str("action kind, e.g. scale | rollout_restart | set_image | hpa_set | cordon | cleanup_pods"),
				"targetNamespace": str("target namespace (or '-' for Node/Namespace-scoped)"),
				"targetKind":      str("Deployment | StatefulSet | DaemonSet | Pod | Node | Namespace | HorizontalPodAutoscaler | CronJob"),
				"targetName":      str("target object name"),
				"params":          map[string]any{"type": "object", "description": "typed params, e.g. {\"replicas\":3} or {\"image\":\"repo:tag\"}"},
			}, "kind", "targetKind", "targetName"),
			call: func(a map[string]any) (string, error) {
				body := map[string]any{
					"kind": s(a, "kind"), "targetNamespace": s(a, "targetNamespace"),
					"targetKind": s(a, "targetKind"), "targetName": s(a, "targetName"),
				}
				if p, ok := a["params"].(map[string]any); ok {
					body["params"] = p
				}
				return apiPost("/actions", body)
			},
		},
	}
}

func put(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}
func orDef(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ── JSON-RPC 2.0 / MCP über stdio ────────────────────────────────────────────

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func reply(id json.RawMessage, result any) {
	writeMsg(map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "result": result})
}
func replyErr(id json.RawMessage, code int, msg string) {
	writeMsg(map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "error": map[string]any{"code": code, "message": msg}})
}
func rawOrNull(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}
func writeMsg(m map[string]any) {
	b, _ := json.Marshal(m)
	os.Stdout.Write(append(b, '\n'))
}

func main() {
	reg := tools()
	byName := map[string]tool{}
	for _, t := range reg {
		byName[t.name] = t
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		// Notifications (kein id) werden nicht beantwortet.
		if len(req.ID) == 0 && strings.HasPrefix(req.Method, "notifications/") {
			continue
		}
		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "rocketplaneio", "version": "0.1.0"},
			})
		case "tools/list":
			list := make([]map[string]any, 0, len(reg))
			for _, t := range reg {
				list = append(list, map[string]any{"name": t.name, "description": t.desc, "inputSchema": t.schema})
			}
			reply(req.ID, map[string]any{"tools": list})
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			t, ok := byName[p.Name]
			if !ok {
				replyErr(req.ID, -32602, "unknown tool: "+p.Name)
				continue
			}
			out, err := t.call(p.Arguments)
			if err != nil {
				reply(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "error: " + err.Error()}}, "isError": true})
				continue
			}
			reply(req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": out}}})
		case "ping":
			reply(req.ID, map[string]any{})
		default:
			if len(req.ID) > 0 {
				replyErr(req.ID, -32601, "method not found: "+req.Method)
			}
		}
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
