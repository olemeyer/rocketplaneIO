package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// action_events_live_test.go verifies the Safe Actions v2 durable step/event
// spine (RP_LIVE_PG-gated): AppendEvents assigns a globally monotonic seq,
// ListGroupEvents resumes from a seq, and UpsertStep is idempotent on (action,seq).
func TestActionEventsLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the action-events test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@ev.local", "Ev", "")
	org, _ := st.CreateOrg(ctx, "ev-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "ev-cluster", u.ID)
	g, _ := st.CreateGroup(ctx, cl.ID, u.ID, "manual_batch", "trace", nil, "", "")
	a, _ := st.AppendAction(ctx, g.ID, cl.ID, u.ID, "scale", "shop", "Deployment", "checkout", "builtin", json.RawMessage(`{}`))

	// UpsertStep idempotent: same (action,seq) twice → one row, status updated.
	s0 := model.ActionStep{ActionID: a.ID, GroupID: g.ID, Seq: 0, Name: "trigger", Kind: "mutate", EffectClass: "reversible", Status: "running"}
	if err := st.UpsertStep(ctx, s0); err != nil {
		t.Fatalf("UpsertStep: %v", err)
	}
	s0.Status = "ok"
	if err := st.UpsertStep(ctx, s0); err != nil {
		t.Fatalf("UpsertStep (2nd): %v", err)
	}
	steps, _ := st.ListActionSteps(ctx, a.ID)
	if len(steps) != 1 || steps[0].Status != "ok" {
		t.Fatalf("UpsertStep not idempotent/updated: %+v", steps)
	}

	// AppendEvents: monotonic seq; ListGroupEvents resumes from a seq.
	max1, err := st.AppendEvents(ctx, cl.ID, []model.ActionEvent{
		{GroupID: g.ID, ActionID: a.ID, Type: "group_start"},
		{GroupID: g.ID, ActionID: a.ID, StepSeq: iptr(0), Type: "step_start", Detail: "trigger"},
	})
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	max2, _ := st.AppendEvents(ctx, cl.ID, []model.ActionEvent{
		{GroupID: g.ID, ActionID: a.ID, StepSeq: iptr(0), Type: "step_ok", Status: "ok"},
	})
	if !(max2 > max1) {
		t.Fatalf("event seq not monotonic: %d then %d", max1, max2)
	}
	all, _ := st.ListGroupEvents(ctx, g.ID, 0, 100)
	if len(all) != 3 {
		t.Fatalf("expected 3 events from seq 0, got %d", len(all))
	}
	// Resume from max1 → only the events after it.
	rest, _ := st.ListGroupEvents(ctx, g.ID, max1, 100)
	if len(rest) != 1 || rest[0].Type != "step_ok" {
		t.Fatalf("resume from seq %d wrong: %+v", max1, rest)
	}
}

func iptr(i int) *int { return &i }
