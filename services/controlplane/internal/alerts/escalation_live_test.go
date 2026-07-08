package alerts

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/events"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// escalation_live_test.go — end-to-end Runner-Test des Escalators gegen echtes
// Postgres (RP_LIVE_PG-gated). Prüft, dass Process() fällige Schritte feuert,
// den Zustand vorschiebt und bei erschöpfter Kette next_escalation_at leert.
// Der Provider zeigt auf einen toten Port — Senden schlägt fehl (wird geloggt),
// die Zustandsmaschine läuft trotzdem korrekt weiter.
func TestEscalatorProcessLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the escalator process test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)
	esc := NewEscalator(st, events.NewBroker(pool))

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@run.local", "Run Tester", "")
	org, _ := st.CreateOrg(ctx, "run-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "run-cluster", u.ID)
	prov, _ := st.CreateAlertProvider(ctx, org.ID, "run-webhook", "webhook", json.RawMessage(`{"url":"http://127.0.0.1:59998/none"}`))

	steps := []model.EscalationStep{
		{AfterMinutes: 0, ProviderIDs: []uuid.UUID{prov.ID}},
		{AfterMinutes: 5, ProviderIDs: []uuid.UUID{prov.ID}},
	}
	pol, err := st.CreateEscalationPolicy(ctx, org.ID, "run-policy", steps)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if err := st.SetClusterEscalationPolicy(ctx, cl.ID, &pol.ID); err != nil {
		t.Fatalf("set default: %v", err)
	}

	inc, err := st.CreateIncident(ctx, org.ID, cl.ID, "runner incident", "", "high", &u.ID, u.Email)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}

	// Tick 1: step0 (after 0) fällig → feuert, step→1, next = +5m.
	t1 := time.Now().UTC().Add(time.Minute)
	if n := esc.Process(ctx, t1); n != 1 {
		t.Fatalf("tick1 should fire 1 step, got %d", n)
	}
	got, _ := st.GetIncident(ctx, cl.ID, inc.ID)
	if got.EscalationStep != 1 || got.NextEscalationAt == nil {
		t.Fatalf("after tick1: step=%d next=%v", got.EscalationStep, got.NextEscalationAt)
	}

	// Tick 2 zur selben Zeit: nicht fällig.
	if n := esc.Process(ctx, t1); n != 0 {
		t.Fatalf("tick2 should fire nothing, got %d", n)
	}

	// Tick 3: nach 6 Min → step1 feuert, step→2, next=NULL (Kette Ende).
	if n := esc.Process(ctx, t1.Add(6*time.Minute)); n != 1 {
		t.Fatalf("tick3 should fire step1, got %d", n)
	}
	got, _ = st.GetIncident(ctx, cl.ID, inc.ID)
	if got.EscalationStep != 2 || got.NextEscalationAt != nil {
		t.Fatalf("after tick3: step=%d next=%v (want step=2 next=nil)", got.EscalationStep, got.NextEscalationAt)
	}

	// Timeline enthält genau zwei escalated-Events.
	events, _ := st.ListIncidentEvents(ctx, inc.ID)
	var n int
	for _, e := range events {
		if e.Kind == "escalated" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("expected 2 escalated events, got %d", n)
	}
	t.Logf("escalator runner OK: 2 steps fired, chain terminated")
}
