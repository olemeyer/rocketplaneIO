package seed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

func fixedClock() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

func newSeed() *Store { return New(WithNow(fixedClock)) }

func TestDeterministic(t *testing.T) {
	a, b := newSeed(), newSeed()
	if len(a.summaries) != len(b.summaries) || len(a.summaries) == 0 {
		t.Fatalf("summary count mismatch: %d vs %d", len(a.summaries), len(b.summaries))
	}
	for i := range a.summaries {
		if a.summaries[i] != b.summaries[i] {
			t.Fatalf("summary %d differs between instances", i)
		}
	}
}

func TestReferenceTrace(t *testing.T) {
	d, err := newSeed().Trace(context.Background(), refTraceID)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if len(d.Spans) != 7 {
		t.Fatalf("spans = %d, want 7", len(d.Spans))
	}
	if d.Spans[0].Name != "POST /checkout" || d.Spans[0].Service != "checkout-api" {
		t.Errorf("root span = %+v", d.Spans[0])
	}
	if d.ErrorCount != 3 {
		t.Errorf("errorCount = %d, want 3", d.ErrorCount)
	}
	// stripe.charge hängt an payment.charge (Tiefe 2).
	if d.Spans[4].Name != "stripe.charge" || d.Spans[4].Depth != 2 {
		t.Errorf("expected stripe.charge at depth 2, got %+v", d.Spans[4])
	}
}

func TestTraceNotFound(t *testing.T) {
	_, err := newSeed().Trace(context.Background(), "00000000000000000000000000000000")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestServicesStatusAndSort(t *testing.T) {
	res, err := newSeed().Services(context.Background(), store.ServicesQuery{})
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	if len(res.Services) != 4 {
		t.Fatalf("services = %d, want 4", len(res.Services))
	}
	// Default-Sort "error": checkout-api (höchste error-ratio) zuerst.
	if res.Services[0].Name != "checkout-api" {
		t.Errorf("first service = %s, want checkout-api", res.Services[0].Name)
	}
	byName := map[string]model.Service{}
	for _, s := range res.Services {
		byName[s.Name] = s
	}
	if byName["checkout-api"].Status != model.HealthCritical {
		t.Errorf("checkout-api status = %s, want critical", byName["checkout-api"].Status)
	}
	if byName["inventory"].Status != model.HealthHealthy {
		t.Errorf("inventory status = %s, want healthy", byName["inventory"].Status)
	}
	if res.Window.Start >= res.Window.End {
		t.Errorf("invalid window: %+v", res.Window)
	}
}

func TestServiceDetail(t *testing.T) {
	s := newSeed()
	ctx := context.Background()

	d, err := s.Service(ctx, store.ServiceQuery{Name: "checkout-api"})
	if err != nil {
		t.Fatalf("Service: %v", err)
	}
	if d.Name != "checkout-api" || d.Status != model.HealthCritical {
		t.Errorf("summary: name=%s status=%s", d.Name, d.Status)
	}
	if len(d.P95Series) == 0 || len(d.RateSeries) == 0 {
		t.Error("expected non-empty timeseries")
	}
	if len(d.Operations) == 0 {
		t.Fatal("expected operations")
	}
	// Die Root-Operation POST /checkout muss vorkommen.
	foundRoot := false
	for _, op := range d.Operations {
		if op.Name == "POST /checkout" {
			foundRoot = true
		}
	}
	if !foundRoot {
		t.Errorf("root op missing in %+v", d.Operations)
	}
	// Dependencies: checkout-api ruft u.a. payment-gateway.
	foundDep := false
	for _, dep := range d.Dependencies {
		if dep.Service == "payment-gateway" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Errorf("expected payment-gateway dependency, got %+v", d.Dependencies)
	}

	if _, err := s.Service(ctx, store.ServiceQuery{Name: "does-not-exist"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown service err = %v, want ErrNotFound", err)
	}
}

func TestServiceMap(t *testing.T) {
	m, err := newSeed().ServiceMap(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("ServiceMap: %v", err)
	}
	if len(m.Nodes) < 4 {
		t.Fatalf("nodes = %d, want >= 4", len(m.Nodes))
	}
	if len(m.Edges) == 0 {
		t.Fatal("expected edges")
	}
	// Kante checkout-api -> payment-gateway muss existieren.
	found := false
	for _, e := range m.Edges {
		if e.From == "checkout-api" && e.To == "payment-gateway" {
			found = true
			if e.CallCount == 0 {
				t.Error("edge has zero calls")
			}
		}
		if e.From == e.To {
			t.Errorf("self-edge: %+v", e)
		}
	}
	if !found {
		t.Error("expected checkout-api -> payment-gateway edge")
	}
}

func TestLogsCorrelateWithTraces(t *testing.T) {
	s := newSeed()
	ctx := context.Background()

	// Alle Logs: es gibt welche.
	all, err := s.Logs(ctx, store.LogsQuery{})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(all.Logs) == 0 {
		t.Fatal("expected seed logs")
	}

	// Logs des Referenz-Trace (korreliert über TraceId): mind. ein Error-Log.
	byTrace, err := s.Logs(ctx, store.LogsQuery{TraceID: refTraceID})
	if err != nil {
		t.Fatalf("Logs(trace): %v", err)
	}
	if len(byTrace.Logs) == 0 {
		t.Fatal("expected correlated logs for reference trace")
	}
	hasError := false
	for _, l := range byTrace.Logs {
		if l.TraceID != refTraceID {
			t.Fatalf("correlated log has wrong trace: %q", l.TraceID)
		}
		if l.Severity == "ERROR" {
			hasError = true
		}
	}
	if !hasError {
		t.Error("reference trace is an error trace -> expected an ERROR log")
	}

	// Severity-Filter: nur ERROR (>=17) liefert weniger als alle.
	errOnly, _ := s.Logs(ctx, store.LogsQuery{MinSeverity: 17})
	if len(errOnly.Logs) == 0 || len(errOnly.Logs) >= len(all.Logs) {
		t.Errorf("severity filter unexpected: %d of %d", len(errOnly.Logs), len(all.Logs))
	}
	for _, l := range errOnly.Logs {
		if l.SeverityNumber < 17 {
			t.Fatalf("severity filter leaked: %d", l.SeverityNumber)
		}
	}
}

func TestTracesFilterAndPagination(t *testing.T) {
	s := newSeed()
	// Filter auf checkout-api.
	list, err := s.Traces(context.Background(), store.TracesQuery{Service: "checkout-api"})
	if err != nil {
		t.Fatalf("Traces: %v", err)
	}
	if len(list.Traces) == 0 {
		t.Fatal("expected checkout-api traces")
	}
	for _, tr := range list.Traces {
		if tr.RootService != "checkout-api" {
			t.Fatalf("unexpected service %s", tr.RootService)
		}
	}

	// Pagination: limit 5 -> nextCursor gesetzt, Folgeseite disjunkt.
	page1, _ := s.Traces(context.Background(), store.TracesQuery{Limit: 5})
	if len(page1.Traces) != 5 || page1.NextCursor == "" {
		t.Fatalf("page1: len=%d cursor=%q", len(page1.Traces), page1.NextCursor)
	}
	page2, _ := s.Traces(context.Background(), store.TracesQuery{Limit: 5, Cursor: page1.NextCursor})
	if len(page2.Traces) == 0 || page2.Traces[0].TraceID == page1.Traces[0].TraceID {
		t.Fatalf("page2 not advanced")
	}
}
