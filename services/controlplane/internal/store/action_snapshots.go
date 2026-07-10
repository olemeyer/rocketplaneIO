package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// action_snapshots.go — the durable, ordered snapshot list backing the generic
// rollback (snapshot substrate). The agent appends each mutation's minimal
// restorable delta as it commits; the reaper replays the list in reverse on a
// crash, and the manual revert replays the same list.

// AppendActionSnapshots appends a batch of capture entries (a JSON array) to a
// running action's ordered snapshot list. Called the instant a mutation commits,
// so the undo state is durable before the action finishes. Order is preserved
// (jsonb `||` concatenates).
func (s *Store) AppendActionSnapshots(ctx context.Context, clusterID, actionID uuid.UUID, captures json.RawMessage) error {
	if len(captures) == 0 {
		return nil
	}
	// Guard: only append a JSON array; a malformed batch must not corrupt the list.
	var probe []json.RawMessage
	if err := json.Unmarshal(captures, &probe); err != nil {
		return fmt.Errorf("append snapshots: batch is not a JSON array: %w", err)
	}
	if len(probe) == 0 {
		return nil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions
		SET snapshots = COALESCE(snapshots,'[]'::jsonb) || $3::jsonb, updated_at = now()
		WHERE id = $2 AND cluster_id = $1 AND status = 'running'`,
		clusterID, actionID, captures)
	if err != nil {
		return fmt.Errorf("append snapshots: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetActionSnapshots returns a run's ordered snapshot list (the reaper + the
// manual-revert path read this to replay the generic restore).
func (s *Store) GetActionSnapshots(ctx context.Context, actionID uuid.UUID) (json.RawMessage, error) {
	var snaps json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(snapshots,'[]'::jsonb) FROM cluster_actions WHERE id = $1`, actionID).Scan(&snaps)
	if err != nil {
		return nil, fmt.Errorf("get snapshots: %w", err)
	}
	return snaps, nil
}

// ReapCrashedSnapshotRuns handles the snapshot-substrate analogue of
// ReapCrashedAgents: a run that was MID-MUTATION when its agent died, whose undo
// state is the durable ordered snapshot list. Because the list is generic and
// ordered, one enqueued `snapshot_restore` action replays ALL of it in reverse —
// multi-mutation-correct (the v4 single-inverse reaper could only replay the
// last write). The CP never touches the cluster; a restarted or peer agent
// claims the restore and applies it. Crash signal = running + a non-empty
// snapshot list + >90s silent (a live agent reports every ~2s).
func (s *Store) ReapCrashedSnapshotRuns(ctx context.Context) (int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, cluster_id, group_id, requested_by, snapshots
		FROM cluster_actions
		WHERE status = 'running'
		  AND snapshots IS NOT NULL AND snapshots <> '[]'::jsonb
		  AND updated_at < now() - interval '90 seconds'`)
	if err != nil {
		return 0, fmt.Errorf("scan crashed snapshot runs: %w", err)
	}
	type crashed struct {
		id, clusterID, groupID uuid.UUID
		requestedBy            *uuid.UUID
		snapshots              json.RawMessage
	}
	var list []crashed
	for rows.Next() {
		var c crashed
		if err := rows.Scan(&c.id, &c.clusterID, &c.groupID, &c.requestedBy, &c.snapshots); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan crashed snapshot run: %w", err)
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var n int64
	for _, c := range list {
		// Claim the crash transition atomically (peer CP / late report may win).
		tag, err := s.pool.Exec(ctx, `
			UPDATE cluster_actions SET status='failed',
				result='agent crashed mid-action — reverting from the durable snapshot list',
				progress='', updated_at=now()
			WHERE id=$1 AND status='running'`, c.id)
		if err != nil {
			return n, fmt.Errorf("mark crashed (snapshot): %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		_, _ = s.pool.Exec(ctx, `UPDATE action_groups SET revert_status='reverting' WHERE id=$1`, c.groupID)
		if c.requestedBy == nil {
			continue
		}
		params, _ := json.Marshal(map[string]any{"snapshots": json.RawMessage(c.snapshots)})
		if _, err := s.AppendAction(ctx, c.groupID, nil, c.clusterID, *c.requestedBy,
			"snapshot_restore", "-", "-", "restore", "builtin", params); err != nil {
			return n, fmt.Errorf("enqueue snapshot restore: %w", err)
		}
		n++
	}
	return n, nil
}
