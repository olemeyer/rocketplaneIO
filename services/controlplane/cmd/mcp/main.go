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
			desc: "Read recent container logs (severity, workload, body). Use to investigate errors. Filter by namespace/workload; body matching supports substring (search), RE2 regex (regex, prefix (?i) for case-insensitive), NOT-substrings (exclude) and a typo-tolerant fuzzy term.",
			schema: obj(map[string]any{
				"namespace": str("namespace filter (optional)"),
				"workload":  str("workload/service name filter (optional)"),
				"search":    str("case-insensitive substring in the log body (optional)"),
				"regex":     str("RE2 regex on the log body, e.g. (?i)oom|exit code 137 (optional)"),
				"exclude":   str("comma-separated substrings that must NOT appear (optional)"),
				"fuzzy":     str("typo-tolerant search term (optional)"),
				"since":     str("lookback window, e.g. 15m, 1h, 6h (default 15m)"),
				"limit":     intp("max lines (default 50)"),
			}),
			call: func(a map[string]any) (string, error) {
				q := url.Values{}
				put(q, "namespace", s(a, "namespace"))
				put(q, "workload", s(a, "workload"))
				put(q, "search", s(a, "search"))
				put(q, "regex", s(a, "regex"))
				for _, ex := range strings.Split(s(a, "exclude"), ",") {
					if ex = strings.TrimSpace(ex); ex != "" {
						q.Add("exclude", ex)
					}
				}
				put(q, "fuzzy", s(a, "fuzzy"))
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
			name: "get_resource",
			desc: "Read the FULL live spec of one Kubernetes object as YAML (like kubectl get -o yaml, noise stripped): ConfigMap (full data!), Deployment/StatefulSet/DaemonSet, Pod, Service, Ingress, NetworkPolicy, PodDisruptionBudget, HPA, CronJob, Job, PVC, Node, Namespace. Secrets show keys + sha256 hashes only.",
			schema: obj(map[string]any{
				"kind":      str("exact resource kind, e.g. ConfigMap"),
				"namespace": str("namespace ('-' for Node/Namespace)"),
				"name":      str("object name"),
			}, "kind", "name"),
			call: func(a map[string]any) (string, error) {
				return apiPost("/actions", map[string]any{
					"kind": "get_resource", "targetNamespace": orDef(s(a, "namespace"), "-"),
					"targetKind": s(a, "kind"), "targetName": s(a, "name"),
				})
			},
		},
		{
			name: "describe_resource",
			desc: "kubectl-describe-like view of one object: status, conditions and its recent events — the current state and why.",
			schema: obj(map[string]any{
				"kind":      str("exact resource kind"),
				"namespace": str("namespace ('-' for Node/Namespace)"),
				"name":      str("object name"),
			}, "kind", "name"),
			call: func(a map[string]any) (string, error) {
				return apiPost("/actions", map[string]any{
					"kind": "describe_resource", "targetNamespace": orDef(s(a, "namespace"), "-"),
					"targetKind": s(a, "kind"), "targetName": s(a, "name"),
				})
			},
		},
		{
			name: "pod_logs",
			desc: "Read a pod's logs directly from the kubelet — including the PREVIOUS crashed container (previous=true, the way to read a crash reason).",
			schema: obj(map[string]any{
				"namespace": str("namespace"),
				"pod":       str("exact pod name"),
				"container": str("container name (optional)"),
				"previous":  str("'true' to read the previous crashed container"),
				"tailLines": intp("lines from the end (default 200, max 500)"),
			}, "namespace", "pod"),
			call: func(a map[string]any) (string, error) {
				params := map[string]any{}
				if c := s(a, "container"); c != "" {
					params["container"] = c
				}
				if s(a, "previous") == "true" {
					params["previous"] = true
				}
				if n := i(a, "tailLines", 0); n > 0 {
					params["tailLines"] = n
				}
				return apiPost("/actions", map[string]any{
					"kind": "pod_logs", "targetNamespace": s(a, "namespace"),
					"targetKind": "Pod", "targetName": s(a, "pod"), "params": params,
				})
			},
		},
		{
			name: "list_events",
			desc: "All recent events of a namespace sorted newest-first — 'what happened here?'.",
			schema: obj(map[string]any{
				"namespace":    str("namespace (omit for all)"),
				"warningsOnly": str("'true' for Warning events only"),
			}),
			call: func(a map[string]any) (string, error) {
				params := map[string]any{}
				if s(a, "warningsOnly") == "true" {
					params["warningsOnly"] = true
				}
				return apiPost("/actions", map[string]any{
					"kind": "list_events", "targetNamespace": "-",
					"targetKind": "Namespace", "targetName": orDef(s(a, "namespace"), "-"), "params": params,
				})
			},
		},
		{
			name: "get_secret",
			desc: "Inspect a Secret WITHOUT revealing values: keys, value lengths and sha256 hashes. Plaintext never leaves the cluster.",
			schema: obj(map[string]any{
				"namespace": str("namespace"),
				"name":      str("secret name"),
			}, "namespace", "name"),
			call: func(a map[string]any) (string, error) {
				return apiPost("/actions", map[string]any{
					"kind": "get_secret", "targetNamespace": s(a, "namespace"),
					"targetKind": "Secret", "targetName": s(a, "name"),
				})
			},
		},
		{
			name: "helm_releases",
			desc: "List Helm releases (from sh.helm.release.v1 secrets): name, namespace, revision, status.",
			schema: obj(map[string]any{
				"namespace": str("filter to one namespace (omit for all)"),
			}),
			call: func(a map[string]any) (string, error) {
				return apiPost("/actions", map[string]any{
					"kind": "helm_releases", "targetNamespace": "-",
					"targetKind": "Namespace", "targetName": orDef(s(a, "namespace"), "-"),
				})
			},
		},
		{
			name: "run_safe_action",
			desc: "Execute a SAFE, verified action from the catalog: scale, rollout_restart/undo/pause/resume/to_revision, set_image, set_env, set_resources, delete_pod, evict_pod, hpa_set, hpa_toggle, statefulset_partition, cordon/uncordon/drain, node_taint/untaint, patch_configmap, patch_secret, create/delete_configmap, pdb_set, patch_resource (Service/Ingress/NetworkPolicy/PDB merge patch), pvc_expand (grow only), delete_job, exec_readonly (whitelisted argv diagnostic command in a container), cronjob_trigger/suspend/resume, cleanup_pods/jobs, annotate, set_label. Verified on the pod, auto-rolled-back on failure, before-snapshot kept. Only ever offer this after investigating; destructive kinds should be confirmed by a human.",
			schema: obj(map[string]any{
				"kind":            str("action kind from the catalog above"),
				"targetNamespace": str("target namespace (or '-' for Node/Namespace-scoped)"),
				"targetKind":      str("Deployment | StatefulSet | DaemonSet | Pod | Node | Namespace | HorizontalPodAutoscaler | CronJob | Job | ConfigMap | Secret | Service | Ingress | NetworkPolicy | PodDisruptionBudget | PersistentVolumeClaim"),
				"targetName":      str("target object name"),
				"params":          map[string]any{"type": "object", "description": "typed params, e.g. {\"replicas\":3}, {\"image\":\"repo:tag\"}, {\"key\":\"k\",\"value\":\"v\"}, {\"command\":[\"cat\",\"/var/log/app.log\"]}"},
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
