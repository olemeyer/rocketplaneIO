package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// mcp_server.go — the remote MCP interface (Streamable HTTP) for external AI
// agents (Claude Code, Claude Desktop, Cursor, …).
//
// Mounted at /mcp/orgs/{org}/clusters/{cluster}, auth = API bearer token only
// (rp_…). The server runs STATELESS: every request re-authenticates from the
// Authorization header and all real state (transactions, actions, snapshots)
// lives in Postgres — no sticky sessions across CP replicas.
//
// Contract for agents: reads are always allowed (and logged when a transaction
// is open); anything mutating requires an open transaction (begin_transaction)
// and runs over the snapshot substrate, so cancel/expiry rolls everything back.

// mcpScope is the per-request authentication scope, resolved by handleMCP and
// carried through the SDK to the tool handlers via context.
type mcpScope struct {
	orgID     uuid.UUID
	clusterID uuid.UUID
	role      string
	tokenID   *uuid.UUID
	userID    uuid.UUID
	userEmail string
	bearer    string // original bearer token, reused for internal API dispatch
}

type mcpScopeKeyType struct{}

var mcpScopeKey mcpScopeKeyType

func mcpScopeFrom(ctx context.Context) (*mcpScope, error) {
	sc, ok := ctx.Value(mcpScopeKey).(*mcpScope)
	if !ok || sc == nil {
		return nil, errors.New("internal: mcp scope missing from context")
	}
	return sc, nil
}

// mcpServer builds the tool registry once; handlers are stateless and read
// their scope from the request context.
func (s *Server) mcpServer() *mcp.Server {
	s.mcpOnce.Do(func() {
		srv := mcp.NewServer(&mcp.Implementation{
			Name:    "rocketplaneio",
			Title:   "rocketplaneIO",
			Version: "1.0.0",
		}, &mcp.ServerOptions{
			Instructions: "rocketplaneIO cluster access. Reads (query_logs, query_traces, query_metrics, " +
				"get_service_map, get_infra, k8s_get, k8s_list) work immediately. ANY mutation (k8s_patch, " +
				"k8s_apply, k8s_delete, k8s_exec, run_script — arbitrary resources incl. CRDs) requires an " +
				"open transaction: call begin_transaction first — every change is snapshotted durably, and " +
				"cancel_transaction (or the deadline) rolls ALL changes back. commit_transaction keeps them. " +
				"rocketplaneIO classifies each operation (read/reversible/disruptive/destructive); gated " +
				"levels pause as awaiting_approval until a human approves them in the rocketplaneIO UI — " +
				"poll with get_transaction or wait_approval.",
		})
		s.registerMCPReadTools(srv)
		s.registerMCPTxnTools(srv)
		s.registerMCPActionTools(srv)
		s.mcpSrv = srv
	})
	return s.mcpSrv
}

// mcpHTTPHandler wires auth + scope resolution around the SDK's streamable
// handler. Registered method-less so POST/GET/DELETE all route to the SDK.
func (s *Server) mcpHTTPHandler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcpServer() },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	scoped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.resolveMCPScope(w, r)
		if !ok {
			return
		}
		ctx := context.WithValue(r.Context(), mcpScopeKey, sc)
		streamable.ServeHTTP(w, r.WithContext(ctx))
	})
	return s.auth.WithAPIToken(scoped)
}

// resolveMCPScope enforces the token↔org↔cluster scoping for the MCP mount.
func (s *Server) resolveMCPScope(w http.ResponseWriter, r *http.Request) (*mcpScope, bool) {
	tp, ok := auth.TokenFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "an api token is required")
		return nil, false
	}
	user, _ := auth.UserFrom(r.Context())
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}

	// {org} may be a uuid or a slug — same grammar as the browser API.
	param := r.PathValue("org")
	var orgID uuid.UUID
	if id, perr := uuid.Parse(param); perr == nil {
		orgID = id
	} else if o, gerr := s.store.GetOrgBySlug(r.Context(), param); gerr == nil {
		orgID = o.ID
	} else {
		writeErr(w, http.StatusNotFound, "org not found")
		return nil, false
	}
	if tp.OrgID != orgID {
		writeErr(w, http.StatusForbidden, "token is not scoped to this org")
		return nil, false
	}
	clusterID, err := uuid.Parse(r.PathValue("cluster"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid cluster id")
		return nil, false
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return nil, false
	}
	tokenID := tp.TokenID
	return &mcpScope{
		orgID:     orgID,
		clusterID: clusterID,
		role:      tp.Role,
		tokenID:   &tokenID,
		userID:    user.ID,
		userEmail: user.Email,
		bearer:    bearerFromRequest(r),
	}, true
}

func bearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// ── Internal dispatch ────────────────────────────────────────────────────────
// Read tools reuse the existing HTTP handlers in-process (no network, no
// cookies): the request runs through the same routing, validation, RBAC and
// audit as the browser UI, so the MCP surface can never drift from the API.

const mcpResultCap = 60 * 1024 // keep tool results model-friendly

// mcpInternalCall dispatches an in-process request against the API mux using
// the caller's bearer token and caps the result. Callers that post-process the
// payload (filtering, projection) must use mcpInternalCallRaw and cap the
// result themselves — capping first would hand them the truncation envelope
// instead of the data.
func (s *Server) mcpInternalCall(ctx context.Context, sc *mcpScope, method, path string, q url.Values, body any) (string, error) {
	out, err := s.mcpInternalCallRaw(ctx, sc, method, path, q, body)
	if err != nil {
		return "", err
	}
	return capResult(out), nil
}

// mcpInternalCallRaw is the same dispatch without the response cap.
func (s *Server) mcpInternalCallRaw(ctx context.Context, sc *mcpScope, method, path string, q url.Values, body any) (string, error) {
	if s.apiMux == nil {
		return "", errors.New("internal: api mux not initialised")
	}
	u := fmt.Sprintf("/api/orgs/%s/clusters/%s%s", sc.orgID, sc.clusterID, path)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var reader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = strings.NewReader(string(b))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(ctx, method, u, reader)
	req.Header.Set("Authorization", "Bearer "+sc.bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.apiMux.ServeHTTP(rec, req)
	out := rec.Body.String()
	if rec.Code >= 300 {
		msg := strings.TrimSpace(out)
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return "", fmt.Errorf("api %d: %s", rec.Code, msg)
	}
	return out, nil
}

// capResult bounds a tool result without handing the caller broken data.
// Slicing bytes off a JSON document leaves an unparseable fragment — worse
// than an error, because a model will try to interpret it. When the payload is
// JSON we return a valid envelope instead, with the prefix as an escaped
// string, so the caller can always parse the response and see what happened.
func capResult(out string) string {
	if len(out) <= mcpResultCap {
		return out
	}
	if json.Valid([]byte(out)) {
		env, err := json.Marshal(map[string]any{
			"truncated":  true,
			"totalBytes": len(out),
			"hint":       "result exceeds the response cap — narrow the query (namespace/limit/minutes/health) and call again",
			"preview":    out[:mcpResultCap],
		})
		if err == nil {
			return string(env)
		}
	}
	return out[:mcpResultCap] + fmt.Sprintf("\n… truncated (%d bytes total) — narrow the query (namespace/limit/minutes)", len(out))
}

// mcpLogRead best-effort logs a read tool call into the token's open
// transaction (if any) — the documented "everything under the transaction is
// recorded" guarantee. Reads without an open transaction are not persisted.
func (s *Server) mcpLogRead(ctx context.Context, sc *mcpScope, tool string, args any) {
	if sc.tokenID == nil {
		return
	}
	txn, err := s.store.GetOpenMCPTransactionForToken(ctx, sc.clusterID, *sc.tokenID)
	if err != nil {
		return
	}
	_ = s.store.AppendTxnEvent(ctx, txn.ID, "read", tool, nil, args)
	s.broker.Publish(sc.clusterID, "transactions", 0)
}

// mcpText wraps a string as a text tool result.
func mcpText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// mcpJSON marshals v as an indented JSON text result.
func mcpJSON(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcpText(fmt.Sprintf("marshal error: %v", err))
	}
	return mcpText(string(b))
}

// requireOpenTxn loads the caller's open transaction or returns a structured
// error telling the agent to begin one.
func (s *Server) requireOpenTxn(ctx context.Context, sc *mcpScope) (*model.MCPTransaction, error) {
	if sc.tokenID == nil {
		return nil, errors.New("no transaction: this token has no open transaction — call begin_transaction first")
	}
	txn, err := s.store.GetOpenMCPTransactionForToken(ctx, sc.clusterID, *sc.tokenID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errors.New("no transaction: call begin_transaction first — mutations only run inside a transaction (they are snapshotted and roll back on cancel/expiry)")
	}
	if err != nil {
		return nil, err
	}
	return txn, nil
}
