// Package api wires the HTTP routes (net/http 1.22 mux) to the store and auth
// layers. It implements the API contract in docs/architecture.md §5.
package api

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/alerts"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/config"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/events"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/leader"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/promqlx"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/telemetry"
)

// Server holds the dependencies for the HTTP handlers.
type Server struct {
	cfg    *config.Config
	store  *store.Store
	auth   *auth.Auth
	pool   *pgxpool.Pool
	tele   *telemetry.Store
	broker *events.Broker
	promql *promqlx.Engine

	// shutdownCh beendet alle offenen SSE-Streams (Browser + Agenten) beim
	// Graceful Shutdown — http.Server.Shutdown wartet auf aktive Requests,
	// und Streams enden nie von selbst. Ohne dieses Signal hinge jeder
	// Restart bis zum Shutdown-Timeout (Agenten reconnecten ohnehin).
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// New builds the API server.
func New(cfg *config.Config, st *store.Store, au *auth.Auth, pool *pgxpool.Pool) *Server {
	tele := telemetry.NewStore(cfg.ClickHouseURL, cfg.ClickHouseUser, cfg.ClickHousePassword, cfg.ClickHouseDB)
	return &Server{cfg: cfg, store: st, auth: au, pool: pool, tele: tele, broker: events.NewBroker(pool), promql: promqlx.New(tele), shutdownCh: make(chan struct{})}
}

// NotifyShutdown schließt alle offenen SSE-Streams; für
// http.Server.RegisterOnShutdown (idempotent).
func (s *Server) NotifyShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })
}

// Handler builds the routed http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health.
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Auth (public).
	mux.HandleFunc("GET /api/setup/status", s.auth.SetupStatus)
	mux.HandleFunc("POST /api/setup", s.auth.Setup)
	mux.HandleFunc("POST /auth/login", s.auth.LoginLocal)
	mux.HandleFunc("GET /auth/google/start", s.auth.StartGoogle)
	mux.HandleFunc("GET /auth/google/callback", s.auth.CallbackGoogle)
	mux.HandleFunc("POST /auth/logout", s.auth.Logout)
	mux.HandleFunc("POST /auth/dev/login", s.auth.DevLogin)

	// Session-protected browser API.
	sess := s.auth.WithSession
	mux.Handle("GET /api/me", sess(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/orgs", sess(http.HandlerFunc(s.handleCreateOrg)))
	mux.Handle("POST /api/session/org", sess(http.HandlerFunc(s.handleSetOrg)))

	// ── Tenancy: members, invitations, org lifecycle, audit ──
	mux.Handle("GET /api/orgs/{org}/members", sess(http.HandlerFunc(s.handleListMembers)))
	mux.Handle("PATCH /api/orgs/{org}/members/{user}", sess(http.HandlerFunc(s.handleUpdateMemberRole)))
	mux.Handle("DELETE /api/orgs/{org}/members/{user}", sess(http.HandlerFunc(s.handleRemoveMember)))
	mux.Handle("GET /api/orgs/{org}/invitations", sess(http.HandlerFunc(s.handleListInvitations)))
	mux.Handle("POST /api/orgs/{org}/invitations", sess(http.HandlerFunc(s.handleCreateInvitation)))
	mux.Handle("DELETE /api/orgs/{org}/invitations/{invite}", sess(http.HandlerFunc(s.handleRevokeInvitation)))
	mux.Handle("GET /api/invitations/{token}", sess(http.HandlerFunc(s.handleInvitationPreview)))
	mux.Handle("POST /api/invitations/{token}/accept", sess(http.HandlerFunc(s.handleAcceptInvitation)))
	mux.Handle("PATCH /api/orgs/{org}", sess(http.HandlerFunc(s.handleUpdateOrg)))
	mux.Handle("DELETE /api/orgs/{org}", sess(http.HandlerFunc(s.handleDeleteOrg)))
	mux.Handle("POST /api/orgs/{org}/transfer", sess(http.HandlerFunc(s.handleTransferOwnership)))
	mux.Handle("GET /api/orgs/{org}/audit", sess(http.HandlerFunc(s.handleListAudit)))

	// ── Platform admin (super admin) console ──
	mux.Handle("GET /api/admin/stats", sess(http.HandlerFunc(s.handleAdminStats)))
	mux.Handle("GET /api/admin/users", sess(http.HandlerFunc(s.handleAdminListUsers)))
	mux.Handle("GET /api/admin/orgs", sess(http.HandlerFunc(s.handleAdminListOrgs)))
	mux.Handle("PATCH /api/admin/users/{user}", sess(http.HandlerFunc(s.handleAdminSetUserFlags)))
	mux.Handle("GET /api/orgs/{org}/clusters", sess(http.HandlerFunc(s.handleListClusters)))
	mux.Handle("POST /api/orgs/{org}/clusters", sess(http.HandlerFunc(s.handleCreateCluster)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}", sess(http.HandlerFunc(s.handleGetCluster)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/service-map", sess(http.HandlerFunc(s.handleServiceMap)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/events", sess(http.HandlerFunc(s.handleClusterEvents)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/infra", sess(http.HandlerFunc(s.handleInfra)))
	mux.Handle("PUT /api/orgs/{org}/clusters/{cluster}/workload-icon", sess(http.HandlerFunc(s.handleSetWorkloadIcon)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/metrics/series", sess(http.HandlerFunc(s.handleMetricSeries)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/metrics/definitions", sess(http.HandlerFunc(s.handleListMetricDefs)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/metrics/definitions", sess(http.HandlerFunc(s.handleCreateMetricDef)))
	mux.Handle("PUT /api/orgs/{org}/clusters/{cluster}/metrics/definitions/{def}", sess(http.HandlerFunc(s.handleUpdateMetricDef)))
	mux.Handle("DELETE /api/orgs/{org}/clusters/{cluster}/metrics/definitions/{def}", sess(http.HandlerFunc(s.handleDeleteMetricDef)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/metrics/preview", sess(http.HandlerFunc(s.handleMetricPreview)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/metrics/derived", sess(http.HandlerFunc(s.handleDerivedSeries)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/query_range", sess(http.HandlerFunc(s.handlePromQLRange)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/query", sess(http.HandlerFunc(s.handlePromQLInstant)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/label/{label}/values", sess(http.HandlerFunc(s.handlePromQLLabelValues)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/labels", sess(http.HandlerFunc(s.handlePromQLLabels)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/metadata", sess(http.HandlerFunc(s.handlePromQLMetadata)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/promql/api/v1/series", sess(http.HandlerFunc(s.handlePromQLSeries)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/promql/api/v1/series", sess(http.HandlerFunc(s.handlePromQLSeries)))
	mux.Handle("GET /api/orgs/{org}/alert-providers", sess(http.HandlerFunc(s.handleListProviders)))
	mux.Handle("POST /api/orgs/{org}/alert-providers", sess(http.HandlerFunc(s.handleCreateProvider)))
	mux.Handle("DELETE /api/orgs/{org}/alert-providers/{provider}", sess(http.HandlerFunc(s.handleDeleteProvider)))
	mux.Handle("POST /api/orgs/{org}/alert-providers/{provider}/test", sess(http.HandlerFunc(s.handleTestProvider)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/alert-rules", sess(http.HandlerFunc(s.handleListRules)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/alert-rules", sess(http.HandlerFunc(s.handleCreateRule)))
	mux.Handle("PUT /api/orgs/{org}/clusters/{cluster}/alert-rules/{rule}", sess(http.HandlerFunc(s.handleUpdateRule)))
	mux.Handle("DELETE /api/orgs/{org}/clusters/{cluster}/alert-rules/{rule}", sess(http.HandlerFunc(s.handleDeleteRule)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/alert-events", sess(http.HandlerFunc(s.handleListAlertEvents)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/alert-rules/{rule}/mute", sess(http.HandlerFunc(s.handleMuteRule)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/alert-rules/{rule}/series", sess(http.HandlerFunc(s.handleRuleSeries)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/logs", sess(http.HandlerFunc(s.handleQueryLogs)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/traces", sess(http.HandlerFunc(s.handleQueryTraces)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/traces/{traceId}", sess(http.HandlerFunc(s.handleTraceDetail)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/span-stats", sess(http.HandlerFunc(s.handleSpanStats)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/reconnect", sess(http.HandlerFunc(s.handleReconnect)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/copilot/chat", sess(http.HandlerFunc(s.handleCopilotChat)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/copilot/action", sess(http.HandlerFunc(s.handleCopilotActionDecision)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/copilot/chats", sess(http.HandlerFunc(s.handleListCopilotChats)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/copilot/chats/{chat}", sess(http.HandlerFunc(s.handleGetCopilotChat)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/copilot/chats/{chat}/graph", sess(http.HandlerFunc(s.handleGetInvestigationGraph)))
	mux.Handle("PUT /api/orgs/{org}/clusters/{cluster}/copilot/chats/{chat}", sess(http.HandlerFunc(s.handleUpsertCopilotChat)))
	mux.Handle("DELETE /api/orgs/{org}/clusters/{cluster}/copilot/chats/{chat}", sess(http.HandlerFunc(s.handleDeleteCopilotChat)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/actions", sess(http.HandlerFunc(s.handleCreateAction)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/actions", sess(http.HandlerFunc(s.handleListActions)))
	mux.Handle("POST /api/orgs/{org}/clusters/{cluster}/actions/{action}/cancel", sess(http.HandlerFunc(s.handleCancelAction)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/workload-pods", sess(http.HandlerFunc(s.handleWorkloadPods)))
	mux.Handle("GET /api/orgs/{org}/clusters/{cluster}/inventory", sess(http.HandlerFunc(s.handleListInventory)))
	mux.Handle("GET /api/orgs/{org}/action-definitions", sess(http.HandlerFunc(s.handleListActionDefs)))
	mux.Handle("POST /api/orgs/{org}/action-definitions", sess(http.HandlerFunc(s.handleCreateActionDef)))
	mux.Handle("PUT /api/orgs/{org}/action-definitions/{def}", sess(http.HandlerFunc(s.handleUpdateActionDef)))
	mux.Handle("DELETE /api/orgs/{org}/action-definitions/{def}", sess(http.HandlerFunc(s.handleDeleteActionDef)))

	// Agent API (outbound-only).
	agent := s.auth.WithAgent
	// Install-Manifest (public): der Enroll-Token in der Query ist die Autorisierung —
	// das gerenderte Manifest enthält nur genau diesen (dem Aufrufer bereits bekannten)
	// Token. Erlaubt den Ein-Befehl-`kubectl apply -f <url>`-Install aus der UI.
	mux.HandleFunc("GET /api/agent/manifest", s.handleAgentManifest)
	mux.HandleFunc("POST /api/agent/enroll", s.handleEnroll)
	mux.Handle("POST /api/agent/heartbeat", agent(http.HandlerFunc(s.handleHeartbeat)))
	mux.Handle("POST /api/agent/namespaces", agent(http.HandlerFunc(s.handleNamespaces)))
	mux.Handle("POST /api/agent/topology", agent(http.HandlerFunc(s.handleTopology)))
	mux.Handle("POST /api/agent/logs", agent(http.HandlerFunc(s.handleAgentLogs)))
	mux.Handle("GET /api/agent/events", agent(http.HandlerFunc(s.handleAgentEvents)))
	mux.Handle("GET /api/agent/actions", agent(http.HandlerFunc(s.handleAgentActions)))
	mux.Handle("POST /api/agent/actions/{action}/result", agent(http.HandlerFunc(s.handleAgentActionResult)))
	mux.Handle("POST /api/agent/inventory", agent(http.HandlerFunc(s.handleAgentInventory)))

	return logRequests(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 3*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// logRequests is a minimal access-log middleware.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush reicht ans Original durch — ohne das wäre SSE hinter der Log-
// Middleware unmöglich („streaming unsupported").
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// StartBackground startet Schema-Sicherung + Alert-Evaluator (bis ctx endet).
func (s *Server) StartBackground(ctx context.Context) {
	// Configured emails become platform admins (RP_PLATFORM_ADMINS).
	s.ensurePlatformAdmins(ctx)
	if err := s.tele.EnsureInfraSchema(ctx); err != nil {
		log.Printf("infra metrics schema: %v", err)
	}
	// Container-Logs: die CP besitzt otel_logs (eigenes Schema mit ClusterId +
	// Workload-Spalten). Der OTel-Collector darf otel_logs NICHT anlegen.
	if err := s.tele.EnsureLogsSchema(ctx); err != nil {
		log.Printf("logs schema: %v", err)
	}
	// Skip-Indizes für Regex-/Token-/Fuzzy-Suche auf Body (idempotent).
	if err := s.tele.EnsureLogsIndexes(ctx); err != nil {
		log.Printf("logs indexes: %v", err)
	}

	// Cross-Replica-Eventverteilung: der LISTEN-Loop stellt NOTIFYs an die
	// lokalen SSE-Subscriber zu (Publish geht bei jeder Replica über NOTIFY).
	go s.broker.Run(ctx)

	// Singleton-Arbeit läuft nur auf dem Leader (Postgres-Advisory-Lock) —
	// mit >1 Replica würden Alerts sonst doppelt feuern und der Reaper doppelt
	// aufräumen. Verliert der Leader seine DB-Session, übernimmt eine andere.
	go leader.Run(ctx, s.pool, "alerts+reaper", leaderLockKey, func(leadCtx context.Context) {
		go alerts.New(s.store, s.tele, s.broker, s.promql).Run(leadCtx)

		// Action-Reaper: garantiert, dass Cancel/Runs nie ewig hängen (auch wenn
		// der Agent tot ist). Läuft leichtgewichtig alle 30s.
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-leadCtx.Done():
				return
			case <-t.C:
				if n, err := s.store.ReapActions(leadCtx); err != nil {
					log.Printf("action reaper: %v", err)
				} else if n > 0 {
					log.Printf("action reaper: finalized %d stuck action(s)", n)
				}
			}
		}
	})
}

// leaderLockKey ist der clusterweit feste Advisory-Lock-Schlüssel für die
// Singleton-Hintergrundarbeit der Control-Plane (zufällig gewählt, stabil).
const leaderLockKey int64 = 0x726f636b_65740001
