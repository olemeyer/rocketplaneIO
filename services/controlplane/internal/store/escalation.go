package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// escalation.go — escalation policies (org-wide) plus the data access for the
// escalator. Policies are ordered notification chains of alert providers; the
// escalator (alerts/escalation.go) scans due open incidents and fires the next
// step.

func scanPolicy(row pgx.Row, p *model.EscalationPolicy) error {
	var stepsRaw []byte
	if err := row.Scan(&p.ID, &p.OrgID, &p.Name, &stepsRaw, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return err
	}
	p.Steps = []model.EscalationStep{}
	_ = json.Unmarshal(stepsRaw, &p.Steps)
	return nil
}

const policyCols = `id, org_id, name, steps, created_at, updated_at`

// ListEscalationPolicies returns all policies of an org.
func (s *Store) ListEscalationPolicies(ctx context.Context, orgID uuid.UUID) ([]model.EscalationPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+policyCols+` FROM escalation_policies WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list policies: %w", err)
	}
	defer rows.Close()
	out := []model.EscalationPolicy{}
	for rows.Next() {
		var p model.EscalationPolicy
		if err := scanPolicy(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetEscalationPolicy loads a policy (org-scoped).
func (s *Store) GetEscalationPolicy(ctx context.Context, orgID, id uuid.UUID) (*model.EscalationPolicy, error) {
	var p model.EscalationPolicy
	err := scanPolicy(s.pool.QueryRow(ctx, `SELECT `+policyCols+` FROM escalation_policies WHERE id=$1 AND org_id=$2`, id, orgID), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// EscalationPolicyStepsByID returns just the steps (escalator path, without
// org scope, since it is already authorized via the incident FK).
func (s *Store) EscalationPolicyStepsByID(ctx context.Context, id uuid.UUID) ([]model.EscalationStep, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT steps FROM escalation_policies WHERE id=$1`, id).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	steps := []model.EscalationStep{}
	_ = json.Unmarshal(raw, &steps)
	return steps, nil
}

func normalizeSteps(steps []model.EscalationStep) []model.EscalationStep {
	if steps == nil {
		return []model.EscalationStep{}
	}
	for i := range steps {
		if steps[i].AfterMinutes < 0 {
			steps[i].AfterMinutes = 0
		}
		// Cap at 7 days — prevents time.Duration overflow in the escalator.
		if steps[i].AfterMinutes > 7*24*60 {
			steps[i].AfterMinutes = 7 * 24 * 60
		}
		if steps[i].ProviderIDs == nil {
			steps[i].ProviderIDs = []uuid.UUID{}
		}
	}
	return steps
}

// CreateEscalationPolicy creates a policy.
func (s *Store) CreateEscalationPolicy(ctx context.Context, orgID uuid.UUID, name string, steps []model.EscalationStep) (*model.EscalationPolicy, error) {
	raw, _ := json.Marshal(normalizeSteps(steps))
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO escalation_policies (org_id, name, steps) VALUES ($1,$2,$3) RETURNING id`,
		orgID, name, raw).Scan(&id); err != nil {
		return nil, fmt.Errorf("create policy: %w", err)
	}
	return s.GetEscalationPolicy(ctx, orgID, id)
}

// UpdateEscalationPolicy replaces name + steps.
func (s *Store) UpdateEscalationPolicy(ctx context.Context, orgID, id uuid.UUID, name string, steps []model.EscalationStep) (*model.EscalationPolicy, error) {
	raw, _ := json.Marshal(normalizeSteps(steps))
	tag, err := s.pool.Exec(ctx, `
		UPDATE escalation_policies SET name=$1, steps=$2, updated_at=now() WHERE id=$3 AND org_id=$4`,
		name, raw, id, orgID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetEscalationPolicy(ctx, orgID, id)
}

// DeleteEscalationPolicy removes a policy. The FKs set the references to NULL;
// to make sure no incident is left with next_escalation_at set but no policy
// (inconsistent, would never fire again on reopen), we clear the running
// escalation timers of the affected incidents in the same transaction.
func (s *Store) DeleteEscalationPolicy(ctx context.Context, orgID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE incidents SET next_escalation_at=NULL, updated_at=now()
		WHERE escalation_policy_id=$1 AND next_escalation_at IS NOT NULL`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM escalation_policies WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// GetClusterEscalationPolicy returns the default policy ID of a cluster (or nil).
func (s *Store) GetClusterEscalationPolicy(ctx context.Context, clusterID uuid.UUID) (*uuid.UUID, error) {
	var pid *uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT policy_id FROM cluster_escalation WHERE cluster_id=$1`, clusterID).Scan(&pid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pid, nil
}

// SetClusterEscalationPolicy sets (or clears, policyID=nil) the default policy.
func (s *Store) SetClusterEscalationPolicy(ctx context.Context, clusterID uuid.UUID, policyID *uuid.UUID) error {
	if policyID == nil {
		_, err := s.pool.Exec(ctx, `DELETE FROM cluster_escalation WHERE cluster_id=$1`, clusterID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cluster_escalation (cluster_id, policy_id) VALUES ($1,$2)
		ON CONFLICT (cluster_id) DO UPDATE SET policy_id=$2`, clusterID, policyID)
	return err
}

// EscalationDue describes a due escalation step.
type EscalationDue struct {
	IncidentID uuid.UUID
	ClusterID  uuid.UUID
	Number     int
	Title      string
	Severity   string
	PolicyID   uuid.UUID
	Step       int
}

// DueEscalationIncidents returns open incidents whose next step is due.
func (s *Store) DueEscalationIncidents(ctx context.Context, now time.Time) ([]EscalationDue, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, cluster_id, number, title, severity, escalation_policy_id, escalation_step
		FROM incidents
		WHERE status='open' AND escalation_policy_id IS NOT NULL
		  AND next_escalation_at IS NOT NULL AND next_escalation_at <= $1
		ORDER BY next_escalation_at ASC LIMIT 100`, now)
	if err != nil {
		return nil, fmt.Errorf("due escalations: %w", err)
	}
	defer rows.Close()
	out := []EscalationDue{}
	for rows.Next() {
		var d EscalationDue
		if err := rows.Scan(&d.IncidentID, &d.ClusterID, &d.Number, &d.Title, &d.Severity, &d.PolicyID, &d.Step); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AdvanceEscalation is the ATOMIC claim of an escalation step: the UPDATE is
// guarded with `status='open' AND escalation_step=expectedStep AND
// next_escalation_at <= now`, so that an intervening acknowledge/resolve OR an
// advance that already happened (another tick) loses the claim (0 rows →
// returns false). The caller (escalator) claims FIRST and notifies AFTERWARDS —
// if the send fails, the step is advanced anyway and is not fired again
// (prevents double paging). A timeline entry is only written on a real step
// (newStep != expectedStep); this allows clearing just `next_escalation_at`
// with newStep==expectedStep+0 (exhausted chain) without an event.
func (s *Store) AdvanceEscalation(ctx context.Context, incidentID uuid.UUID, expectedStep, newStep int, nextAt *time.Time, now time.Time, providerNames []string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE incidents SET escalation_step=$1, next_escalation_at=$2, updated_at=now()
		WHERE id=$3 AND status='open' AND escalation_step=$4
		  AND next_escalation_at IS NOT NULL AND next_escalation_at <= $5`,
		newStep, nextAt, incidentID, expectedStep, now)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // acknowledged/resolved or already advanced — no claim
	}
	// Only write a timeline entry when a step was actually fired.
	if newStep != expectedStep {
		namesRaw, _ := json.Marshal(providerNames)
		if providerNames == nil {
			namesRaw = []byte(`[]`)
		}
		meta := []byte(fmt.Sprintf(`{"step":%d,"providers":%s}`, expectedStep+1, string(namesRaw)))
		var msg string
		if len(providerNames) == 0 {
			msg = fmt.Sprintf("Escalation step %d: no channels configured", expectedStep+1)
		} else {
			msg = fmt.Sprintf("Escalation step %d: paged %d channel(s)", expectedStep+1, len(providerNames))
		}
		if err := writeIncidentEvent(ctx, tx, incidentID, "escalated", nil, "", msg, "", nil, meta); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
