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

// alerts.go — Providers (channels), Rules (typed checks) and Events.

/* ── Provider ───────────────────────────────────────────────────────────── */

const provCols = "id, org_id, name, type, config, created_at"

func scanProvider(row pgx.Row) (*model.AlertProvider, error) {
	var p model.AlertProvider
	if err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.Type, &p.Config, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListAlertProviders(ctx context.Context, orgID uuid.UUID) ([]model.AlertProvider, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+provCols+` FROM alert_providers WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	out := []model.AlertProvider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) CreateAlertProvider(ctx context.Context, orgID uuid.UUID, name, ptype string, config json.RawMessage) (*model.AlertProvider, error) {
	return scanProvider(s.pool.QueryRow(ctx, `
		INSERT INTO alert_providers (org_id, name, type, config) VALUES ($1,$2,$3,$4)
		RETURNING `+provCols, orgID, name, ptype, config))
}

func (s *Store) DeleteAlertProvider(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_providers WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ProvidersByIDs loads providers for dispatch (evaluator-side, without an org-scope
// check — the IDs come from the rule, which was already created with an org check).
func (s *Store) ProvidersByIDs(ctx context.Context, ids []uuid.UUID) ([]model.AlertProvider, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+provCols+` FROM alert_providers WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AlertProvider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

/* ── Rules ──────────────────────────────────────────────────────────────── */

const ruleCols = `id, cluster_id, name, kind, params, op, threshold, window_seconds, for_seconds,
	severity, provider_ids, enabled, state, state_since, last_value, last_eval_at, last_error,
	query, muted_until, action_definition_id, action_args, created_at`

func scanRule(row pgx.Row) (*model.AlertRule, error) {
	var r model.AlertRule
	if err := row.Scan(&r.ID, &r.ClusterID, &r.Name, &r.Kind, &r.Params, &r.Op, &r.Threshold,
		&r.WindowSeconds, &r.ForSeconds, &r.Severity, &r.ProviderIDs, &r.Enabled,
		&r.State, &r.StateSince, &r.LastValue, &r.LastEvalAt, &r.LastError,
		&r.Query, &r.MutedUntil, &r.ActionDefinitionID, &r.ActionArgs, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) ListAlertRules(ctx context.Context, clusterID uuid.UUID) ([]model.AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM alert_rules WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	out := []model.AlertRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListEnabledRulesAll: all active rules (evaluator, across all clusters).
func (s *Store) ListEnabledRulesAll(ctx context.Context) ([]model.AlertRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM alert_rules WHERE enabled ORDER BY cluster_id, name`)
	if err != nil {
		return nil, fmt.Errorf("list enabled rules: %w", err)
	}
	defer rows.Close()
	out := []model.AlertRule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) CreateAlertRule(ctx context.Context, r *model.AlertRule) (*model.AlertRule, error) {
	return scanRule(s.pool.QueryRow(ctx, `
		INSERT INTO alert_rules (cluster_id, name, kind, params, op, threshold, window_seconds, for_seconds, severity, provider_ids, enabled, query, muted_until, action_definition_id, action_args)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING `+ruleCols,
		r.ClusterID, r.Name, r.Kind, r.Params, r.Op, r.Threshold, r.WindowSeconds, r.ForSeconds, r.Severity, r.ProviderIDs, r.Enabled,
		r.Query, r.MutedUntil, r.ActionDefinitionID, r.ActionArgs))
}

func (s *Store) UpdateAlertRule(ctx context.Context, clusterID, id uuid.UUID, r *model.AlertRule) (*model.AlertRule, error) {
	return scanRule(s.pool.QueryRow(ctx, `
		UPDATE alert_rules SET name=$3, kind=$4, params=$5, op=$6, threshold=$7, window_seconds=$8,
			for_seconds=$9, severity=$10, provider_ids=$11, enabled=$12,
			query=$13, muted_until=$14, action_definition_id=$15, action_args=$16
		WHERE cluster_id=$1 AND id=$2
		RETURNING `+ruleCols,
		clusterID, id, r.Name, r.Kind, r.Params, r.Op, r.Threshold, r.WindowSeconds, r.ForSeconds, r.Severity, r.ProviderIDs, r.Enabled,
		r.Query, r.MutedUntil, r.ActionDefinitionID, r.ActionArgs))
}

func (s *Store) DeleteAlertRule(ctx context.Context, clusterID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM alert_rules WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRuleEval writes the evaluation result (state-machine transition).
func (s *Store) UpdateRuleEval(ctx context.Context, id uuid.UUID, state string, stateSince time.Time, lastValue float64, lastError string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE alert_rules SET state=$2, state_since=$3, last_value=$4, last_eval_at=now(), last_error=$5
		WHERE id=$1`, id, state, stateSince, lastValue, lastError)
	return err
}

/* ── Events ─────────────────────────────────────────────────────────────── */

// InsertAlertEvent writes a state transition and returns the event ID
// (the evaluator links it to the auto-declared incident).
func (s *Store) InsertAlertEvent(ctx context.Context, ruleID, clusterID uuid.UUID, from, to string, value float64, msg string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO alert_events (rule_id, cluster_id, from_state, to_state, value, message)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, ruleID, clusterID, from, to, value, msg).Scan(&id)
	return id, err
}

func (s *Store) ListAlertEvents(ctx context.Context, clusterID uuid.UUID, limit int) ([]model.AlertEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.rule_id, COALESCE(r.name, '(deleted)'), e.at, e.from_state, e.to_state, e.value, e.message
		FROM alert_events e LEFT JOIN alert_rules r ON r.id = e.rule_id
		WHERE e.cluster_id=$1 ORDER BY e.at DESC LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	out := []model.AlertEvent{}
	for rows.Next() {
		var e model.AlertEvent
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.At, &e.FromState, &e.ToState, &e.Value, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SetRuleMute sets/clears the snooze deadline of a rule.
func (s *Store) SetRuleMute(ctx context.Context, clusterID, id uuid.UUID, until *time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE alert_rules SET muted_until=$3 WHERE cluster_id=$1 AND id=$2`, clusterID, id, until)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetActionDefinitionForCluster resolves a workflow definition via the cluster's
// org (auto-remediation in the evaluator — no session context).
func (s *Store) GetActionDefinitionForCluster(ctx context.Context, clusterID, defID uuid.UUID) (*model.ActionDefinition, error) {
	return scanDef(s.pool.QueryRow(ctx, `
		SELECT d.id, d.org_id, d.name, d.description, d.params, d.source, d.timeout_seconds, d.created_at, d.updated_at
		FROM action_definitions d
		JOIN clusters c ON c.org_id = d.org_id
		WHERE c.id=$1 AND d.id=$2`, clusterID, defID))
}

