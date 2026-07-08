package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// incidents.go — Persistenz des Incident-Lebenszyklus. Ein Incident klammert
// Alerts, Copilot-Investigations und Actions über open→acknowledged→mitigated→
// resolved. Die Timeline (incident_events) ist die kanonische Chronik; Alert-
// Feuern/Klären, Statuswechsel, Notizen und verlinkte Investigations/Actions
// schreiben je einen Eintrag. Der Alert-Evaluator dedupliziert offene Auto-
// Incidents über dedup_key = "alert:<ruleId>".

// execer wird von *pgxpool.Pool und pgx.Tx erfüllt — erlaubt, Timeline-Einträge
// sowohl direkt als auch innerhalb einer Transaktion zu schreiben.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Statusränge für den Lebenszyklus (open < acknowledged < mitigated < resolved).
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
	i.acknowledged_at, i.mitigated_at, i.resolved_at, i.postmortem, i.created_at, i.updated_at
	FROM incidents i
	LEFT JOIN users a  ON a.id  = i.assignee_id
	LEFT JOIN users cb ON cb.id = i.created_by`

func scanIncident(row pgx.Row, inc *model.Incident) error {
	return row.Scan(&inc.ID, &inc.OrgID, &inc.ClusterID, &inc.Number, &inc.Title, &inc.Summary,
		&inc.Severity, &inc.Status, &inc.Source,
		&inc.AssigneeID, &inc.AssigneeName, &inc.AssigneeEmail,
		&inc.CreatedBy, &inc.CreatedByName,
		&inc.AcknowledgedAt, &inc.MitigatedAt, &inc.ResolvedAt, &inc.Postmortem,
		&inc.CreatedAt, &inc.UpdatedAt)
}

// ListIncidents liefert die Incidents eines Clusters (neueste zuerst). status
// filtert optional; "open" fasst alle nicht-resolved zusammen.
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
	// Timeline-Länge je Incident (ein Roundtrip).
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

// GetIncident liefert einen Incident (Cluster-scoped).
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

// ListIncidentEvents liefert die Timeline chronologisch (älteste zuerst).
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

// writeIncidentEvent hängt einen Timeline-Eintrag an (pool oder tx) und stupst
// updated_at des Incidents nach.
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

// nextIncidentNumber liefert die nächste pro-Org fortlaufende Nummer (atomar).
func nextIncidentNumber(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `
		INSERT INTO incident_counters (org_id, seq) VALUES ($1, 1)
		ON CONFLICT (org_id) DO UPDATE SET seq = incident_counters.seq + 1
		RETURNING seq`, orgID).Scan(&n)
	return n, err
}

// CreateIncident deklariert einen Incident manuell.
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
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, clusterID, id)
}

// DeclareIncidentFromAlert deklariert (oder dedupliziert) einen Incident, wenn
// eine Alert-Regel nach `firing` wechselt. Aufruf aus dem Evaluator; org_id
// wird aus dem Cluster aufgelöst. Gibt die Incident-ID und created=true zurück,
// wenn ein neuer Incident entstand.
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

	// Offener Incident für diese Regel?
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
	} else if err != nil {
		return uuid.Nil, false, fmt.Errorf("find open incident: %w", err)
	}

	meta := []byte(fmt.Sprintf(`{"value":%g}`, value))
	if err := writeIncidentEvent(ctx, tx, id, "alert", nil, "",
		fmt.Sprintf("Alert %q firing (value %.2f)", ruleName, value), "alert_event", alertEventID, meta); err != nil {
		return uuid.Nil, false, err
	}
	// Alert-Event mit dem Incident verknüpfen.
	if alertEventID != nil {
		_, _ = tx.Exec(ctx, `UPDATE alert_events SET incident_id=$1 WHERE id=$2`, id, *alertEventID)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return id, created, nil
}

// ResolveIncidentFromAlert schließt einen Auto-Incident, wenn seine Alert-Regel
// nach `ok` zurückfällt. Manuell deklarierte Incidents bleiben offen (nur ein
// Timeline-Eintrag). Gibt die betroffene Incident-ID zurück (Nil, falls keiner).
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
	// Auto-Incidents (source='alert') schließen sich mit dem Alert; dedup_key
	// wird geleert, damit ein späteres Re-Open nicht mit einem neuen Auto-
	// Incident kollidiert.
	if source == "alert" {
		if _, err := tx.Exec(ctx, `
			UPDATE incidents SET status='resolved', resolved_at=now(), dedup_key=NULL, updated_at=now()
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

// UpdateIncidentStatus fährt den Lebenszyklus weiter und setzt die passenden
// Timestamps (acknowledged_at/mitigated_at/resolved_at). Schreibt Timeline.
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
	// Timestamp-Spalten setzen; Re-Open leert die Abschlussmarken nicht rückwirkend,
	// setzt aber resolved_at zurück, damit MTTR sauber bleibt.
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

// UpdateIncidentFields ändert Titel/Summary/Severity und protokolliert relevante
// Änderungen. Leere Zeiger lassen das Feld unverändert.
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

// AssignIncident setzt/entfernt die zuständige Person.
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

// AddIncidentNote hängt eine freie Notiz an die Timeline.
func (s *Store) AddIncidentNote(ctx context.Context, clusterID, id uuid.UUID, note string, actorID *uuid.UUID, actorEmail string) error {
	if _, err := s.GetIncident(ctx, clusterID, id); err != nil {
		return err
	}
	return writeIncidentEvent(ctx, s.pool, id, "note", actorID, actorEmail, note, "", nil, nil)
}

// SetIncidentPostmortem speichert/aktualisiert das Postmortem.
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

// LinkInvestigationToIncident verknüpft eine Copilot-Investigation (über ihre
// Chat-ID) mit einem Incident und schreibt einen Timeline-Eintrag.
func (s *Store) LinkInvestigationToIncident(ctx context.Context, clusterID, incidentID, chatID uuid.UUID, actorID *uuid.UUID, actorEmail string) error {
	if _, err := s.GetIncident(ctx, clusterID, incidentID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE copilot_investigations SET incident_id=$1 WHERE chat_id=$2`, incidentID, chatID)
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

// AttachActionToIncident verknüpft eine ausgeführte Action mit einem Incident.
func (s *Store) AttachActionToIncident(ctx context.Context, incidentID, actionID uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE cluster_actions SET incident_id=$1 WHERE id=$2`, incidentID, actionID)
	if err != nil {
		return err
	}
	meta := []byte(fmt.Sprintf(`{"actionId":%q}`, actionID.String()))
	return writeIncidentEvent(ctx, s.pool, incidentID, "action", nil, "",
		"Action taken: "+title, "action", &actionID, meta)
}

// IncidentStats liefert Kennzahlen für den Listen-Header (offene Incidents,
// MTTA/MTTR über resolved Incidents der letzten 30 Tage in Sekunden).
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
