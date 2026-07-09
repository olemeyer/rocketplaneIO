package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// maintenance.go — maintenance windows: planned suppression of alert
// notifications + auto-incident declaration for a cluster (optionally a single
// namespace). The evaluator calls InMaintenance on the hot path.

const maintenanceCols = `m.id, m.org_id, m.cluster_id, m.title, m.scope_namespace,
	m.starts_at, m.ends_at, m.created_by, COALESCE(NULLIF(u.name,''), u.email, ''), m.created_at`

func scanMaintenance(row pgx.Row, m *model.MaintenanceWindow) error {
	return row.Scan(&m.ID, &m.OrgID, &m.ClusterID, &m.Title, &m.ScopeNamespace,
		&m.StartsAt, &m.EndsAt, &m.CreatedBy, &m.CreatedByName, &m.CreatedAt)
}

func maintenanceStatus(m *model.MaintenanceWindow, now time.Time) string {
	switch {
	case now.Before(m.StartsAt):
		return "scheduled"
	case now.Before(m.EndsAt):
		return "active"
	default:
		return "ended"
	}
}

// ListMaintenanceWindows returns a cluster's windows (active/scheduled first,
// then most recent). Ended windows older than 7 days are omitted.
func (s *Store) ListMaintenanceWindows(ctx context.Context, clusterID uuid.UUID) ([]model.MaintenanceWindow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+maintenanceCols+`
		FROM maintenance_windows m LEFT JOIN users u ON u.id = m.created_by
		WHERE m.cluster_id=$1 AND m.ends_at > now() - interval '7 days'
		ORDER BY m.ends_at DESC`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list maintenance windows: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []model.MaintenanceWindow{}
	for rows.Next() {
		var m model.MaintenanceWindow
		if err := scanMaintenance(rows, &m); err != nil {
			return nil, err
		}
		m.Status = maintenanceStatus(&m, now)
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateMaintenanceWindow schedules a window. ends_at must be after starts_at.
func (s *Store) CreateMaintenanceWindow(ctx context.Context, orgID, clusterID uuid.UUID, title, namespace string, startsAt, endsAt time.Time, createdBy *uuid.UUID) (*model.MaintenanceWindow, error) {
	if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("ends_at must be after starts_at")
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO maintenance_windows (org_id, cluster_id, title, scope_namespace, starts_at, ends_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		orgID, clusterID, title, namespace, startsAt, endsAt, createdBy).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert maintenance window: %w", err)
	}
	return s.getMaintenanceWindow(ctx, clusterID, id)
}

func (s *Store) getMaintenanceWindow(ctx context.Context, clusterID, id uuid.UUID) (*model.MaintenanceWindow, error) {
	var m model.MaintenanceWindow
	err := scanMaintenance(s.pool.QueryRow(ctx, `SELECT `+maintenanceCols+`
		FROM maintenance_windows m LEFT JOIN users u ON u.id = m.created_by
		WHERE m.id=$1 AND m.cluster_id=$2`, id, clusterID), &m)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.Status = maintenanceStatus(&m, time.Now().UTC())
	return &m, nil
}

// EndMaintenanceWindow ends an active window immediately (sets ends_at=now).
// Scheduled (not yet started) windows are deleted instead.
func (s *Store) EndMaintenanceWindow(ctx context.Context, clusterID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE maintenance_windows SET ends_at=now()
		WHERE id=$1 AND cluster_id=$2 AND starts_at <= now() AND ends_at > now()`, id, clusterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// Not currently active — delete (scheduled or already ended).
	tag, err = s.pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE id=$1 AND cluster_id=$2`, id, clusterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMaintenanceWindow removes a window outright.
func (s *Store) DeleteMaintenanceWindow(ctx context.Context, clusterID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM maintenance_windows WHERE id=$1 AND cluster_id=$2`, id, clusterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// InMaintenance reports whether the given cluster+namespace is currently under an
// active maintenance window. A cluster-wide window (scope_namespace='') matches
// any namespace; a namespace-scoped window matches only that namespace. This is
// the evaluator's hot-path suppression check.
func (s *Store) InMaintenance(ctx context.Context, clusterID uuid.UUID, namespace string, now time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM maintenance_windows
			WHERE cluster_id=$1 AND starts_at <= $3 AND ends_at > $3
			  AND (scope_namespace='' OR scope_namespace=$2)
		)`, clusterID, namespace, now).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("in maintenance: %w", err)
	}
	return exists, nil
}
