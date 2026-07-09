package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// incidents.go — persistence for the incident lifecycle. An incident brackets
// alerts, Copilot investigations and actions across open→acknowledged→mitigated→
// resolved. The timeline (incident_events) is the canonical chronicle; alert
// firing/clearing, status changes, notes and linked investigations/actions
// each write an entry. The alert evaluator deduplicates open auto-incidents
// via dedup_key = "alert:<ruleId>".

// execer is satisfied by both *pgxpool.Pool and pgx.Tx — lets us write timeline
// entries either directly or inside a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Status ranks for the lifecycle (open < acknowledged < mitigated < resolved).
func incidentStatusValid(s string) bool {
	switch s {
	case "open", "acknowledged", "mitigated", "resolved":
		return true
	}
	return false
}

func incidentSeverityValid(s string) bool {
	switch s {
	case "critical", "high", "medium", "low":
		return true
	}
	return false
}

const incidentSelect = `
	i.id, i.org_id, i.cluster_id, i.number, i.title, i.summary, i.severity, i.status, i.source,
	i.assignee_id, COALESCE(a.name,''), COALESCE(a.email,''),
	i.created_by, COALESCE(NULLIF(cb.name,''), cb.email, ''),
	i.acknowledged_at, i.mitigated_at, i.resolved_at, i.postmortem, i.created_at, i.updated_at,
	i.escalation_policy_id, i.escalation_step, i.next_escalation_at
	FROM incidents i
	LEFT JOIN users a  ON a.id  = i.assignee_id
	LEFT JOIN users cb ON cb.id = i.created_by`

func scanIncident(row pgx.Row, inc *model.Incident) error {
	return row.Scan(&inc.ID, &inc.OrgID, &inc.ClusterID, &inc.Number, &inc.Title, &inc.Summary,
		&inc.Severity, &inc.Status, &inc.Source,
		&inc.AssigneeID, &inc.AssigneeName, &inc.AssigneeEmail,
		&inc.CreatedBy, &inc.CreatedByName,
		&inc.AcknowledgedAt, &inc.MitigatedAt, &inc.ResolvedAt, &inc.Postmortem,
		&inc.CreatedAt, &inc.UpdatedAt,
		&inc.EscalationPolicyID, &inc.EscalationStep, &inc.NextEscalationAt)
}

// ListIncidents returns a cluster's incidents (newest first). status is an
// optional filter; "open" groups together everything not yet resolved.
func (s *Store) ListIncidents(ctx context.Context, clusterID uuid.UUID, status string, limit int) ([]model.Incident, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	where := "i.cluster_id=$1"
	args := []any{clusterID}
	switch status {
	case "open":
		where += " AND i.status <> 'resolved'"
	case "acknowledged", "mitigated", "resolved":
		args = append(args, status)
		where += fmt.Sprintf(" AND i.status=$%d", len(args))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT `+incidentSelect+`
		WHERE `+where+` ORDER BY i.created_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()
	out := []model.Incident{}
	ids := []uuid.UUID{}
	for rows.Next() {
		var inc model.Incident
		if err := scanIncident(rows, &inc); err != nil {
			return nil, err
		}
		out = append(out, inc)
		ids = append(ids, inc.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Timeline length per incident (one roundtrip).
	if len(ids) > 0 {
		counts := map[uuid.UUID]int{}
		crows, err := s.pool.Query(ctx, `SELECT incident_id, count(*) FROM incident_events WHERE incident_id = ANY($1) GROUP BY incident_id`, ids)
		if err == nil {
			for crows.Next() {
				var id uuid.UUID
				var n int
				if crows.Scan(&id, &n) == nil {
					counts[id] = n
				}
			}
			crows.Close()
		}
		for i := range out {
			out[i].EventCount = counts[out[i].ID]
		}
	}
	return out, nil
}

// GetIncident returns a single incident (cluster-scoped).
func (s *Store) GetIncident(ctx context.Context, clusterID, id uuid.UUID) (*model.Incident, error) {
	var inc model.Incident
	err := scanIncident(s.pool.QueryRow(ctx, `SELECT `+incidentSelect+`
		WHERE i.id=$1 AND i.cluster_id=$2`, id, clusterID), &inc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get incident: %w", err)
	}
	return &inc, nil
}

// ListIncidentEvents returns the timeline in chronological order (oldest first).
func (s *Store) ListIncidentEvents(ctx context.Context, incidentID uuid.UUID) ([]model.IncidentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, incident_id, at, kind, actor_id, actor_email, message, ref_type, ref_id, metadata
		FROM incident_events WHERE incident_id=$1 ORDER BY at ASC, id ASC`, incidentID)
	if err != nil {
		return nil, fmt.Errorf("list incident events: %w", err)
	}
	defer rows.Close()
	out := []model.IncidentEvent{}
	for rows.Next() {
		var e model.IncidentEvent
		if err := rows.Scan(&e.ID, &e.IncidentID, &e.At, &e.Kind, &e.ActorID, &e.ActorEmail,
			&e.Message, &e.RefType, &e.RefID, &e.Metadata); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// writeIncidentEvent appends a timeline entry (pool or tx) and bumps the
// incident's updated_at.
func writeIncidentEvent(ctx context.Context, q execer, incidentID uuid.UUID, kind string, actorID *uuid.UUID, actorEmail, message, refType string, refID *uuid.UUID, metadata []byte) error {
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO incident_events (incident_id, kind, actor_id, actor_email, message, ref_type, ref_id, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		incidentID, kind, actorID, actorEmail, message, refType, refID, metadata); err != nil {
		return fmt.Errorf("write incident event: %w", err)
	}
	_, _ = q.Exec(ctx, `UPDATE incidents SET updated_at=now() WHERE id=$1`, incidentID)
	return nil
}

// nextIncidentNumber returns the next per-org sequential number (atomically).
func nextIncidentNumber(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		INSERT INTO incident_counters (org_id, seq) VALUES ($1, 1)
		ON CONFLICT (org_id) DO UPDATE SET seq = incident_counters.seq + 1
		RETURNING seq`, orgID).Scan(&n)
	return n, err
}

// setupIncidentEscalation attaches the cluster's default policy to a freshly
// declared incident and schedules the first step (next_escalation_at =
// declaration + steps[0].afterMinutes). A no-op when there is no policy/steps.
func setupIncidentEscalation(ctx context.Context, tx pgx.Tx, incidentID, clusterID uuid.UUID) error {
	var policyID *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT policy_id FROM cluster_escalation WHERE cluster_id=$1`, clusterID).Scan(&policyID)
	if errors.Is(err, pgx.ErrNoRows) || policyID == nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cluster escalation: %w", err)
	}
	var stepsRaw []byte
	if err := tx.QueryRow(ctx, `SELECT steps FROM escalation_policies WHERE id=$1`, *policyID).Scan(&stepsRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	var steps []model.EscalationStep
	_ = json.Unmarshal(stepsRaw, &steps)
	if len(steps) == 0 {
		_, err = tx.Exec(ctx, `UPDATE incidents SET escalation_policy_id=$1 WHERE id=$2`, *policyID, incidentID)
		return err
	}
	next := time.Now().UTC().Add(time.Duration(steps[0].AfterMinutes) * time.Minute)
	_, err = tx.Exec(ctx, `
		UPDATE incidents SET escalation_policy_id=$1, escalation_step=0, next_escalation_at=$2 WHERE id=$3`,
		*policyID, next, incidentID)
	return err
}

// CreateIncident declares an incident manually.
func (s *Store) CreateIncident(ctx context.Context, orgID, clusterID uuid.UUID, title, summary, severity string, actorID *uuid.UUID, actorEmail string) (*model.Incident, error) {
	if !incidentSeverityValid(severity) {
		severity = "high"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	num, err := nextIncidentNumber(ctx, tx, orgID)
	if err != nil {
		return nil, fmt.Errorf("incident number: %w", err)
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO incidents (org_id, cluster_id, number, title, summary, severity, status, source, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,'open','manual',$7) RETURNING id`,
		orgID, clusterID, num, title, summary, severity, actorID).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert incident: %w", err)
	}
	if err := writeIncidentEvent(ctx, tx, id, "declared", actorID, actorEmail,
		fmt.Sprintf("Incident declared (%s)", severity), "", nil, nil); err != nil {
		return nil, err
	}
	if err := setupIncidentEscalation(ctx, tx, id, clusterID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// DeclareIncidentFromAlert declares (or deduplicates) an incident when an alert
// rule transitions to `firing`. Called from the evaluator; org_id is resolved
// from the cluster. Returns the incident ID and created=true when a new incident
// was created.
func (s *Store) DeclareIncidentFromAlert(ctx context.Context, ruleID, clusterID uuid.UUID, ruleName, severity string, value float64, alertEventID *uuid.UUID) (uuid.UUID, bool, error) {
	if !incidentSeverityValid(severity) {
		severity = "high"
	}
	dedup := "alert:" + ruleID.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)

	// Any open incident for this rule?
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM incidents WHERE dedup_key=$1 AND status<>'resolved' FOR UPDATE`, dedup).Scan(&id)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		var orgID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT org_id FROM clusters WHERE id=$1`, clusterID).Scan(&orgID); err != nil {
			return uuid.Nil, false, fmt.Errorf("resolve org: %w", err)
		}
		num, err := nextIncidentNumber(ctx, tx, orgID)
		if err != nil {
			return uuid.Nil, false, err
		}
		title := ruleName
		if title == "" {
			title = "Alert firing"
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO incidents (org_id, cluster_id, number, title, severity, status, source, dedup_key)
			VALUES ($1,$2,$3,$4,$5,'open','alert',$6) RETURNING id`,
			orgID, clusterID, num, title, severity, dedup).Scan(&id); err != nil {
			return uuid.Nil, false, fmt.Errorf("insert alert incident: %w", err)
		}
		created = true
		if err := writeIncidentEvent(ctx, tx, id, "declared", nil, "",
			fmt.Sprintf("Auto-declared from alert %q", ruleName), "", nil, nil); err != nil {
			return uuid.Nil, false, err
		}
		if err := setupIncidentEscalation(ctx, tx, id, clusterID); err != nil {
			return uuid.Nil, false, err
		}
	} else if err != nil {
		return uuid.Nil, false, fmt.Errorf("find open incident: %w", err)
	}

	meta := []byte(fmt.Sprintf(`{"value":%g}`, value))
	if err := writeIncidentEvent(ctx, tx, id, "alert", nil, "",
		fmt.Sprintf("Alert %q firing (value %.2f)", ruleName, value), "alert_event", alertEventID, meta); err != nil {
		return uuid.Nil, false, err
	}
	// Link the alert event to the incident.
	if alertEventID != nil {
		_, _ = tx.Exec(ctx, `UPDATE alert_events SET incident_id=$1 WHERE id=$2`, id, *alertEventID)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return id, created, nil
}

// ResolveIncidentFromAlert closes an auto-incident when its alert rule falls
// back to `ok`. Manually declared incidents stay open (just a timeline entry).
// Returns the affected incident ID (Nil if there is none).
func (s *Store) ResolveIncidentFromAlert(ctx context.Context, ruleID, clusterID uuid.UUID, ruleName string, alertEventID *uuid.UUID) (uuid.UUID, error) {
	dedup := "alert:" + ruleID.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var source string
	err = tx.QueryRow(ctx, `SELECT id, source FROM incidents WHERE dedup_key=$1 AND status<>'resolved' FOR UPDATE`, dedup).Scan(&id, &source)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find open incident: %w", err)
	}
	if err := writeIncidentEvent(ctx, tx, id, "alert_cleared", nil, "",
		fmt.Sprintf("Alert %q cleared", ruleName), "alert_event", alertEventID, nil); err != nil {
		return uuid.Nil, err
	}
	if alertEventID != nil {
		_, _ = tx.Exec(ctx, `UPDATE alert_events SET incident_id=$1 WHERE id=$2`, id, *alertEventID)
	}
	// Auto-incidents (source='alert') close together with the alert; dedup_key
	// is cleared so that a later re-open does not collide with a new auto-
	// incident.
	if source == "alert" {
		if _, err := tx.Exec(ctx, `
			UPDATE incidents SET status='resolved', resolved_at=now(), dedup_key=NULL,
			  next_escalation_at=NULL, updated_at=now()
			WHERE id=$1`, id); err != nil {
			return uuid.Nil, err
		}
		if err := writeIncidentEvent(ctx, tx, id, "status", nil, "",
			"Auto-resolved: triggering alert cleared", "", nil, []byte(`{"to":"resolved","auto":true}`)); err != nil {
			return uuid.Nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// UpdateIncidentStatus advances the lifecycle and sets the matching timestamps
// (acknowledged_at/mitigated_at/resolved_at). Writes the timeline.
func (s *Store) UpdateIncidentStatus(ctx context.Context, clusterID, id uuid.UUID, status string, actorID *uuid.UUID, actorEmail string) (*model.Incident, error) {
	if !incidentStatusValid(status) {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var prev string
	if err := tx.QueryRow(ctx, `SELECT status FROM incidents WHERE id=$1 AND cluster_id=$2 FOR UPDATE`, id, clusterID).Scan(&prev); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if prev == status {
		return s.GetIncident(ctx, clusterID, id)
	}
	// Set timestamp columns; re-open does not retroactively clear the closing
	// markers, but does reset resolved_at so MTTR stays clean.
	set := "status=$1, updated_at=now()"
	switch status {
	case "acknowledged":
		set += ", acknowledged_at=COALESCE(acknowledged_at, now()), acknowledged_by=$3"
	case "mitigated":
		set += ", mitigated_at=COALESCE(mitigated_at, now())"
	case "resolved":
		set += ", resolved_at=now(), resolved_by=$3, dedup_key=NULL"
	case "open":
		set += ", resolved_at=NULL, mitigated_at=NULL"
	}
	// Escalation stops as soon as the incident leaves the open state (ack/
	// mitigate/resolve). Reopen deliberately does NOT restart it.
	if status != "open" {
		set += ", next_escalation_at=NULL"
	}
	q := `UPDATE incidents SET ` + set + ` WHERE id=$2`
	if status == "acknowledged" || status == "resolved" {
		_, err = tx.Exec(ctx, q, status, id, actorID)
	} else {
		_, err = tx.Exec(ctx, q, status, id)
	}
	if err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	meta := []byte(fmt.Sprintf(`{"from":%q,"to":%q}`, prev, status))
	if err := writeIncidentEvent(ctx, tx, id, "status", actorID, actorEmail,
		fmt.Sprintf("Status %s → %s", prev, status), "", nil, meta); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// UpdateIncidentFields changes title/summary/severity and logs the relevant
// changes. Nil pointers leave the field unchanged.
func (s *Store) UpdateIncidentFields(ctx context.Context, clusterID, id uuid.UUID, title, summary, severity *string, actorID *uuid.UUID, actorEmail string) (*model.Incident, error) {
	cur, err := s.GetIncident(ctx, clusterID, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if title != nil && *title != "" && *title != cur.Title {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET title=$1, updated_at=now() WHERE id=$2`, *title, id); err != nil {
			return nil, err
		}
	}
	if summary != nil && *summary != cur.Summary {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET summary=$1, updated_at=now() WHERE id=$2`, *summary, id); err != nil {
			return nil, err
		}
	}
	if severity != nil && incidentSeverityValid(*severity) && *severity != cur.Severity {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET severity=$1, updated_at=now() WHERE id=$2`, *severity, id); err != nil {
			return nil, err
		}
		meta := []byte(fmt.Sprintf(`{"from":%q,"to":%q}`, cur.Severity, *severity))
		if err := writeIncidentEvent(ctx, tx, id, "severity", actorID, actorEmail,
			fmt.Sprintf("Severity %s → %s", cur.Severity, *severity), "", nil, meta); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// AssignIncident sets/removes the responsible person.
func (s *Store) AssignIncident(ctx context.Context, clusterID, id uuid.UUID, assigneeID *uuid.UUID, actorID *uuid.UUID, actorEmail string) (*model.Incident, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE incidents SET assignee_id=$1, updated_at=now() WHERE id=$2 AND cluster_id=$3`, assigneeID, id, clusterID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	msg := "Unassigned"
	var meta []byte
	if assigneeID != nil {
		var name string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(name,''), email) FROM users WHERE id=$1`, *assigneeID).Scan(&name)
		msg = "Assigned to " + name
		meta = []byte(fmt.Sprintf(`{"assigneeId":%q}`, assigneeID.String()))
	}
	if err := writeIncidentEvent(ctx, tx, id, "assigned", actorID, actorEmail, msg, "", nil, meta); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// AddIncidentNote appends a free-form note to the timeline.
func (s *Store) AddIncidentNote(ctx context.Context, clusterID, id uuid.UUID, note string, actorID *uuid.UUID, actorEmail string) error {
	if _, err := s.GetIncident(ctx, clusterID, id); err != nil {
		return err
	}
	return writeIncidentEvent(ctx, s.pool, id, "note", actorID, actorEmail, note, "", nil, nil)
}

// SetIncidentPostmortem stores/updates the postmortem.
func (s *Store) SetIncidentPostmortem(ctx context.Context, clusterID, id uuid.UUID, text string, actorID *uuid.UUID, actorEmail string) (*model.Incident, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE incidents SET postmortem=$1, updated_at=now() WHERE id=$2 AND cluster_id=$3`, text, id, clusterID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if err := writeIncidentEvent(ctx, tx, id, "postmortem", actorID, actorEmail, "Postmortem updated", "", nil, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// LinkInvestigationToIncident links a Copilot investigation (via its chat ID)
// to an incident and writes a timeline entry.
func (s *Store) LinkInvestigationToIncident(ctx context.Context, clusterID, incidentID, chatID uuid.UUID, actorID *uuid.UUID, actorEmail string) error {
	if _, err := s.GetIncident(ctx, clusterID, incidentID); err != nil {
		return err
	}
	// Enforce cluster scope: the investigation MUST belong to the same cluster
	// as the incident — otherwise a foreign chat_id could attach an investigation
	// from another org/cluster to an incident (IDOR).
	tag, err := s.pool.Exec(ctx, `
		UPDATE copilot_investigations SET incident_id=$1 WHERE chat_id=$2 AND cluster_id=$3`,
		incidentID, chatID, clusterID)
	if err != nil {
		return fmt.Errorf("link investigation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	meta := []byte(fmt.Sprintf(`{"chatId":%q}`, chatID.String()))
	return writeIncidentEvent(ctx, s.pool, incidentID, "investigation", actorID, actorEmail,
		"Copilot investigation linked", "chat", &chatID, meta)
}

// AttachActionToIncident links an executed action to an incident.
func (s *Store) AttachActionToIncident(ctx context.Context, incidentID, actionID uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE cluster_actions SET incident_id=$1 WHERE id=$2`, incidentID, actionID)
	if err != nil {
		return err
	}
	meta := []byte(fmt.Sprintf(`{"actionId":%q}`, actionID.String()))
	return writeIncidentEvent(ctx, s.pool, incidentID, "action", nil, "",
		"Action taken: "+title, "action", &actionID, meta)
}

// IncidentStats returns metrics for the list header (open incidents,
// MTTA/MTTR over resolved incidents from the last 30 days, in seconds).
func (s *Store) IncidentStats(ctx context.Context, clusterID uuid.UUID) (map[string]float64, error) {
	out := map[string]float64{}
	row := s.pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status <> 'resolved'),
		  count(*) FILTER (WHERE status = 'open'),
		  count(*) FILTER (WHERE severity = 'critical' AND status <> 'resolved'),
		  COALESCE(avg(EXTRACT(EPOCH FROM (acknowledged_at - created_at))) FILTER (WHERE acknowledged_at IS NOT NULL AND created_at > now() - interval '30 days'), 0),
		  COALESCE(avg(EXTRACT(EPOCH FROM (resolved_at - created_at)))     FILTER (WHERE resolved_at IS NOT NULL AND created_at > now() - interval '30 days'), 0)
		FROM incidents WHERE cluster_id=$1`, clusterID)
	var open, unack, crit, mtta, mttr float64
	if err := row.Scan(&open, &unack, &crit, &mtta, &mttr); err != nil {
		return nil, err
	}
	out["open"] = open
	out["unacknowledged"] = unack
	out["critical"] = crit
	out["mttaSeconds"] = mtta
	out["mttrSeconds"] = mttr
	return out, nil
}
