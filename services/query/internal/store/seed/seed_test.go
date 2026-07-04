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
