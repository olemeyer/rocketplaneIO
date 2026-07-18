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

// mcp_transactions.go — persistence for the MCP transaction envelope.
//
// Lifecycle: open →(commit)→ committed
//            open →(cancel | deadline)→ cancelling →(restore ok / nothing to
//            restore)→ rolled_back | →(restore failed)→ rollback_failed.
//
// The rollback itself reuses the snapshot substrate: ONE synthetic
// snapshot_restore action carries the chronologically concatenated capture
// list of every run in the transaction; the agent replays it in reverse
// (LIFO). DriveTxnRollbacks below is the state driver — it is called from the
// leader-only reaper loop and (best-effort) right after a cancel.

// ErrTxnAlreadyOpen is returned by CreateMCPTransaction when the token already
// has an open transaction on the cluster.
var ErrTxnAlreadyOpen = errors.New("transaction already open for this token")

// mcpTxnCols is the shared SELECT column list for scanMCPTxn.
const mcpTxnCols = `t.id, t.org_id, t.cluster_id, t.token_id, COALESCE(k.name,''),
	COALESCE(u.email,''), t.incident_id, t.title, t.status, COALESCE(t.close_reason,''),
	t.deadline, t.rollback_action_id, t.created_at, t.closed_at, t.updated_at`

func scanMCPTxn(row pgx.Row) (*model.MCPTransaction, error) {
	var t model.MCPTransaction
	err := row.Scan(&t.ID, &t.OrgID, &t.ClusterID, &t.TokenID, &t.TokenName,
		&t.RequestedBy, &t.IncidentID, &t.Title, &t.Status, &t.CloseReason,
		&t.Deadline, &t.RollbackActionID, &t.CreatedAt, &t.ClosedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DefaultMCPPolicy is the fail-closed org policy: disruptive + destructive
// actions require human approval; transactions default to 30 minutes and are
// capped at 4 hours.
func DefaultMCPPolicy() model.MCPPolicy {
	return model.MCPPolicy{
		ApprovalLevels:    []string{"disruptive", "destructive"},
		DefaultTTLSeconds: 1800,
		MaxTTLSeconds:     14400,
	}
}

// GetMCPPolicy loads the org's MCP policy (org_settings key 'mcp'), merged
// over the defaults. Absent/invalid settings yield the fail-closed defaults.
func (s *Store) GetMCPPolicy(ctx context.Context, orgID uuid.UUID) model.MCPPolicy {
	pol := DefaultMCPPolicy()
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM org_settings WHERE org_id=$1 AND key='mcp'`, orgID).Scan(&raw)
	if err != nil {
		return pol
	}
	var stored model.MCPPolicy
	if json.Unmarshal(raw, &stored) != nil {
		return pol
	}
	if stored.ApprovalLevels != nil {
		pol.ApprovalLevels = stored.ApprovalLevels
	}
	if stored.DefaultTTLSeconds > 0 {
		pol.DefaultTTLSeconds = stored.DefaultTTLSeconds
	}
	if stored.MaxTTLSeconds > 0 {
		pol.MaxTTLSeconds = stored.MaxTTLSeconds
	}
	pol.NotifyProviderIDs = stored.NotifyProviderIDs
	return pol
}

// SetOrgSetting upserts one org-scoped settings value (JSON).
func (s *Store) SetOrgSetting(ctx context.Context, orgID uuid.UUID, key string, value json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_settings (org_id, key, value) VALUES ($1,$2,$3)
		ON CONFLICT (org_id, key) DO UPDATE SET value=$3, updated_at=now()`,
		orgID, key, value)
	if err != nil {
		return fmt.Errorf("set org setting %s: %w", key, err)
	}
	return nil
}

// CreateMCPTransaction opens a transaction for a token on a cluster. The
// partial unique index allows at most ONE open transaction per (token,
// cluster); on conflict the existing open transaction is returned together
// with ErrTxnAlreadyOpen so the caller can tell the agent which one to reuse.
func (s *Store) CreateMCPTransaction(ctx context.Context, orgID, clusterID uuid.UUID,
	tokenID *uuid.UUID, requestedBy uuid.UUID, incidentID *uuid.UUID, title string, ttl time.Duration) (*model.MCPTransaction, error) {
	row := s.pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO mcp_transactions (org_id, cluster_id, token_id, requested_by, incident_id, title, deadline)
			VALUES ($1,$2,$3,$4,$5,$6, now() + $7::interval)
			ON CONFLICT DO NOTHING
			RETURNING *
		)
		SELECT `+mcpTxnCols+`
		FROM ins t
		LEFT JOIN api_tokens k ON k.id = t.token_id
		LEFT JOIN users u ON u.id = t.requested_by`,
		orgID, clusterID, tokenID, requestedBy, incidentID, title, ttl)
	t, err := scanMCPTxn(row)
	if err == nil {
		s.appendTxnEvent(ctx, t.ID, "opened", "begin_transaction", nil,
			map[string]any{"title": title, "deadline": t.Deadline})
		return t, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("create mcp transaction: %w", err)
	}
	// Conflict path: hand back the existing open transaction.
	if tokenID != nil {
		if existing, gerr := s.GetOpenMCPTransactionForToken(ctx, clusterID, *tokenID); gerr == nil {
			return existing, ErrTxnAlreadyOpen
		}
	}
	return nil, fmt.Errorf("create mcp transaction: conflict but no open transaction found")
}

// GetOpenMCPTransactionForToken returns the token's open transaction on the
// cluster, or ErrNotFound.
func (s *Store) GetOpenMCPTransactionForToken(ctx context.Context, clusterID, tokenID uuid.UUID) (*model.MCPTransaction, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+mcpTxnCols+`
		FROM mcp_transactions t
		LEFT JOIN api_tokens k ON k.id = t.token_id
		LEFT JOIN users u ON u.id = t.requested_by
		WHERE t.cluster_id=$1 AND t.token_id=$2 AND t.status='open'`,
		clusterID, tokenID)
	t, err := scanMCPTxn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get open mcp transaction: %w", err)
	}
	return t, nil
}

// GetMCPTransaction loads one transaction incl. its member action runs
// (ordered chronologically — the timeline spine).
func (s *Store) GetMCPTransaction(ctx context.Context, clusterID, id uuid.UUID) (*model.MCPTransaction, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+mcpTxnCols+`
		FROM mcp_transactions t
		LEFT JOIN api_tokens k ON k.id = t.token_id
		LEFT JOIN users u ON u.id = t.requested_by
		WHERE t.cluster_id=$1 AND t.id=$2`,
		clusterID, id)
	t, err := scanMCPTxn(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp transaction: %w", err)
	}
	actions, err := s.ListTxnActions(ctx, id)
	if err != nil {
		return nil, err
	}
	t.Actions = actions
	return t, nil
}

// ListMCPTransactions returns the cluster's recent transactions, newest first.
func (s *Store) ListMCPTransactions(ctx context.Context, clusterID uuid.UUID, limit int) ([]model.MCPTransaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+mcpTxnCols+`
		FROM mcp_transactions t
		LEFT JOIN api_tokens k ON k.id = t.token_id
		LEFT JOIN users u ON u.id = t.requested_by
		WHERE t.cluster_id=$1
		ORDER BY t.created_at DESC
		LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list mcp transactions: %w", err)
	}
	defer rows.Close()
	out := []model.MCPTransaction{}
	for rows.Next() {
		t, err := scanMCPTxn(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mcp transaction: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// ListTxnActions returns every action run belonging to the transaction
// (joined via action_groups.transaction_id), oldest first.
func (s *Store) ListTxnActions(ctx context.Context, txnID uuid.UUID) ([]model.Action, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.cluster_id, COALESCE(u.email,''), a.kind, a.target_namespace, a.target_kind, a.target_name,
		       a.params, a.status, a.result, a.progress, a.steps, a.cancel_requested,
		       a.group_id, a.group_seq, a.created_at, a.updated_at,
		       (a.snapshots <> '[]'::jsonb) AS revertible
		FROM cluster_actions a
		JOIN action_groups g ON g.id = a.group_id
		LEFT JOIN users u ON u.id = a.requested_by
		WHERE g.transaction_id = $1
		ORDER BY a.created_at ASC`, txnID)
	if err != nil {
		return nil, fmt.Errorf("list txn actions: %w", err)
	}
	defer rows.Close()
	out := []model.Action{}
	for rows.Next() {
		var a model.Action
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.RequestedBy, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName,
			&a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.CancelRequested,
			&a.GroupID, &a.GroupSeq, &a.CreatedAt, &a.UpdatedAt, &a.Revertible); err != nil {
			return nil, fmt.Errorf("scan txn action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CommitMCPTransaction atomically closes an open transaction keeping all
// changes. Snapshots remain on the individual runs for manual revert.
func (s *Store) CommitMCPTransaction(ctx context.Context, clusterID, id uuid.UUID) (*model.MCPTransaction, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mcp_transactions
		SET status='committed', close_reason='commit', closed_at=now(), updated_at=now()
		WHERE cluster_id=$1 AND id=$2 AND status='open'`, clusterID, id)
	if err != nil {
		return nil, fmt.Errorf("commit mcp transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	s.appendTxnEvent(ctx, id, "committed", "commit_transaction", nil, nil)
	return s.GetMCPTransaction(ctx, clusterID, id)
}

// MarkTxnCancelling atomically flips an open transaction to cancelling
// (reason: cancel|expired). The rollback itself is driven by
// DriveTxnRollbacks — call it right after for the fast path.
func (s *Store) MarkTxnCancelling(ctx context.Context, clusterID, id uuid.UUID, reason string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE mcp_transactions
		SET status='cancelling', close_reason=$3, updated_at=now()
		WHERE cluster_id=$1 AND id=$2 AND status='open'`, clusterID, id, reason)
	if err != nil {
		return fmt.Errorf("cancel mcp transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	typ := "cancel_requested"
	if reason == "expired" {
		typ = "expired"
	}
	s.appendTxnEvent(ctx, id, typ, "", nil, nil)
	return nil
}

// AppendTxnEvent writes one timeline entry. payload is marshalled and capped —
// the timeline stores summaries, never full tool results.
func (s *Store) AppendTxnEvent(ctx context.Context, txnID uuid.UUID, typ, tool string, actionID *uuid.UUID, payload any) error {
	return s.appendTxnEvent(ctx, txnID, typ, tool, actionID, payload)
}

const txnEventPayloadCap = 8 * 1024

func (s *Store) appendTxnEvent(ctx context.Context, txnID uuid.UUID, typ, tool string, actionID *uuid.UUID, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err == nil {
			if len(b) > txnEventPayloadCap {
				b, _ = json.Marshal(map[string]any{"truncated": true, "bytes": len(b)})
			}
			raw = b
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mcp_transaction_events (txn_id, type, tool, action_id, payload)
		VALUES ($1,$2,$3,$4,$5)`, txnID, typ, tool, actionID, raw)
	if err != nil {
		return fmt.Errorf("append txn event: %w", err)
	}
	return nil
}

// ListTxnEvents returns a transaction's timeline from a given seq (exclusive)
// — the UI resumes with ?from=<last seen seq>.
func (s *Store) ListTxnEvents(ctx context.Context, txnID uuid.UUID, fromSeq int64, limit int) ([]model.MCPTransactionEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `
		SELECT seq, txn_id, type, tool, action_id, payload, at
		FROM mcp_transaction_events
		WHERE txn_id=$1 AND seq > $2
		ORDER BY seq ASC
		LIMIT $3`, txnID, fromSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("list txn events: %w", err)
	}
	defer rows.Close()
	out := []model.MCPTransactionEvent{}
	for rows.Next() {
		var e model.MCPTransactionEvent
		if err := rows.Scan(&e.Seq, &e.TxnID, &e.Type, &e.Tool, &e.ActionID, &e.Payload, &e.At); err != nil {
			return nil, fmt.Errorf("scan txn event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TxnSignal is a broker notification the reaper owes after a state change
// (the store has no broker — the API layer publishes these).
type TxnSignal struct {
	ClusterID uuid.UUID
	ActionID  uuid.UUID // cancel signal target (zero = none)
	Dispatch  bool      // a new pending action (restore) wants agent attention
}

// ExpireMCPTransactions flips every open transaction past its deadline to
// cancelling (reason 'expired'). Returns the affected cluster ids so the
// caller can publish 'transactions' invalidation signals.
func (s *Store) ExpireMCPTransactions(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE mcp_transactions
		SET status='cancelling', close_reason='expired', updated_at=now()
		WHERE status='open' AND deadline < now()
		RETURNING id, cluster_id`)
	if err != nil {
		return nil, fmt.Errorf("expire mcp transactions: %w", err)
	}
	defer rows.Close()
	var clusters []uuid.UUID
	var ids []uuid.UUID
	for rows.Next() {
		var id, cl uuid.UUID
		if err := rows.Scan(&id, &cl); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		clusters = append(clusters, cl)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		s.appendTxnEvent(ctx, id, "expired", "", nil, nil)
	}
	return clusters, nil
}

// DriveTxnRollbacks advances every 'cancelling' transaction one step:
//  1. auto-reject actions still awaiting approval,
//  2. request cancel for pending/running runs (the agent aborts + restores
//     that run itself),
//  3. once every run is terminal, enqueue ONE snapshot_restore action with the
//     chronologically concatenated capture list of the whole transaction
//     (the agent replays it in reverse → LIFO across the transaction),
//  4. track that restore run to its terminal state → rolled_back /
//     rollback_failed.
//
// Idempotent by construction: rollback_action_id is claimed exactly once, and
// every step re-checks current state. Runs leader-only (reaper) plus a
// best-effort immediate pass after a cancel; both may interleave safely.
func (s *Store) DriveTxnRollbacks(ctx context.Context) ([]TxnSignal, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.cluster_id, t.requested_by, t.rollback_action_id
		FROM mcp_transactions t
		WHERE t.status='cancelling'`)
	if err != nil {
		return nil, fmt.Errorf("list cancelling transactions: %w", err)
	}
	type txnRow struct {
		id, clusterID uuid.UUID
		requestedBy   *uuid.UUID
		rollbackID    *uuid.UUID
	}
	var txns []txnRow
	for rows.Next() {
		var t txnRow
		if err := rows.Scan(&t.id, &t.clusterID, &t.requestedBy, &t.rollbackID); err != nil {
			rows.Close()
			return nil, err
		}
		txns = append(txns, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var signals []TxnSignal
	for _, t := range txns {
		sig, err := s.driveOneTxnRollback(ctx, t.id, t.clusterID, t.requestedBy, t.rollbackID)
		if err != nil {
			// Best-effort per transaction — one broken txn must not stall the rest.
			continue
		}
		signals = append(signals, sig...)
	}
	return signals, nil
}

func (s *Store) driveOneTxnRollback(ctx context.Context, txnID, clusterID uuid.UUID, requestedBy, rollbackID *uuid.UUID) ([]TxnSignal, error) {
	var signals []TxnSignal

	// Phase A — a restore run already exists: follow it to its terminal state.
	if rollbackID != nil {
		var status string
		err := s.pool.QueryRow(ctx,
			`SELECT status FROM cluster_actions WHERE id=$1`, *rollbackID).Scan(&status)
		if err != nil {
			return nil, fmt.Errorf("rollback action status: %w", err)
		}
		switch status {
		case "succeeded":
			_, err = s.pool.Exec(ctx, `
				UPDATE mcp_transactions SET status='rolled_back', closed_at=now(), updated_at=now()
				WHERE id=$1 AND status='cancelling'`, txnID)
			if err == nil {
				s.appendTxnEvent(ctx, txnID, "rollback_done", "", rollbackID, nil)
			}
		case "failed", "cancelled":
			_, err = s.pool.Exec(ctx, `
				UPDATE mcp_transactions SET status='rollback_failed', closed_at=now(), updated_at=now()
				WHERE id=$1 AND status='cancelling'`, txnID)
			if err == nil {
				s.appendTxnEvent(ctx, txnID, "rollback_failed", "", rollbackID,
					map[string]any{"restoreStatus": status})
			}
		}
		return signals, err
	}

	// Phase B — auto-reject anything still waiting for a human.
	_, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions a SET
			status='cancelled', approval_state='rejected', approval_decided_at=now(),
			result='auto-rejected: transaction closed', updated_at=now()
		FROM action_groups g
		WHERE g.id = a.group_id AND g.transaction_id = $1 AND a.status='awaiting_approval'`, txnID)
	if err != nil {
		return nil, fmt.Errorf("auto-reject approvals: %w", err)
	}

	// Phase C — request cancel for every still-active run. The existing action
	// reaper guarantees these reach a terminal state within its windows even if
	// the agent is gone.
	cancelRows, err := s.pool.Query(ctx, `
		UPDATE cluster_actions a SET
			cancel_requested = true,
			status   = CASE WHEN a.status = 'pending' THEN 'cancelled' ELSE a.status END,
			result   = CASE WHEN a.status = 'pending' THEN 'cancelled: transaction closed' ELSE a.result END,
			updated_at = now()
		FROM action_groups g
		WHERE g.id = a.group_id AND g.transaction_id = $1 AND a.status IN ('pending','running')
		RETURNING a.id, a.status`, txnID)
	if err != nil {
		return nil, fmt.Errorf("cancel txn actions: %w", err)
	}
	stillRunning := false
	for cancelRows.Next() {
		var id uuid.UUID
		var status string
		if err := cancelRows.Scan(&id, &status); err != nil {
			cancelRows.Close()
			return nil, err
		}
		if status == "running" {
			stillRunning = true
			// The agent learns about the abort via the broker 'cancel' signal.
			signals = append(signals, TxnSignal{ClusterID: clusterID, ActionID: id})
		}
	}
	cancelRows.Close()
	if err := cancelRows.Err(); err != nil {
		return nil, err
	}
	if stillRunning {
		return signals, nil // wait for terminal state; next drive pass continues
	}

	// Re-check: nothing non-terminal may remain (a run may have been claimed
	// between the UPDATE above and here).
	var active int
	err = s.pool.QueryRow(ctx, `
		SELECT count(*) FROM cluster_actions a
		JOIN action_groups g ON g.id = a.group_id
		WHERE g.transaction_id = $1 AND a.status IN ('pending','running','awaiting_approval')`, txnID).Scan(&active)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return signals, nil
	}

	// Phase D — build the combined capture list (chronological; the agent's
	// Restore() walks it in reverse). Runs that failed already auto-restored
	// their own captures on the agent; re-restoring is effectively idempotent
	// (merge-patch back to the same before-state), so we keep it simple and
	// include every non-empty list.
	combined, err := s.collectTxnCaptures(ctx, txnID)
	if err != nil {
		return nil, err
	}

	// Nothing to restore → done.
	if len(combined) == 0 {
		_, err = s.pool.Exec(ctx, `
			UPDATE mcp_transactions SET status='rolled_back', closed_at=now(), updated_at=now()
			WHERE id=$1 AND status='cancelling'`, txnID)
		if err == nil {
			s.appendTxnEvent(ctx, txnID, "rollback_done", "", nil, map[string]any{"captures": 0})
		}
		return signals, err
	}

	// Phase E — claim + enqueue the ONE restore run. The id is generated first
	// and claimed via the NULL-guard so concurrent drivers can never enqueue two.
	restoreID := uuid.New()
	tag, err := s.pool.Exec(ctx, `
		UPDATE mcp_transactions SET rollback_action_id=$2, updated_at=now()
		WHERE id=$1 AND status='cancelling' AND rollback_action_id IS NULL`, txnID, restoreID)
	if err != nil {
		return nil, fmt.Errorf("claim rollback: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return signals, nil // another driver won; it will enqueue
	}
	if requestedBy == nil {
		// No actor to attribute the restore to (token owner deleted) — the
		// transaction cannot be auto-rolled-back; surface as rollback_failed.
		_, _ = s.pool.Exec(ctx, `
			UPDATE mcp_transactions SET status='rollback_failed', closed_at=now(), updated_at=now()
			WHERE id=$1 AND status='cancelling'`, txnID)
		s.appendTxnEvent(ctx, txnID, "rollback_failed", "", nil,
			map[string]any{"reason": "no actor to attribute the restore to"})
		return signals, nil
	}
	g, err := s.CreateGroup(ctx, clusterID, *requestedBy, "mcp", "rollback transaction", nil, "", "")
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE action_groups SET transaction_id=$2 WHERE id=$1`, g.ID, txnID); err != nil {
		return nil, fmt.Errorf("link rollback group: %w", err)
	}
	captures, _ := json.Marshal(combined)
	params, _ := json.Marshal(map[string]any{"snapshots": json.RawMessage(captures)})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO cluster_actions (id, cluster_id, requested_by, kind, target_namespace, target_kind, target_name,
		                             params, group_id, group_seq, source_kind)
		VALUES ($1,$2,$3,'snapshot_restore','-','-','restore',$4,$5,0,'builtin')`,
		restoreID, clusterID, *requestedBy, params, g.ID); err != nil {
		return nil, fmt.Errorf("enqueue txn restore: %w", err)
	}
	s.appendTxnEvent(ctx, txnID, "rollback_started", "", &restoreID,
		map[string]any{"captures": len(combined)})
	signals = append(signals, TxnSignal{ClusterID: clusterID, Dispatch: true})
	return signals, nil
}

// CreateSystemTransactionWithAction wraps a system-initiated operation (alert
// auto-remediation, transaction revert) in its own transaction so it shows up
// on the Transactions page like everything else. The transaction is born
// COMMITTED (system fixes are not auto-rolled-back); its snapshots stay
// available for manual revert. requestedBy may be nil (pure system actor).
func (s *Store) CreateSystemTransactionWithAction(ctx context.Context, clusterID uuid.UUID,
	requestedBy *uuid.UUID, title, origin, kind, ns, targetKind, targetName string, params json.RawMessage) (*model.MCPTransaction, *model.Action, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var txnID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mcp_transactions (org_id, cluster_id, requested_by, title, status, close_reason, deadline, closed_at)
		VALUES ((SELECT org_id FROM clusters WHERE id=$1), $1, $2, $3, 'committed', 'commit', now(), now())
		RETURNING id`, clusterID, requestedBy, title).Scan(&txnID)
	if err != nil {
		return nil, nil, fmt.Errorf("create system transaction: %w", err)
	}
	var g model.ActionGroup
	err = s.pool.QueryRow(ctx, `
		INSERT INTO action_groups (cluster_id, org_id, origin, title, requested_by, transaction_id)
		VALUES ($1, (SELECT org_id FROM clusters WHERE id=$1), $2, $3, $4, $5)
		RETURNING id`, clusterID, origin, title, requestedBy, txnID).Scan(&g.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("create system group: %w", err)
	}
	var a model.Action
	err = s.pool.QueryRow(ctx, `
		INSERT INTO cluster_actions (cluster_id, requested_by, kind, target_namespace, target_kind, target_name, params, group_id, group_seq, source_kind)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,0,$9)
		RETURNING id, cluster_id, kind, target_namespace, target_kind, target_name, params, status, result, progress, steps, cancel_requested, created_at, updated_at`,
		clusterID, requestedBy, kind, ns, targetKind, targetName, params, g.ID,
		map[bool]string{true: "script", false: "builtin"}[kind == "script"],
	).Scan(&a.ID, &a.ClusterID, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName, &a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.CancelRequested, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("insert system action: %w", err)
	}
	s.appendTxnEvent(ctx, txnID, "action_created", origin, &a.ID,
		map[string]any{"kind": kind, "target": ns + "/" + targetKind + "/" + targetName})
	txn, err := s.GetMCPTransaction(ctx, clusterID, txnID)
	if err != nil {
		return nil, nil, err
	}
	return txn, &a, nil
}

// RevertMCPTransaction undoes a COMMITTED transaction after the fact: it opens
// a new "revert" transaction in state cancelling that carries ONE
// snapshot_restore run with the source transaction's concatenated captures.
// The regular rollback driver then walks it to rolled_back — same machinery,
// same visibility as every other rollback.
func (s *Store) RevertMCPTransaction(ctx context.Context, clusterID, txnID uuid.UUID, actorID uuid.UUID) (*model.MCPTransaction, error) {
	src, err := s.GetMCPTransaction(ctx, clusterID, txnID)
	if err != nil {
		return nil, err
	}
	if src.Status != "committed" {
		return nil, fmt.Errorf("only committed transactions can be reverted (status is %s)", src.Status)
	}
	combined, err := s.collectTxnCaptures(ctx, txnID)
	if err != nil {
		return nil, err
	}
	if len(combined) == 0 {
		return nil, errors.New("nothing to revert — no snapshots were captured in this transaction")
	}
	restoreID := uuid.New()
	var revertTxnID uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mcp_transactions (org_id, cluster_id, requested_by, title, status, close_reason, deadline, rollback_action_id)
		VALUES ((SELECT org_id FROM clusters WHERE id=$1), $1, $2, $3, 'cancelling', 'cancel', now(), $4)
		RETURNING id`, clusterID, actorID, "revert: "+src.Title, restoreID).Scan(&revertTxnID)
	if err != nil {
		return nil, fmt.Errorf("create revert transaction: %w", err)
	}
	var groupID uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO action_groups (cluster_id, org_id, origin, title, requested_by, transaction_id)
		VALUES ($1, (SELECT org_id FROM clusters WHERE id=$1), 'mcp', $2, $3, $4)
		RETURNING id`, clusterID, "revert: "+src.Title, actorID, revertTxnID).Scan(&groupID)
	if err != nil {
		return nil, fmt.Errorf("create revert group: %w", err)
	}
	captures, _ := json.Marshal(combined)
	params, _ := json.Marshal(map[string]any{"snapshots": json.RawMessage(captures)})
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO cluster_actions (id, cluster_id, requested_by, kind, target_namespace, target_kind, target_name,
		                             params, group_id, group_seq, source_kind)
		VALUES ($1,$2,$3,'snapshot_restore','-','-','restore',$4,$5,0,'builtin')`,
		restoreID, clusterID, actorID, params, groupID); err != nil {
		return nil, fmt.Errorf("enqueue revert restore: %w", err)
	}
	s.appendTxnEvent(ctx, revertTxnID, "opened", "revert_transaction", nil,
		map[string]any{"revertsTransaction": txnID, "captures": len(combined)})
	s.appendTxnEvent(ctx, revertTxnID, "rollback_started", "", &restoreID, nil)
	s.appendTxnEvent(ctx, txnID, "reverted_by", "", nil, map[string]any{"revertTransaction": revertTxnID})
	return s.GetMCPTransaction(ctx, clusterID, revertTxnID)
}

// collectTxnCaptures concatenates every run's capture list chronologically
// (excluding restore runs) — the payload the agent replays in reverse.
func (s *Store) collectTxnCaptures(ctx context.Context, txnID uuid.UUID) ([]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(a.snapshots,'[]'::jsonb)
		FROM cluster_actions a
		JOIN action_groups g ON g.id = a.group_id
		WHERE g.transaction_id = $1 AND a.kind <> 'snapshot_restore'
		ORDER BY a.created_at ASC`, txnID)
	if err != nil {
		return nil, fmt.Errorf("collect txn snapshots: %w", err)
	}
	defer rows.Close()
	var combined []json.RawMessage
	for rows.Next() {
		var one json.RawMessage
		if err := rows.Scan(&one); err != nil {
			return nil, err
		}
		var entries []json.RawMessage
		if json.Unmarshal(one, &entries) == nil {
			combined = append(combined, entries...)
		}
	}
	return combined, rows.Err()
}

// LinkGroupToTransaction attaches an action group to an MCP transaction —
// the join the timeline, the rollback driver and ListTxnActions all use.
func (s *Store) LinkGroupToTransaction(ctx context.Context, groupID, txnID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE action_groups SET transaction_id=$2, updated_at=now() WHERE id=$1`, groupID, txnID)
	if err != nil {
		return fmt.Errorf("link group to transaction: %w", err)
	}
	return nil
}

// ParkActionForApproval flips a just-created pending run to awaiting_approval
// BEFORE any dispatch signal goes out. ClaimPendingActions only claims
// status='pending', so a parked run can never reach the agent.
func (s *Store) ParkActionForApproval(ctx context.Context, clusterID, actionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status='awaiting_approval', approval_state='pending', updated_at=now()
		WHERE id=$2 AND cluster_id=$1 AND status='pending'`, clusterID, actionID)
	if err != nil {
		return fmt.Errorf("park action for approval: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ApproveAction releases an awaiting_approval run to the agent. created_at is
// reset so the pending reaper's 10-minute unclaimed window starts at approval
// time, not at proposal time. Returns the transaction id (if any) for event
// logging.
func (s *Store) ApproveAction(ctx context.Context, clusterID, actionID, approvedBy uuid.UUID) (*uuid.UUID, error) {
	var txnID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE cluster_actions a SET
			status='pending', approval_state='approved', approved_by=$3,
			approval_decided_at=now(), created_at=now(), updated_at=now()
		FROM action_groups g
		WHERE a.id=$2 AND a.cluster_id=$1 AND a.status='awaiting_approval' AND g.id=a.group_id
		RETURNING g.transaction_id`, clusterID, actionID, approvedBy).Scan(&txnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("approve action: %w", err)
	}
	return txnID, nil
}

// RejectAction cancels an awaiting_approval run. Returns the transaction id
// (if any) for event logging.
func (s *Store) RejectAction(ctx context.Context, clusterID, actionID, rejectedBy uuid.UUID) (*uuid.UUID, error) {
	var txnID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE cluster_actions a SET
			status='cancelled', approval_state='rejected', approved_by=$3,
			approval_decided_at=now(), result='rejected by human review', updated_at=now()
		FROM action_groups g
		WHERE a.id=$2 AND a.cluster_id=$1 AND a.status='awaiting_approval' AND g.id=a.group_id
		RETURNING g.transaction_id`, clusterID, actionID, rejectedBy).Scan(&txnID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reject action: %w", err)
	}
	return txnID, nil
}

// GetActionApproval returns one run's status + approval state (wait_approval
// polling + the approval endpoints read this).
func (s *Store) GetActionApproval(ctx context.Context, clusterID, actionID uuid.UUID) (status, approvalState, result string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT status, approval_state, result FROM cluster_actions
		WHERE id=$2 AND cluster_id=$1`, clusterID, actionID).Scan(&status, &approvalState, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	return status, approvalState, result, err
}

// ReapTransactions is the leader-loop entry point: expire overdue open
// transactions, then advance every cancelling one. Returns broker signals the
// API layer must publish.
func (s *Store) ReapTransactions(ctx context.Context) ([]TxnSignal, error) {
	expired, err := s.ExpireMCPTransactions(ctx)
	if err != nil {
		return nil, err
	}
	signals, err := s.DriveTxnRollbacks(ctx)
	if err != nil {
		return nil, err
	}
	// Expired clusters need a 'transactions' invalidation even without cancels.
	for _, cl := range expired {
		signals = append(signals, TxnSignal{ClusterID: cl})
	}
	return signals, nil
}
