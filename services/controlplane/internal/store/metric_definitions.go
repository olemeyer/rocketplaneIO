package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

const mdefCols = "id, cluster_id, name, description, source, namespace, workload, search, value_mode, pattern, agg, unit, query, created_at"

func scanMDef(row pgx.Row) (*model.MetricDefinition, error) {
	var d model.MetricDefinition
	if err := row.Scan(&d.ID, &d.ClusterID, &d.Name, &d.Description, &d.Source, &d.Namespace,
		&d.Workload, &d.Search, &d.ValueMode, &d.Pattern, &d.Agg, &d.Unit, &d.Query, &d.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &d, nil
}

func (s *Store) ListMetricDefinitions(ctx context.Context, clusterID uuid.UUID) ([]model.MetricDefinition, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mdefCols+` FROM metric_definitions WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list metric defs: %w", err)
	}
	defer rows.Close()
	out := []model.MetricDefinition{}
	for rows.Next() {
		d, err := scanMDef(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) GetMetricDefinition(ctx context.Context, clusterID, id uuid.UUID) (*model.MetricDefinition, error) {
	return scanMDef(s.pool.QueryRow(ctx, `SELECT `+mdefCols+` FROM metric_definitions WHERE cluster_id=$1 AND id=$2`, clusterID, id))
}

func (s *Store) CreateMetricDefinition(ctx context.Context, d *model.MetricDefinition) (*model.MetricDefinition, error) {
	return scanMDef(s.pool.QueryRow(ctx, `
		INSERT INTO metric_definitions (cluster_id, name, description, source, namespace, workload, search, value_mode, pattern, agg, unit, query)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+mdefCols,
		d.ClusterID, d.Name, d.Description, d.Source, d.Namespace, d.Workload, d.Search, d.ValueMode, d.Pattern, d.Agg, d.Unit, d.Query))
}

func (s *Store) UpdateMetricDefinition(ctx context.Context, clusterID, id uuid.UUID, d *model.MetricDefinition) (*model.MetricDefinition, error) {
	return scanMDef(s.pool.QueryRow(ctx, `
		UPDATE metric_definitions SET name=$3, description=$4, source=$5, namespace=$6, workload=$7, search=$8, value_mode=$9, pattern=$10, agg=$11, unit=$12, query=$13
		WHERE cluster_id=$1 AND id=$2 RETURNING `+mdefCols,
		clusterID, id, d.Name, d.Description, d.Source, d.Namespace, d.Workload, d.Search, d.ValueMode, d.Pattern, d.Agg, d.Unit, d.Query))
}

func (s *Store) DeleteMetricDefinition(ctx context.Context, clusterID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM metric_definitions WHERE cluster_id=$1 AND id=$2`, clusterID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
