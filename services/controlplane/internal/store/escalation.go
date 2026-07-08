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

// escalation.go — Eskalations-Policies (org-weit) + der Datenzugriff des
// Escalators. Policies sind geordnete Notification-Ketten aus Alert-Providern;
// der Escalator (alerts/escalation.go) scannt fällige offene Incidents und
// feuert den nächsten Schritt.

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

// ListEscalationPolicies liefert alle Policies eines Orgs.
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

// GetEscalationPolicy lädt eine Policy (org-scoped).
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

// EscalationPolicyStepsByID liefert nur die Schritte (Escalator-Pfad, ohne
// Org-Scope, da über die Incident-FK bereits autorisiert).
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
		if steps[i].ProviderIDs == nil {
			steps[i].ProviderIDs = []uuid.UUID{}
		}
	}
	return steps
}

// CreateEscalationPolicy legt eine Policy an.
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

// UpdateEscalationPolicy ersetzt Name + Schritte.
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

// DeleteEscalationPolicy entfernt eine Policy (FKs setzen Referenzen auf NULL).
func (s *Store) DeleteEscalationPolicy(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM escalation_policies WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetClusterEscalationPolicy liefert die Default-Policy-ID eines Clusters (oder nil).
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

// SetClusterEscalationPolicy setzt (oder löscht, policyID=nil) die Default-Policy.
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

// EscalationDue beschreibt einen fälligen Eskalationsschritt.
type EscalationDue struct {
	IncidentID uuid.UUID
	ClusterID  uuid.UUID
	Number     int
	Title      string
	Severity   string
	PolicyID   uuid.UUID
	Step       int
}

// DueEscalationIncidents liefert offene Incidents, deren nächster Schritt fällig ist.
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

// AdvanceEscalation setzt Schritt + nächste Fälligkeit und schreibt — wenn
// providerNames gesetzt sind — einen 'escalated'-Timeline-Eintrag. Ohne Namen
// (Kette erschöpft) nur Zustand nachziehen, kein Event.
func (s *Store) AdvanceEscalation(ctx context.Context, incidentID uuid.UUID, newStep int, nextAt *time.Time, firedStep int, providerNames []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE incidents SET escalation_step=$1, next_escalation_at=$2, updated_at=now() WHERE id=$3`,
		newStep, nextAt, incidentID); err != nil {
		return err
	}
	if len(providerNames) > 0 {
		namesRaw, _ := json.Marshal(providerNames)
		meta := []byte(fmt.Sprintf(`{"step":%d,"providers":%s}`, firedStep+1, string(namesRaw)))
		msg := fmt.Sprintf("Escalation step %d: paged %d channel(s)", firedStep+1, len(providerNames))
		if err := writeIncidentEvent(ctx, tx, incidentID, "escalated", nil, "", msg, "", nil, meta); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
