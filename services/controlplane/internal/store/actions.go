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

// actions.go — Safe-Actions-Persistenz. Lebenszyklus:
//   UI → CreateAction (pending) → Agent ClaimPendingActions (running)
//   → Agent CompleteAction (succeeded|failed + result).
// Der Claim ist atomar (UPDATE … RETURNING), damit auch mehrere Agent-Replicas
// eine Aktion nie doppelt ausführen.

// CreateGroup opens a fresh ActionGroup. org_id is derived from the cluster so
// callers only need cluster + user. Runs are appended via AppendAction.
func (s *Store) CreateGroup(ctx context.Context, clusterID, userID uuid.UUID, origin, title string,
	chatID, investigationID, incidentID *uuid.UUID, turnID, atomicity, onFailure string) (*model.ActionGroup, error) {
	if origin == "" {
		origin = "manual_single"
	}
	if atomicity == "" {
		atomicity = "independent"
	}
	if onFailure == "" {
		onFailure = "continue"
	}
	var g model.ActionGroup
	err := s.pool.QueryRow(ctx, `
		INSERT INTO action_groups (cluster_id, org_id, origin, chat_id, investigation_id, incident_id, turn_id, title, requested_by, atomicity, on_failure)
		VALUES ($1, (SELECT org_id FROM clusters WHERE id=$1), $2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10)
		RETURNING id, cluster_id, org_id, origin, chat_id, investigation_id, incident_id, COALESCE(turn_id,''), title, atomicity, on_failure, status, revert_status, created_at, updated_at`,
		clusterID, origin, chatID, investigationID, incidentID, turnID, title, userID, atomicity, onFailure,
	).Scan(&g.ID, &g.ClusterID, &g.OrgID, &g.Origin, &g.ChatID, &g.InvestigationID, &g.IncidentID, &g.TurnID, &g.Title, &g.Atomicity, &g.OnFailure, &g.Status, &g.RevertStatus, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create action group: %w", err)
	}
	return &g, nil
}

// GetOrCreateTurnGroup returns the one group for a (chat, turn) pair, creating it
// if absent — idempotent on Copilot resume via the uq_action_groups_turn index.
func (s *Store) GetOrCreateTurnGroup(ctx context.Context, clusterID, userID uuid.UUID, chatID, investigationID *uuid.UUID, turnID, title string) (*model.ActionGroup, error) {
	var g model.ActionGroup
	err := s.pool.QueryRow(ctx, `
		INSERT INTO action_groups (cluster_id, org_id, origin, chat_id, investigation_id, turn_id, title, requested_by)
		VALUES ($1, (SELECT org_id FROM clusters WHERE id=$1), 'copilot_turn', $2, $3, $4, $5, $6)
		ON CONFLICT (chat_id, turn_id) WHERE chat_id IS NOT NULL AND turn_id IS NOT NULL
		DO UPDATE SET updated_at = now()
		RETURNING id, cluster_id, org_id, origin, chat_id, investigation_id, incident_id, COALESCE(turn_id,''), title, atomicity, on_failure, status, revert_status, created_at, updated_at`,
		clusterID, chatID, investigationID, turnID, title, userID,
	).Scan(&g.ID, &g.ClusterID, &g.OrgID, &g.Origin, &g.ChatID, &g.InvestigationID, &g.IncidentID, &g.TurnID, &g.Title, &g.Atomicity, &g.OnFailure, &g.Status, &g.RevertStatus, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get-or-create turn group: %w", err)
	}
	return &g, nil
}

// AppendAction adds a pending run to a group with the next group_seq (max+1
// within the group, assigned atomically). investigationNodeID + sourceKind are
// stamped for the bidirectional link + builtin/script classification.
func (s *Store) AppendAction(ctx context.Context, groupID uuid.UUID, investigationNodeID *uuid.UUID,
	clusterID, userID uuid.UUID, kind, ns, targetKind, targetName, sourceKind string, params json.RawMessage) (*model.Action, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	if sourceKind == "" {
		sourceKind = "builtin"
	}
	var a model.Action
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cluster_actions (cluster_id, requested_by, kind, target_namespace, target_kind, target_name, params,
		                             group_id, group_seq, investigation_node_id, source_kind)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,
		        (SELECT COALESCE(MAX(group_seq)+1,0) FROM cluster_actions WHERE group_id=$8),
		        $9,$10)
		RETURNING id, cluster_id, kind, target_namespace, target_kind, target_name, params, status, result, progress, steps,
		          group_id, group_seq, investigation_node_id, created_at, updated_at`,
		clusterID, userID, kind, ns, targetKind, targetName, params, groupID, investigationNodeID, sourceKind,
	).Scan(&a.ID, &a.ClusterID, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName, &a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps,
		&a.GroupID, &a.GroupSeq, &a.InvestigationNodeID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("append action to group: %w", err)
	}
	return &a, nil
}

// CreateAction legt eine pending-Aktion an. In v2 bekommt jede lose Aktion
// automatisch eine group-of-one (origin=manual_single), damit die Runs-Ansicht
// einheitlich als Trace rendert.
func (s *Store) CreateAction(ctx context.Context, clusterID, userID uuid.UUID, kind, ns, targetKind, targetName string, params json.RawMessage) (*model.Action, error) {
	title := kind
	if targetName != "" {
		title = kind + " " + targetName
	}
	g, err := s.CreateGroup(ctx, clusterID, userID, "manual_single", title, nil, nil, nil, "", "", "")
	if err != nil {
		return nil, err
	}
	return s.AppendAction(ctx, g.ID, nil, clusterID, userID, kind, ns, targetKind, targetName, "builtin", params)
}

// ListActions liefert die jüngsten Aktionen eines Clusters (optional nur für
// ein Target), neueste zuerst — der Feed im Workload-Panel.
func (s *Store) ListActions(ctx context.Context, clusterID uuid.UUID, ns, targetName string, limit int) ([]model.Action, error) {
	// 200 = Runs-Seite (Audit-Trail); außerhalb des Rahmens fällt auf 30 zurück.
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.cluster_id, COALESCE(u.email, ''), a.kind, a.target_namespace, a.target_kind, a.target_name,
		       a.params, a.status, a.result, a.progress, a.steps, a.revert, a.snapshot, a.cancel_requested,
		       a.group_id, a.group_seq, a.investigation_node_id, a.created_at, a.updated_at
		FROM cluster_actions a
		LEFT JOIN users u ON u.id = a.requested_by
		WHERE a.cluster_id = $1
		  AND ($2 = '' OR a.target_namespace = $2)
		  AND ($3 = '' OR a.target_name = $3 OR (a.kind = 'delete_pod' AND a.target_name LIKE $3 || '-%'))
		ORDER BY a.created_at DESC
		LIMIT $4`, clusterID, ns, targetName, limit)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()
	out := []model.Action{}
	for rows.Next() {
		var a model.Action
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.RequestedBy, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName,
			&a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.Revert, &a.Snapshot, &a.CancelRequested,
			&a.GroupID, &a.GroupSeq, &a.InvestigationNodeID, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ClaimPendingActions markiert bis zu limit pending-Aktionen des Clusters
// atomar als running und gibt sie zurück (Agent-Poll).
func (s *Store) ClaimPendingActions(ctx context.Context, clusterID uuid.UUID, limit int) ([]model.Action, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		UPDATE cluster_actions SET status = 'running', updated_at = now()
		WHERE id IN (
			SELECT id FROM cluster_actions
			WHERE cluster_id = $1 AND status = 'pending'
			ORDER BY created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, cluster_id, kind, target_namespace, target_kind, target_name, params, status, result, progress, steps, created_at, updated_at`,
		clusterID, limit)
	if err != nil {
		return nil, fmt.Errorf("claim actions: %w", err)
	}
	defer rows.Close()
	out := []model.Action{}
	for rows.Next() {
		var a model.Action
		if err := rows.Scan(&a.ID, &a.ClusterID, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName,
			&a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan claimed action: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateActionProgress records a live progress tick. In v4 a running tick may
// also carry the durable compensation: the agent reports the inverse the instant
// a mutation commits (not only at completion), so a mid-action agent crash still
// leaves a revertible row for ReapCrashedAgents. revert is COALESCEd — once set,
// a later tick without one does not erase it.
func (s *Store) UpdateActionProgress(ctx context.Context, clusterID, actionID uuid.UUID, progress string, steps, revert json.RawMessage) (bool, error) {
	if len(steps) == 0 {
		steps = json.RawMessage("[]")
	}
	var rev any
	if len(revert) > 0 {
		rev = revert
	}
	var cancel bool
	err := s.pool.QueryRow(ctx, `
		UPDATE cluster_actions SET progress = $3, steps = $4, revert = COALESCE($5, revert), updated_at = now()
		WHERE id = $2 AND cluster_id = $1 AND status = 'running'
		RETURNING cancel_requested`,
		clusterID, actionID, progress, steps, rev).Scan(&cancel)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("update action progress: %w", err)
	}
	return cancel, nil
}

// RequestCancel markiert eine Aktion zum Abbruch: pending → sofort cancelled
// (der Agent hat sie nie gesehen), running → cancel_requested (der Agent rollt
// beim nächsten Progress-Report zurück).
func (s *Store) RequestCancel(ctx context.Context, clusterID, actionID uuid.UUID) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		UPDATE cluster_actions SET
			cancel_requested = true,
			status   = CASE WHEN status = 'pending' THEN 'cancelled' ELSE status END,
			result   = CASE WHEN status = 'pending' THEN 'cancelled before start' ELSE result END,
			updated_at = now()
		WHERE id = $2 AND cluster_id = $1 AND status IN ('pending', 'running')
		RETURNING status`,
		clusterID, actionID).Scan(&status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("request cancel: %w", err)
	}
	return status, nil
}

// ForceCancel finalisiert eine hängende Aktion SOFORT als cancelled — ohne auf
// den Agenten zu warten. Der Nutzer-Notausgang: nichts bleibt dauerhaft hängen.
// (Der Agent, falls er noch lebt, beendet/rollt im Cluster weiter zurück; sein
// spätes Result trifft dann auf status!=running und wird verworfen.)
func (s *Store) ForceCancel(ctx context.Context, clusterID, actionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status='cancelled', cancel_requested=true,
			result='force-cancelled by user — the agent may still be rolling back in the cluster',
			progress='', updated_at=now()
		WHERE id = $2 AND cluster_id = $1 AND status IN ('pending','running')`,
		clusterID, actionID)
	if err != nil {
		return fmt.Errorf("force cancel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReapActions garantiert, dass KEINE Aktion ewig hängt — Cancel geht IMMER durch:
//  1. cancel_requested ohne Agent-Bestätigung (keine Updates > 90s) → cancelled.
//  2. running ohne jedes Progress-Update > 8m (Agent-monitorTimeout ist 3m —
//     ein lebender Agent meldet IMMER vorher) → failed (stale).
//  3. pending, das > 10m kein Agent geclaimt hat → cancelled (expired) — sonst
//     würde es unerwartet ausgeführt, wenn der Agent irgendwann zurückkommt.
func (s *Store) ReapActions(ctx context.Context) (int64, error) {
	var total int64
	// v4: a run whose agent DIED mid-mutation. Because the inverse was persisted
	// at commit, we finish the job for the operator — see ReapCrashedAgents.
	crashed, err := s.ReapCrashedAgents(ctx)
	if err != nil {
		return 0, err
	}
	total += crashed
	t1, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status='cancelled',
			result='cancel forced — the agent did not confirm within 90s (unreachable?)',
			progress='', updated_at=now()
		WHERE status='running' AND cancel_requested AND updated_at < now() - interval '90 seconds'`)
	if err != nil {
		return 0, fmt.Errorf("reap cancels: %w", err)
	}
	total += t1.RowsAffected()
	t2, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status='failed',
			result='stale — no progress from the agent for 8 minutes',
			progress='', updated_at=now()
		WHERE status='running' AND updated_at < now() - interval '8 minutes'`)
	if err != nil {
		return total, fmt.Errorf("reap stale: %w", err)
	}
	total += t2.RowsAffected()
	t3, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status='cancelled',
			result='expired — no agent claimed it within 10 minutes',
			updated_at=now()
		WHERE status='pending' AND created_at < now() - interval '10 minutes'`)
	if err != nil {
		return total, fmt.Errorf("reap pending: %w", err)
	}
	total += t3.RowsAffected()
	return total, nil
}

// ReapCrashedAgents handles what the stale-reaper cannot: a run that was
// MID-MUTATION when its agent died. The 8-minute stale clause would only mark it
// failed and leave the half-applied change standing. But in v4 the mutation's
// inverse is persisted the instant it is armed (the durable revert), so we can
// finish the job: mark the dead run crashed and enqueue its inverse as a fresh
// pending action in the SAME group, which the restarted (or a peer) agent claims
// and applies. The CP never touches the cluster — it only queues work
// (outbound-only).
//
// The crash signal is action-level, not cluster-level: a live agent reports
// progress every ~2s throughout observe/verify, so a `running` action that has
// gone silent for >90s means THAT run's goroutine is dead — even if the agent
// pod has already been restarted (k8s restarts it in seconds and its heartbeat
// resumes, so a cluster-level signal would never fire on the common case). The
// >90s window is far longer than any progress gap, so a mere GC pause is not
// reaped; and because the run already produced a durable revert, it had reached
// the point where a compensation is meaningful. A late report from a resurrected
// agent hits status!=running and is discarded, so there is no double-apply.
func (s *Store) ReapCrashedAgents(ctx context.Context) (int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.cluster_id, a.group_id, a.requested_by,
		       a.target_namespace, a.revert
		FROM cluster_actions a
		WHERE a.status = 'running'
		  AND a.revert IS NOT NULL
		  AND a.updated_at < now() - interval '90 seconds'`)
	if err != nil {
		return 0, fmt.Errorf("scan crashed runs: %w", err)
	}
	type crashed struct {
		id, clusterID, groupID uuid.UUID
		requestedBy            *uuid.UUID
		ns                     string
		revert                 []byte
	}
	var list []crashed
	for rows.Next() {
		var c crashed
		if err := rows.Scan(&c.id, &c.clusterID, &c.groupID, &c.requestedBy, &c.ns, &c.revert); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan crashed run: %w", err)
		}
		list = append(list, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var n int64
	for _, c := range list {
		// Claim the crash transition atomically — if another CP replica or the
		// agent's own late report got here first, status is no longer running
		// and we skip (no double-enqueue).
		tag, err := s.pool.Exec(ctx, `
			UPDATE cluster_actions SET status='failed',
				result='agent crashed mid-action — reverting from the durable compensation',
				progress='', updated_at=now()
			WHERE id=$1 AND status='running'`, c.id)
		if err != nil {
			return n, fmt.Errorf("mark crashed: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		// revert_status is a GROUP-level field (0028) — flag the group so the UI
		// shows the trace is being reverted.
		_, _ = s.pool.Exec(ctx, `UPDATE action_groups SET revert_status='reverting' WHERE id=$1`, c.groupID)
		var spec struct {
			Kind            string          `json:"kind"`
			TargetNamespace string          `json:"targetNamespace"`
			TargetKind      string          `json:"targetKind"`
			TargetName      string          `json:"targetName"`
			Params          json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(c.revert, &spec); err != nil || spec.Kind == "" {
			continue // no usable inverse — the run is marked failed, nothing to enqueue
		}
		ns := spec.TargetNamespace
		if ns == "" {
			ns = c.ns
		}
		if c.requestedBy == nil {
			continue // no owner to attribute the inverse to — marked failed, not auto-reverted
		}
		if _, err := s.AppendAction(ctx, c.groupID, nil, c.clusterID, *c.requestedBy,
			spec.Kind, ns, spec.TargetKind, spec.TargetName, "builtin", spec.Params); err != nil {
			return n, fmt.Errorf("enqueue inverse: %w", err)
		}
		n++
	}
	return n, nil
}

// CompleteAction setzt das Ergebnis einer running-Aktion (Agent-Callback).
func (s *Store) CompleteAction(ctx context.Context, clusterID, actionID uuid.UUID, status, result string, steps, revert json.RawMessage) error {
	if len(steps) == 0 {
		steps = json.RawMessage("[]")
	}
	var rev any
	if len(revert) > 0 {
		rev = revert
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET status = $3, result = $4, steps = $5, revert = COALESCE($6, revert), progress = '', updated_at = now()
		WHERE id = $2 AND cluster_id = $1 AND status = 'running'`,
		clusterID, actionID, status, result, steps, rev)
	if err != nil {
		return fmt.Errorf("complete action: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetActionSnapshot persistiert den generischen Before-Snapshot (gestripptes
// Zielobjekt) einer abgeschlossenen Mutation — Audit + restore_resource-Basis.
func (s *Store) SetActionSnapshot(ctx context.Context, clusterID, actionID uuid.UUID, snapshot json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE cluster_actions SET snapshot = $3, updated_at = now()
		WHERE id = $2 AND cluster_id = $1`, clusterID, actionID, snapshot)
	if err != nil {
		return fmt.Errorf("set action snapshot: %w", err)
	}
	return nil
}

// ListWorkloadPods liefert die Pods eines Workloads (für Panel + delete_pod).
func (s *Store) ListWorkloadPods(ctx context.Context, clusterID uuid.UUID, ns, workloadKind, workloadName string) ([]model.WorkloadPod, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT namespace, name, node_name, phase, ready, restarts, ip, first_seen_at
		FROM pods
		WHERE cluster_id = $1 AND namespace = $2 AND workload_kind = $3 AND workload_name = $4
		ORDER BY first_seen_at DESC, name`, clusterID, ns, workloadKind, workloadName)
	if err != nil {
		return nil, fmt.Errorf("list workload pods: %w", err)
	}
	defer rows.Close()
	out := []model.WorkloadPod{}
	for rows.Next() {
		var p model.WorkloadPod
		if err := rows.Scan(&p.Namespace, &p.Name, &p.NodeName, &p.Phase, &p.Ready, &p.Restarts, &p.IP, &p.FirstSeenAt); err != nil {
			return nil, fmt.Errorf("scan workload pod: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
