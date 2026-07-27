package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcp_tools_read.go — read-only MCP tools. Always allowed; logged into the
// caller's transaction when one is open. Each tool dispatches in-process
// against the existing HTTP handlers (mcpInternalCall), so validation, RBAC
// and audit are byte-identical to the browser API.

func addQ(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func (s *Server) registerMCPReadTools(srv *mcp.Server) {
	type logsIn struct {
		Namespace string `json:"namespace,omitempty" jsonschema:"namespace filter (optional)"`
		Workload  string `json:"workload,omitempty" jsonschema:"workload/service name filter (optional)"`
		Search    string `json:"search,omitempty" jsonschema:"case-insensitive substring in the log body (optional)"`
		Regex     string `json:"regex,omitempty" jsonschema:"RE2 regex on the log body, e.g. (?i)oom|exit code 137 (optional)"`
		Exclude   string `json:"exclude,omitempty" jsonschema:"comma-separated substrings that must NOT appear (optional)"`
		Fuzzy     string `json:"fuzzy,omitempty" jsonschema:"typo-tolerant search term (optional)"`
		Since     string `json:"since,omitempty" jsonschema:"lookback window, e.g. 15m, 1h, 6h (default 15m)"`
		Limit     int    `json:"limit,omitempty" jsonschema:"max lines (default 50)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_logs",
		Description: "Read recent container logs (severity, workload, body). Use to investigate errors. " +
			"Filter by namespace/workload; body matching supports substring (search), RE2 regex (regex, " +
			"prefix (?i) for case-insensitive), NOT-substrings (exclude) and a typo-tolerant fuzzy term.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		q := url.Values{}
		addQ(q, "namespace", in.Namespace)
		addQ(q, "workload", in.Workload)
		addQ(q, "search", in.Search)
		addQ(q, "regex", in.Regex)
		for _, ex := range strings.Split(in.Exclude, ",") {
			if ex = strings.TrimSpace(ex); ex != "" {
				q.Add("exclude", ex)
			}
		}
		addQ(q, "fuzzy", in.Fuzzy)
		since := in.Since
		if since == "" {
			since = "15m"
		}
		q.Set("since", since)
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		q.Set("limit", strconv.Itoa(limit))
		out, err := s.mcpInternalCall(ctx, sc, http.MethodGet, "/logs", q, nil)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLogRead(ctx, sc, "query_logs", in)
		return mcpText(out), nil, nil
	})

	// Filters exist because the whole map does not fit: a 600-pod cluster
	// serialises to well over the response cap, and an agent asking "what is
	// unhealthy" wants a handful of nodes, not the inventory.
	type mapIn struct {
		Namespace string `json:"namespace,omitempty" jsonschema:"only workloads in this namespace"`
		Health    string `json:"health,omitempty" jsonschema:"only workloads with this health: healthy, degraded, critical or unknown"`
	}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_service_map",
		Description: "Read the live service map: workloads, health (healthy/degraded/critical/unknown), and " +
			"flow edges between them. The topology at a glance. Filter by namespace or health — the " +
			"unfiltered map of a real cluster exceeds the response cap.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in mapIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInternalCallRaw(ctx, sc, http.MethodGet, "/service-map", nil, nil)
		if err != nil {
			return nil, nil, err
		}
		out = filterServiceMap(out, in.Namespace, in.Health)
		s.mcpLogRead(ctx, sc, "get_service_map", in)
		return mcpText(capResult(out)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_infra",
		Description: "Read cluster infrastructure: nodes (CPU/memory/disk pressure, ready/unschedulable) " +
			"and persistent volume claims.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		out, err := s.mcpInternalCall(ctx, sc, http.MethodGet, "/infra", nil, nil)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLogRead(ctx, sc, "get_infra", nil)
		return mcpText(out), nil, nil
	})

	type metricsIn struct {
		Query    string `json:"query" jsonschema:"PromQL range expression, e.g. sum by (service_name) (rate(http_server_request_duration_count[5m]))"`
		SinceMin int    `json:"sinceMin,omitempty" jsonschema:"lookback in minutes (default 15)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_metrics",
		Description: "Run a PromQL range query against the embedded engine (RED metrics from eBPF spans, " +
			"log rates, node/workload infra metrics). Returns a time-series matrix.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in metricsIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		sinceMin := in.SinceMin
		if sinceMin <= 0 {
			sinceMin = 15
		}
		end := time.Now().Unix()
		start := end - int64(sinceMin)*60
		step := (end - start) / 120
		if step < 5 {
			step = 5
		}
		q := url.Values{}
		q.Set("query", in.Query)
		q.Set("start", strconv.FormatInt(start, 10))
		q.Set("end", strconv.FormatInt(end, 10))
		q.Set("step", strconv.FormatInt(step, 10))
		out, err := s.mcpInternalCall(ctx, sc, http.MethodGet, "/promql/api/v1/query_range", q, nil)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLogRead(ctx, sc, "query_metrics", in)
		return mcpText(out), nil, nil
	})

	type tracesIn struct {
		Service   string `json:"service,omitempty" jsonschema:"service name filter (optional)"`
		Namespace string `json:"namespace,omitempty" jsonschema:"namespace filter (optional)"`
		Limit     int    `json:"limit,omitempty" jsonschema:"max traces (default 25)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "query_traces",
		Description: "Read recent distributed traces (service, span, duration, status). Filter by " +
			"service/namespace. Use to find slow or failing requests.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in tracesIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		q := url.Values{}
		addQ(q, "service", in.Service)
		addQ(q, "namespace", in.Namespace)
		limit := in.Limit
		if limit <= 0 {
			limit = 25
		}
		q.Set("limit", strconv.Itoa(limit))
		out, err := s.mcpInternalCall(ctx, sc, http.MethodGet, "/traces", q, nil)
		if err != nil {
			return nil, nil, err
		}
		s.mcpLogRead(ctx, sc, "query_traces", in)
		return mcpText(out), nil, nil
	})

	type listActionsIn struct {
		Namespace string `json:"namespace,omitempty" jsonschema:"namespace filter (optional)"`
		Target    string `json:"target,omitempty" jsonschema:"target name filter (optional)"`
		Limit     int    `json:"limit,omitempty" jsonschema:"max runs (default 20)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_actions",
		Description: "List recent safe-action runs on this cluster with their status (the audit trail of " +
			"what was done).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listActionsIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		q := url.Values{}
		addQ(q, "namespace", in.Namespace)
		addQ(q, "target", in.Target)
		limit := in.Limit
		if limit <= 0 {
			limit = 20
		}
		q.Set("limit", strconv.Itoa(limit))
		out, err := s.mcpInternalCall(ctx, sc, http.MethodGet, "/actions", q, nil)
		if err != nil {
			return nil, nil, err
		}
		return mcpText(out), nil, nil
	})

}

// filterServiceMap narrows a /service-map payload to the requested namespace
// and/or health before it reaches the caller. Edges follow their nodes: an edge
// whose endpoints were filtered out would dangle.
//
// Returns the input unchanged when nothing is requested or the payload does not
// parse — a filter must never be the reason a read fails.
func filterServiceMap(payload, namespace, health string) string {
	if namespace == "" && health == "" {
		return payload
	}
	var m struct {
		Namespaces []string         `json:"namespaces"`
		Nodes      []map[string]any `json:"nodes"`
		Edges      []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}
	keep := map[string]bool{}
	nodes := make([]map[string]any, 0, len(m.Nodes))
	for _, n := range m.Nodes {
		if namespace != "" && n["namespace"] != namespace {
			continue
		}
		if health != "" && n["health"] != health {
			continue
		}
		if id, ok := n["id"].(string); ok {
			keep[id] = true
		}
		nodes = append(nodes, n)
	}
	edges := make([]map[string]any, 0, len(m.Edges))
	for _, e := range m.Edges {
		from, _ := e["from"].(string)
		to, _ := e["to"].(string)
		if keep[from] && keep[to] {
			edges = append(edges, e)
		}
	}
	out, err := json.Marshal(map[string]any{
		"namespaces": m.Namespaces,
		"nodes":      nodes,
		"edges":      edges,
		"filtered":   map[string]string{"namespace": namespace, "health": health},
		"matched":    len(nodes),
		"totalNodes": len(m.Nodes),
	})
	if err != nil {
		return payload
	}
	return string(out)
}
