package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// action_definitions.go — CRUD für org-weite Starlark-Workflows.

const defCols = "id, org_id, name, description, params, source, timeout_seconds, created_at, updated_at"

func scanDef(row pgx.Row) (*model.ActionDefinition, error) {
	var d model.ActionDefinition
	if err := row.Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.Params, &d.Source, &d.TimeoutSeconds, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListActionDefinitions(ctx context.Context, orgID uuid.UUID) ([]model.ActionDefinition, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+defCols+` FROM action_definitions WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list action definitions: %w", err)
	}
	defer rows.Close()
	out := []model.ActionDefinition{}
	for rows.Next() {
		d, err := scanDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetActionDefinition(ctx context.Context, orgID, id uuid.UUID) (*model.ActionDefinition, error) {
	return scanDef(s.pool.QueryRow(ctx,
		`SELECT `+defCols+` FROM action_definitions WHERE org_id=$1 AND id=$2`, orgID, id))
}

func (s *Store) CreateActionDefinition(ctx context.Context, orgID, userID uuid.UUID, name, description string, params json.RawMessage, source string, timeoutSeconds int) (*model.ActionDefinition, error) {
	if len(params) == 0 {
		params = json.RawMessage("[]")
	}
	return scanDef(s.pool.QueryRow(ctx, `
		INSERT INTO action_definitions (org_id, name, description, params, source, created_by, timeout_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+defCols, orgID, name, description, params, source, userID, timeoutSeconds))
}

func (s *Store) UpdateActionDefinition(ctx context.Context, orgID, id uuid.UUID, name, description string, params json.RawMessage, source string, timeoutSeconds int) (*model.ActionDefinition, error) {
	if len(params) == 0 {
		params = json.RawMessage("[]")
	}
	return scanDef(s.pool.QueryRow(ctx, `
		UPDATE action_definitions SET name=$3, description=$4, params=$5, source=$6, timeout_seconds=$7, updated_at=now()
		WHERE org_id=$1 AND id=$2
		RETURNING `+defCols, orgID, id, name, description, params, source, timeoutSeconds))
}

func (s *Store) DeleteActionDefinition(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM action_definitions WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return fmt.Errorf("delete action definition: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
