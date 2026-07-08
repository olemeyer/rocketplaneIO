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

// CreateAction legt eine pending-Aktion an und gibt sie (mit ID) zurück.
func (s *Store) CreateAction(ctx context.Context, clusterID, userID uuid.UUID, kind, ns, targetKind, targetName string, params json.RawMessage) (*model.Action, error) {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var a model.Action
	err := s.pool.QueryRow(ctx, `
		INSERT INTO cluster_actions (cluster_id, requested_by, kind, target_namespace, target_kind, target_name, params)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, cluster_id, kind, target_namespace, target_kind, target_name, params, status, result, progress, steps, created_at, updated_at`,
		clusterID, userID, kind, ns, targetKind, targetName, params,
	).Scan(&a.ID, &a.ClusterID, &a.Kind, &a.TargetNamespace, &a.TargetKind, &a.TargetName, &a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert action: %w", err)
	}
	return &a, nil
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
		       a.params, a.status, a.result, a.progress, a.steps, a.revert, a.snapshot, a.cancel_requested, a.created_at, a.updated_at
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
			&a.Params, &a.Status, &a.Result, &a.Progress, &a.Steps, &a.Revert, &a.Snapshot, &a.CancelRequested, &a.CreatedAt, &a.UpdatedAt); err != nil {
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

// UpdateActionProgress aktualisiert die Live-Zeile + Steps einer laufenden
// Aktion und liefert zurück, ob der User inzwischen CANCEL angefordert hat —
// der outbound-only-Rückkanal an den Agenten.
func (s *Store) UpdateActionProgress(ctx context.Context, clusterID, actionID uuid.UUID, progress string, steps json.RawMessage) (bool, error) {
	if len(steps) == 0 {
		steps = json.RawMessage("[]")
	}
	var cancel bool
	err := s.pool.QueryRow(ctx, `
		UPDATE cluster_actions SET progress = $3, steps = $4, updated_at = now()
		WHERE id = $2 AND cluster_id = $1 AND status = 'running'
		RETURNING cancel_requested`,
		clusterID, actionID, progress, steps).Scan(&cancel)
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
