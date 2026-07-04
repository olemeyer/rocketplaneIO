package otlp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler() http.Handler {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil))).Mux()
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if body["status"] != "ok" || body["service"] != "ingest" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestSignalEndpointsNotImplemented(t *testing.T) {
	handler := newTestHandler()
	for _, path := range []string{"/v1/traces", "/v1/metrics", "/v1/logs"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("POST %s = %d, want %d", path, rec.Code, http.StatusNotImplemented)
		}
	}
}

func TestSignalEndpointRejectsWrongMethod(t *testing.T) {
	// Nur POST ist registriert; ein GET muss vom Go-1.22-Mux mit 405 quittiert werden.
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/traces", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /v1/traces = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
