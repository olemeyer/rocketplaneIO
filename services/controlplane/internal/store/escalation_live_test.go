package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// escalation_live_test.go — incident-escalation state machine against a real
// Postgres. RP_LIVE_PG-gated like incidents_live_test.go. Covered: policy CRUD,
// cluster default, setup on declare, due detection, advance with timeline,
// and that acknowledge stops the escalation (next_escalation_at → NULL).
func TestEscalationStateMachineLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the escalation state-machine test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, err := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@esc.local", "Esc Tester", "")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	org, err := st.CreateOrg(ctx, "esc-"+uuid.NewString()[:8], u.ID)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	cl, _, err := st.CreateClusterWithEnrollToken(ctx, org.ID, "esc-cluster", u.ID)
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}

	// Provider (dummy webhook — enough as an escalation target).
	prov, err := st.CreateAlertProvider(ctx, org.ID, "esc-webhook", "webhook", json.RawMessage(`{"url":"http://127.0.0.1:59999/none"}`))
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	// Policy: step0 immediately, step1 after 5 min.
	steps := []model.EscalationStep{
		{AfterMinutes: 0, ProviderIDs: []uuid.UUID{prov.ID}},
		{AfterMinutes: 5, ProviderIDs: []uuid.UUID{prov.ID}},
	}
	pol, err := st.CreateEscalationPolicy(ctx, org.ID, "prod-oncall", steps)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if len(pol.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(pol.Steps))
	}

	// Cross-org provider must be rejected (validation lives in the handler; here
	// we only check that the policy is persisted/read back cleanly).
	got, err := st.GetEscalationPolicy(ctx, org.ID, pol.ID)
	if err != nil || got.Steps[1].AfterMinutes != 5 {
		t.Fatalf("get policy roundtrip failed: %v", err)
	}

	// Set the cluster default.
	if err := st.SetClusterEscalationPolicy(ctx, cl.ID, &pol.ID); err != nil {
		t.Fatalf("set cluster policy: %v", err)
	}
	if pid, _ := st.GetClusterEscalationPolicy(ctx, cl.ID); pid == nil || *pid != pol.ID {
		t.Fatalf("cluster default not persisted")
	}

	// Declare the incident → escalation set up, step0 immediately due.
	inc, err := st.CreateIncident(ctx, org.ID, cl.ID, "escalating incident", "", "critical", &u.ID, u.Email)
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if inc.EscalationPolicyID == nil || *inc.EscalationPolicyID != pol.ID {
		t.Fatalf("incident should carry the policy")
	}
	if inc.NextEscalationAt == nil {
		t.Fatalf("next_escalation_at should be scheduled")
	}

	now := time.Now().UTC().Add(time.Minute) // step0 (after 0) is due
	due, err := st.DueEscalationIncidents(ctx, now)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	var found *EscalationDue
	for i := range due {
		if due[i].IncidentID == inc.ID {
			found = &due[i]
		}
	}
	if found == nil {
		t.Fatalf("incident should be due for escalation")
	}
	if found.Step != 0 {
		t.Fatalf("expected step 0, got %d", found.Step)
	}

	// Claim step 0 atomically (like the Escalator): expected=0 → new=1, next=now+5m.
	next := now.Add(5 * time.Minute)
	claimed, err := st.AdvanceEscalation(ctx, inc.ID, 0, 1, &next, now, []string{prov.Name})
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if !claimed {
		t.Fatalf("step 0 should have been claimed")
	}
	// A second claim with the same expectedStep must fail (idempotency).
	if again, _ := st.AdvanceEscalation(ctx, inc.ID, 0, 1, &next, now, []string{prov.Name}); again {
		t.Fatalf("re-claiming an already-advanced step must fail")
	}
	inc, _ = st.GetIncident(ctx, cl.ID, inc.ID)
	if inc.EscalationStep != 1 {
		t.Fatalf("step should be 1, got %d", inc.EscalationStep)
	}
	events, _ := st.ListIncidentEvents(ctx, inc.ID)
	var escalated int
	for _, e := range events {
		if e.Kind == "escalated" {
			escalated++
		}
	}
	if escalated != 1 {
		t.Fatalf("expected 1 escalated timeline event, got %d", escalated)
	}

	// Now NOT due anymore (next is +5m out).
	due2, _ := st.DueEscalationIncidents(ctx, now)
	for _, d := range due2 {
		if d.IncidentID == inc.ID {
			t.Fatalf("incident should not be due again yet")
		}
	}

	// Acknowledge stops the escalation.
	if _, err := st.UpdateIncidentStatus(ctx, cl.ID, inc.ID, "acknowledged", &u.ID, u.Email); err != nil {
		t.Fatalf("ack: %v", err)
	}
	inc, _ = st.GetIncident(ctx, cl.ID, inc.ID)
	if inc.NextEscalationAt != nil {
		t.Fatalf("acknowledge should clear next_escalation_at")
	}
	dueAfterAck, _ := st.DueEscalationIncidents(ctx, now.Add(10*time.Minute))
	for _, d := range dueAfterAck {
		if d.IncidentID == inc.ID {
			t.Fatalf("acknowledged incident must not escalate")
		}
	}
	// Race guard: even a direct claim (as if a tick had read the row before the
	// ack) must NOT advance an acknowledged incident anymore.
	if claimed, _ := st.AdvanceEscalation(ctx, inc.ID, 1, 2, &next, now.Add(10*time.Minute), []string{prov.Name}); claimed {
		t.Fatalf("claim on an acknowledged incident must fail (status guard)")
	}

	// Delete the policy → the reference on the incident becomes NULL (FK SET NULL).
	if err := st.DeleteEscalationPolicy(ctx, org.ID, pol.ID); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	t.Logf("escalation state machine OK")
}
