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

// enrollTokenTTL is how long a freshly issued enroll (registration) token stays
// valid. It is reusable within this window (see RedeemEnrollToken) so agent Pods
// can re-enroll across restarts — hence a generous, install-token-style TTL.
const enrollTokenTTL = 30 * 24 * time.Hour

const clusterCols = `id, org_id, name, COALESCE(k8s_uid, ''), status, agent_version, last_seen_at, created_at`

func scanCluster(row pgx.Row, c *model.Cluster) error {
	return row.Scan(&c.ID, &c.OrgID, &c.Name, &c.K8sUID, &c.Status, &c.AgentVersion, &c.LastSeenAt, &c.CreatedAt)
}

// ListClusters returns all clusters of an org.
func (s *Store) ListClusters(ctx context.Context, orgID uuid.UUID) ([]model.Cluster, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+clusterCols+` FROM clusters WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	defer rows.Close()

	clusters := []model.Cluster{}
	for rows.Next() {
		var c model.Cluster
		if err := scanCluster(rows, &c); err != nil {
			return nil, fmt.Errorf("scan cluster: %w", err)
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// GetClusterWithNamespaces loads one cluster (scoped to the org) and its synced
// namespaces.
func (s *Store) GetClusterWithNamespaces(ctx context.Context, orgID, clusterID uuid.UUID) (*model.Cluster, []model.Namespace, error) {
	var c model.Cluster
	err := scanCluster(s.pool.QueryRow(ctx,
		`SELECT `+clusterCols+` FROM clusters WHERE id=$1 AND org_id=$2`, clusterID, orgID), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get cluster: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, k8s_uid, phase, labels, first_seen_at, last_seen_at
		FROM namespaces WHERE cluster_id=$1 ORDER BY name`, clusterID)
	if err != nil {
		return nil, nil, fmt.Errorf("list namespaces: %w", err)
	}
	defer rows.Close()

	namespaces := []model.Namespace{}
	for rows.Next() {
		var (
			ns        model.Namespace
			labelsRaw []byte
		)
		if err := rows.Scan(&ns.ID, &ns.Name, &ns.K8sUID, &ns.Phase, &labelsRaw, &ns.FirstSeenAt, &ns.LastSeenAt); err != nil {
			return nil, nil, fmt.Errorf("scan namespace: %w", err)
		}
		ns.Labels = map[string]string{}
		if len(labelsRaw) > 0 {
			if err := json.Unmarshal(labelsRaw, &ns.Labels); err != nil {
				return nil, nil, fmt.Errorf("decode namespace labels: %w", err)
			}
		}
		namespaces = append(namespaces, ns)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate namespaces: %w", err)
	}
	return &c, namespaces, nil
}

// CreateClusterWithEnrollToken creates a pending cluster and issues its first
// enroll token. The plaintext token is returned exactly once.
func (s *Store) CreateClusterWithEnrollToken(ctx context.Context, orgID uuid.UUID, name string, userID uuid.UUID) (*model.Cluster, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var c model.Cluster
	if err := scanCluster(tx.QueryRow(ctx, `
		INSERT INTO clusters (org_id, name, status, created_by)
		VALUES ($1, $2, 'pending', $3)
		RETURNING `+clusterCols, orgID, name, userID), &c); err != nil {
		return nil, "", fmt.Errorf("insert cluster: %w", err)
	}

	plain, err := insertEnrollToken(ctx, tx, orgID, c.ID, userID)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit cluster: %w", err)
	}
	return &c, plain, nil
}

// NewEnrollToken issues a fresh enroll token for an existing cluster (reconnect).
func (s *Store) NewEnrollToken(ctx context.Context, orgID, clusterID, userID uuid.UUID) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	plain, err := insertEnrollToken(ctx, tx, orgID, clusterID, userID)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit enroll token: %w", err)
	}
	return plain, nil
}

func insertEnrollToken(ctx context.Context, tx pgx.Tx, orgID, clusterID, userID uuid.UUID) (string, error) {
	plain, hash, err := newToken(enrollTokenPrefix)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO enrollment_tokens (org_id, cluster_id, token_hash, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		orgID, clusterID, hash, userID, time.Now().Add(enrollTokenTTL)); err != nil {
		return "", fmt.Errorf("insert enroll token: %w", err)
	}
	return plain, nil
}

// RedeemEnrollToken validates an enroll token and connects the cluster: it sets
// k8s_uid/status/agent_version, records a long-lived agent credential and stamps
// the token's last-redeemed time — all atomically. Returns the cluster id and the
// plaintext agent token (returned exactly once).
//
// The enroll token is a REUSABLE cluster-registration token (valid until
// expires_at), not a one-shot code — this is the industry-standard install-token
// pattern (cf. Datadog/Grafana Alloy). It makes re-enrollment on every agent Pod
// restart idempotent: an ephemeral DaemonSet Pod has no persistent volume to cache
// its agent token, so it re-enrolls each boot. A single-use token would 401 on the
// second restart and silently break the sync. used_at is kept as a
// "last-redeemed-at" observability marker, not an authorization gate.
func (s *Store) RedeemEnrollToken(ctx context.Context, tokenPlain, k8sUID, clusterName, agentVersion string) (uuid.UUID, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		tokenID   uuid.UUID
		clusterID *uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT id, cluster_id FROM enrollment_tokens
		WHERE token_hash=$1 AND expires_at > now()
		FOR UPDATE`, hashToken(tokenPlain)).Scan(&tokenID, &clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("lookup enroll token: %w", err)
	}
	if clusterID == nil {
		return uuid.Nil, "", ErrInvalidToken
	}

	// Idempotenz: Gehört die kube-system-UID bereits einem ANDEREN Cluster derselben
	// Org (Doppel-Connect zum selben physischen Cluster ODER Re-Enroll nach Agent-
	// Restart mit einem frisch angelegten pending-Cluster), dann den EXISTIERENDEN
	// Cluster wiederverwenden und das gerade eingelöste pending-Duplikat verwerfen.
	// Das macht Re-Enroll robust, statt den Agent an der globalen UID-Uniqueness zu
	// blockieren. Cross-Org bleibt hart getrennt (nur gleiche Org wird zusammengeführt).
	var existingID uuid.UUID
	if e := tx.QueryRow(ctx, `
		SELECT c.id FROM clusters c
		WHERE c.k8s_uid=$1
		  AND c.org_id = (SELECT org_id FROM clusters WHERE id=$2)`,
		k8sUID, *clusterID).Scan(&existingID); e == nil && existingID != *clusterID {
		if _, err := tx.Exec(ctx, `DELETE FROM clusters WHERE id=$1 AND status='pending'`, *clusterID); err != nil {
			return uuid.Nil, "", fmt.Errorf("prune duplicate cluster: %w", err)
		}
		clusterID = &existingID
	}

	tag, err := tx.Exec(ctx, `
		UPDATE clusters
		SET k8s_uid=$2,
		    status='connected',
		    agent_version=$3,
		    last_seen_at=now(),
		    name=CASE WHEN name='' THEN $4 ELSE name END
		WHERE id=$1`, *clusterID, k8sUID, agentVersion, clusterName)
	if err != nil {
		if isUniqueViolation(err) {
			// Globale UID-Uniqueness: dieser physische Cluster ist bereits verbunden.
			return uuid.Nil, "", fmt.Errorf("this cluster is already connected to rocketplane")
		}
		return uuid.Nil, "", fmt.Errorf("connect cluster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return uuid.Nil, "", ErrInvalidToken
	}

	// Stamp last-redeemed time (observability only — the token stays reusable).
	if _, err := tx.Exec(ctx, `UPDATE enrollment_tokens SET used_at=now() WHERE id=$1`, tokenID); err != nil {
		return uuid.Nil, "", fmt.Errorf("stamp enroll token: %w", err)
	}

	agentPlain, agentHash, err := newToken(agentTokenPrefix)
	if err != nil {
		return uuid.Nil, "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO cluster_credentials (cluster_id, token_hash) VALUES ($1, $2)`,
		*clusterID, agentHash); err != nil {
		return uuid.Nil, "", fmt.Errorf("insert agent credential: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, "", fmt.Errorf("commit enroll: %w", err)
	}
	return *clusterID, agentPlain, nil
}

// AuthAgentToken resolves a plaintext agent token to its cluster id (only for
// non-revoked credentials).
func (s *Store) AuthAgentToken(ctx context.Context, tokenPlain string) (uuid.UUID, error) {
	var clusterID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT cluster_id FROM cluster_credentials
		WHERE token_hash=$1 AND revoked_at IS NULL
		LIMIT 1`, hashToken(tokenPlain)).Scan(&clusterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidToken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth agent token: %w", err)
	}
	return clusterID, nil
}

// TouchCluster updates the cluster's liveness (last_seen_at, status=connected)
// and — if provided — its reported agent version.
func (s *Store) TouchCluster(ctx context.Context, clusterID uuid.UUID, agentVersion string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE clusters
		SET last_seen_at=now(),
		    status='connected',
		    agent_version=CASE WHEN $2 <> '' THEN $2 ELSE agent_version END
		WHERE id=$1`, clusterID, agentVersion)
	if err != nil {
		return fmt.Errorf("touch cluster: %w", err)
	}
	return nil
}

// SyncNamespaces performs a full sync: upsert every provided namespace by
// (cluster_id, name) and delete rows that are no longer reported.
func (s *Store) SyncNamespaces(ctx context.Context, clusterID uuid.UUID, namespaces []model.Namespace) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	names := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		labels := ns.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labelsJSON, err := json.Marshal(labels)
		if err != nil {
			return fmt.Errorf("encode labels for %s: %w", ns.Name, err)
		}
		phase := ns.Phase
		if phase == "" {
			phase = "Active"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO namespaces (cluster_id, name, k8s_uid, phase, labels, first_seen_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, now(), now())
			ON CONFLICT (cluster_id, name) DO UPDATE
			SET k8s_uid=EXCLUDED.k8s_uid,
			    phase=EXCLUDED.phase,
			    labels=EXCLUDED.labels,
			    last_seen_at=now()`,
			clusterID, ns.Name, ns.K8sUID, phase, string(labelsJSON)); err != nil {
			return fmt.Errorf("upsert namespace %s: %w", ns.Name, err)
		}
		names = append(names, ns.Name)
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM namespaces WHERE cluster_id=$1 AND name <> ALL($2)`,
		clusterID, names); err != nil {
		return fmt.Errorf("prune namespaces: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit namespaces: %w", err)
	}
	return nil
}
