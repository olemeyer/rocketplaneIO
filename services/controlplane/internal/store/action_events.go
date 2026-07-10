package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// action_events.go — Safe Actions v2 durable step/event spine. The agent posts a
// batch of steps + events with each result; the CP appends them (assigning the
// global monotonic event seq) so every action streams the same live steps
// everywhere and any client can resume from a seq (?from=<lastSeq>).

// UpsertStep inserts or updates a durable step row (idempotent on (action_id,seq),
// so re-posted batches converge). group_id is denormalized for trace queries.
func (s *Store) UpsertStep(ctx context.Context, st model.ActionStep) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cluster_action_steps
		  (action_id, group_id, seq, parent_seq, name, kind, effect_class, status, detail, structured, error_code, started_at, ended_at)
		VALUES ($1,$2,$3,$4,$5,COALESCE(NULLIF($6,''),'trigger'),COALESCE(NULLIF($7,''),'observe'),
		        COALESCE(NULLIF($8,''),'pending'),$9,$10,NULLIF($11,''),$12,$13)
		ON CONFLICT (action_id, seq) DO UPDATE SET
		  status=EXCLUDED.status, detail=EXCLUDED.detail, structured=EXCLUDED.structured,
		  error_code=EXCLUDED.error_code, ended_at=COALESCE(EXCLUDED.ended_at, cluster_action_steps.ended_at)`,
		st.ActionID, st.GroupID, st.Seq, st.ParentSeq, st.Name, st.Kind, st.EffectClass, st.Status,
		st.Detail, st.Structured, st.ErrorCode, st.StartedAt, st.EndedAt)
	if err != nil {
		return fmt.Errorf("upsert step: %w", err)
	}
	return nil
}

// AppendEvents appends a batch to the event spine and returns the highest seq
// assigned (the wake-signal value clients resume from). Append-only, ordered.
func (s *Store) AppendEvents(ctx context.Context, clusterID uuid.UUID, evs []model.ActionEvent) (int64, error) {
	var maxSeq int64
	for _, e := range evs {
		var seq int64
		if err := s.pool.QueryRow(ctx, `
			INSERT INTO cluster_action_events (cluster_id, group_id, action_id, step_seq, type, status, detail, payload)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING seq`,
			clusterID, e.GroupID, e.ActionID, e.StepSeq, e.Type, e.Status, e.Detail, e.Payload,
		).Scan(&seq); err != nil {
			return maxSeq, fmt.Errorf("append event: %w", err)
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq, nil
}

// ListGroupEvents returns the group's events with seq > fromSeq in order — the
// SSE seed (?from=0) and resume (?from=<lastSeq>) query.
func (s *Store) ListGroupEvents(ctx context.Context, groupID uuid.UUID, fromSeq int64, limit int) ([]model.ActionEvent, error) {
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT seq, cluster_id, group_id, action_id, step_seq, type, COALESCE(status,''), COALESCE(detail,''), payload, at
		FROM cluster_action_events
		WHERE group_id = $1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, groupID, fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list group events: %w", err)
	}
	defer rows.Close()
	out := []model.ActionEvent{}
	for rows.Next() {
		var e model.ActionEvent
		var payload []byte
		if err := rows.Scan(&e.Seq, &e.ClusterID, &e.GroupID, &e.ActionID, &e.StepSeq, &e.Type, &e.Status, &e.Detail, &payload, &e.At); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if len(payload) > 0 {
			e.Payload = json.RawMessage(payload)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListActionSteps returns the durable step rows of one action (trace detail).
func (s *Store) ListActionSteps(ctx context.Context, actionID uuid.UUID) ([]model.ActionStep, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT action_id, group_id, seq, parent_seq, name, kind, effect_class, status, COALESCE(detail,''), structured, COALESCE(error_code,''), started_at, ended_at
		FROM cluster_action_steps WHERE action_id = $1 ORDER BY seq ASC`, actionID)
	if err != nil {
		return nil, fmt.Errorf("list action steps: %w", err)
	}
	defer rows.Close()
	out := []model.ActionStep{}
	for rows.Next() {
		var st model.ActionStep
		var structured []byte
		if err := rows.Scan(&st.ActionID, &st.GroupID, &st.Seq, &st.ParentSeq, &st.Name, &st.Kind, &st.EffectClass,
			&st.Status, &st.Detail, &structured, &st.ErrorCode, &st.StartedAt, &st.EndedAt); err != nil {
			return nil, fmt.Errorf("scan step: %w", err)
		}
		if len(structured) > 0 {
			st.Structured = json.RawMessage(structured)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}
