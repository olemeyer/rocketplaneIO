package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/promql"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store/storetest"
)

const allowedOrigin = "http://localhost:4173"
const validTraceID = "abcdef0123456789abcdef0123456789"

func newTestServer(f *storetest.Fake) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(f, promql.NewEngine(), log, []string{allowedOrigin}).Handler()
}

func do(h http.Handler, method, target, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthAndReady(t *testing.T) {
	f := &storetest.Fake{}
	h := newTestServer(f)

	if rec := do(h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("/healthz = %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/readyz", ""); rec.Code != http.StatusOK {
		t.Errorf("/readyz = %d", rec.Code)
	}
	f.PingErr = store.ErrUnsupported
	if rec := do(h, http.MethodGet, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz (down) = %d, want 503", rec.Code)
	}
}

func TestServicesEnvelope(t *testing.T) {
	f := &storetest.Fake{ServicesResult: model.ServicesResult{
		Window:   model.Window{Start: 1, End: 2, Step: 1},
		Services: []model.Service{{Name: "checkout-api", Status: model.HealthCritical}},
	}}
	rec := do(newTestServer(f), http.MethodGet, "/api/v1/services?sort=rate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var env struct {
		Status string               `json:"status"`
		Data   model.ServicesResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Status != "success" || len(env.Data.Services) != 1 || env.Data.Services[0].Name != "checkout-api" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if f.LastServicesQuery.Sort != "rate" {
		t.Errorf("sort not forwarded: %q", f.LastServicesQuery.Sort)
	}
}

func TestTraceBadRequestAndNotFound(t *testing.T) {
	// Ungültige traceId -> 400 bad_data.
	if rec := do(newTestServer(&storetest.Fake{}), http.MethodGet, "/api/v1/traces/xyz", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id = %d, want 400", rec.Code)
	}
	// Gültige, aber unbekannte traceId -> 404 not_found.
	f := &storetest.Fake{TraceErr: store.ErrNotFound}
	rec := do(newTestServer(f), http.MethodGet, "/api/v1/traces/"+validTraceID, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
	if f.LastTraceID != validTraceID {
		t.Errorf("traceID not forwarded: %q", f.LastTraceID)
	}
}

func TestQueryNotImplemented(t *testing.T) {
	h := newTestServer(&storetest.Fake{})
	if rec := do(h, http.MethodGet, "/api/v1/query", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("missing query = %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/api/v1/query?query=up", ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("query = %d, want 501", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := do(newTestServer(&storetest.Fake{}), http.MethodPost, "/api/v1/services", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST services = %d, want 405", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	h := newTestServer(&storetest.Fake{})

	// Preflight von erlaubtem Origin -> 204 + Header.
	rec := do(h, http.MethodOptions, "/api/v1/services", allowedOrigin)
	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Errorf("ACAO = %q", got)
	}

	// GET von nicht erlaubtem Origin -> kein ACAO-Header.
	rec = do(h, http.MethodGet, "/healthz", "http://evil.test")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO for disallowed origin = %q, want empty", got)
	}
}
