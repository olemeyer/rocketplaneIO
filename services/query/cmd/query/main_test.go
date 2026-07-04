package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rocketplaneio/rocketplane/services/query/internal/promql"
)

func TestQueryHandlerMissingParam(t *testing.T) {
	h := queryHandler(promql.NewEngine(), false)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (missing query param)", rec.Code, http.StatusBadRequest)
	}
}

func TestQueryHandlerNotImplemented(t *testing.T) {
	h := queryHandler(promql.NewEngine(), false)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d (engine stub)", rec.Code, http.StatusNotImplemented)
	}
}
