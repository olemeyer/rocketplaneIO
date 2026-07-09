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

// escalation_live_test.go — end-to-end runner test of the escalator against a real
// Postgres (RP_LIVE_PG-gated). Verifies that Process() fires due steps,
// advances state, and clears next_escalation_at once the chain is exhausted.
// The provider points at a dead port — sending fails (and is logged),
// but the state machine still advances correctly.
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

	// Process fires DB-globally; so we do NOT check the global counter
	// (the shared test DB contains old incidents), but rather the progression
	// of EXACTLY this incident.

	// Tick 1: step0 (after 0) is due → fires, step→1, next = +5m.
	t1 := time.Now().UTC().Add(time.Minute)
	esc.Process(ctx, t1)
	got, _ := st.GetIncident(ctx, cl.ID, inc.ID)
	if got.EscalationStep != 1 || got.NextEscalationAt == nil {
		t.Fatalf("after tick1: step=%d next=%v", got.EscalationStep, got.NextEscalationAt)
	}

	// Tick 2 at the same time: this incident must NOT advance (next is +5m out).
	esc.Process(ctx, t1)
	got, _ = st.GetIncident(ctx, cl.ID, inc.ID)
	if got.EscalationStep != 1 {
		t.Fatalf("tick2 must not advance this incident, step=%d", got.EscalationStep)
	}

	// Tick 3: after 6 min → step1 fires, step→2, next=NULL (end of chain).
	esc.Process(ctx, t1.Add(6*time.Minute))
	got, _ = st.GetIncident(ctx, cl.ID, inc.ID)
	if got.EscalationStep != 2 || got.NextEscalationAt != nil {
		t.Fatalf("after tick3: step=%d next=%v (want step=2 next=nil)", got.EscalationStep, got.NextEscalationAt)
	}

	// Timeline contains exactly two escalated events.
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
