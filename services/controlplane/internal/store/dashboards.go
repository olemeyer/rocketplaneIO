package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// dashboards.go — CRUD für org-weite Dashboards-as-Code (Perses-YAML). Spiegelt
// das action_definitions-Muster: org-scoped, UNIQUE(org_id, name).

const dashCols = "id, org_id, name, description, spec, created_at, updated_at"

func scanDashboard(row pgx.Row) (*model.Dashboard, error) {
	var d model.Dashboard
	if err := row.Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.Spec, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListDashboards(ctx context.Context, orgID uuid.UUID) ([]model.Dashboard, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+dashCols+` FROM dashboards WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	defer rows.Close()
	out := []model.Dashboard{}
	for rows.Next() {
		d, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetDashboard(ctx context.Context, orgID, id uuid.UUID) (*model.Dashboard, error) {
	return scanDashboard(s.pool.QueryRow(ctx,
		`SELECT `+dashCols+` FROM dashboards WHERE org_id=$1 AND id=$2`, orgID, id))
}

func (s *Store) CreateDashboard(ctx context.Context, orgID, userID uuid.UUID, name, description, spec string) (*model.Dashboard, error) {
	return scanDashboard(s.pool.QueryRow(ctx, `
		INSERT INTO dashboards (org_id, name, description, spec, created_by)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING `+dashCols, orgID, name, description, spec, userID))
}

func (s *Store) UpdateDashboard(ctx context.Context, orgID, id uuid.UUID, name, description, spec string) (*model.Dashboard, error) {
	return scanDashboard(s.pool.QueryRow(ctx, `
		UPDATE dashboards SET name=$3, description=$4, spec=$5, updated_at=now()
		WHERE org_id=$1 AND id=$2
		RETURNING `+dashCols, orgID, id, name, description, spec))
}

func (s *Store) DeleteDashboard(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM dashboards WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
