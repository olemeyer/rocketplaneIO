package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// topology.go persistiert die vom Agent gesyncte Cluster-Topologie (Pods, Services,
// abgeleitete Workloads) und aggregiert sie mit den Flows zur Service-Map.

const (
	// flowRetention: so lange bleibt eine nicht mehr beobachtete Flow-Kante gespeichert.
	flowRetention = "1 hour"
	// flowFreshness: nur Kanten, die innerhalb dieses Fensters zuletzt gesehen wurden,
	// erscheinen in der Service-Map (verhindert Geister-Kanten bei versiegtem Traffic).
	flowFreshness = "5 minutes"
)

// SyncTopology übernimmt einen Full-Sync von Pods + Services eines Clusters und
// leitet daraus die Workloads ab (ein Workload = namespace/kind/name der Pod-Owner).
// Full-Sync: Nicht mehr gelieferte Zeilen werden entfernt (transaktional).
func (s *Store) SyncTopology(ctx context.Context, clusterID uuid.UUID, t model.TopologySync) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// ── Pods upsert + stale löschen ──
	seenPods := make([][2]string, 0, len(t.Pods)) // (namespace, name)
	for _, p := range t.Pods {
		if _, err := tx.Exec(ctx, `
			INSERT INTO pods (cluster_id, namespace, name, ip, node_name, phase, ready, restarts, workload_kind, workload_name, image, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
			ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
				ip=$4, node_name=$5, phase=$6, ready=$7, restarts=$8, workload_kind=$9, workload_name=$10, image=$11, last_seen_at=now()`,
			clusterID, p.Namespace, p.Name, p.IP, p.NodeName, p.Phase, p.Ready, p.Restarts, p.WorkloadKind, p.WorkloadName, p.Image); err != nil {
			return fmt.Errorf("upsert pod: %w", err)
		}
		seenPods = append(seenPods, [2]string{p.Namespace, p.Name})
	}
	if err := deleteMissing(ctx, tx, "pods", clusterID, seenPods); err != nil {
		return err
	}

	// ── Workloads aus Pods ableiten (aggregiert je namespace/kind/name) ──
	type wl struct {
		ns, kind, name         string
		total, ready, restarts int
	}
	agg := map[string]*wl{}
	for _, p := range t.Pods {
		kind, name := p.WorkloadKind, p.WorkloadName
		if kind == "" || name == "" {
			kind, name = "Pod", p.Name // ownerlose Pods als eigener Knoten
		}
		key := p.Namespace + "/" + kind + "/" + name
		w := agg[key]
		if w == nil {
			w = &wl{ns: p.Namespace, kind: kind, name: name}
			agg[key] = w
		}
		w.total++
		if p.Ready {
			w.ready++
		}
		w.restarts += p.Restarts
	}
	// Explizit gesyncte Workloads (direkt vom Objekt) ÜBERSCHREIBEN die
	// pod-abgeleiteten Werte: spec.replicas ist die Wahrheit für desired —
	// so bleibt ein scaled-to-zero-Workload als 0/0-Knoten auf der Map,
	// statt mangels Pods gelöscht zu werden.
	for _, ws := range t.Workloads {
		key := ws.Namespace + "/" + ws.Kind + "/" + ws.Name
		agg[key] = &wl{
			ns: ws.Namespace, kind: ws.Kind, name: ws.Name,
			total: ws.ReplicasDesired, ready: ws.ReplicasReady,
			restarts: func() int {
				if w := agg[key]; w != nil {
					return w.restarts
				}
				return 0
			}(),
		}
	}
	seenWl := make([][3]string, 0, len(agg))
	for _, w := range agg {
		if _, err := tx.Exec(ctx, `
			INSERT INTO workloads (cluster_id, namespace, name, kind, replicas_desired, replicas_ready, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6, now())
			ON CONFLICT (cluster_id, namespace, kind, name) DO UPDATE SET
				replicas_desired=$5, replicas_ready=$6, last_seen_at=now()`,
			clusterID, w.ns, w.name, w.kind, w.total, w.ready); err != nil {
			return fmt.Errorf("upsert workload: %w", err)
		}
		seenWl = append(seenWl, [3]string{w.ns, w.kind, w.name})
	}
	if err := deleteMissingWorkloads(ctx, tx, clusterID, seenWl); err != nil {
		return err
	}

	// ── Services upsert + stale löschen ──
	seenSvc := make([][2]string, 0, len(t.Services))
	for _, sv := range t.Services {
		sel, _ := json.Marshal(sv.Selector)
		if _, err := tx.Exec(ctx, `
			INSERT INTO k8s_services (cluster_id, namespace, name, type, cluster_ip, selector, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6, now())
			ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
				type=$4, cluster_ip=$5, selector=$6, last_seen_at=now()`,
			clusterID, sv.Namespace, sv.Name, sv.Type, sv.ClusterIP, string(sel)); err != nil {
			return fmt.Errorf("upsert service: %w", err)
		}
		seenSvc = append(seenSvc, [2]string{sv.Namespace, sv.Name})
	}
	if err := deleteMissing(ctx, tx, "k8s_services", clusterID, seenSvc); err != nil {
		return err
	}

	// ── Flows: rollierendes Aging statt Snapshot-Replace ──
	// conntrack ist eine Momentaufnahme (ein 120s-TIME_WAIT-Fenster). Ein einzelner
	// Zyklus ohne Treffer würde bei Snapshot-Replace die GANZE Map wegwischen (Flicker).
	// Stattdessen: je Kante die zuletzt beobachtete Verbindungsintensität + last_seen_at
	// upserten (nicht kumulieren — das Sample ist bereits ein Fenster), im Query nur
	// frische Kanten zeigen und alte Kanten periodisch ausaltern.
	for _, e := range t.Edges {
		if e.FromName == "" || e.ToName == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO flows (cluster_id, from_namespace, from_kind, from_name, to_namespace, to_kind, to_name, to_port, conn_count, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
			ON CONFLICT (cluster_id, from_namespace, from_kind, from_name, to_namespace, to_kind, to_name, to_port)
			DO UPDATE SET conn_count = EXCLUDED.conn_count, last_seen_at = now()`,
			clusterID, e.FromNamespace, e.FromKind, e.FromName, e.ToNamespace, e.ToKind, e.ToName, e.ToPort, e.ConnCount); err != nil {
			return fmt.Errorf("insert flow: %w", err)
		}
	}
	// Ausgealterte Kanten entfernen (nicht mehr beobachtet seit flowRetention).
	if _, err := tx.Exec(ctx,
		`DELETE FROM flows WHERE cluster_id=$1 AND last_seen_at < now() - $2::interval`,
		clusterID, flowRetention); err != nil {
		return fmt.Errorf("prune flows: %w", err)
	}

	// Infrastruktur (Nodes + PVCs) — Teil desselben Full-Syncs.
	if err := syncInfra(ctx, tx, clusterID, t); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// deleteMissing entfernt (namespace,name)-Zeilen einer Tabelle, die nicht im
// Full-Sync enthalten waren.
func deleteMissing(ctx context.Context, tx pgx.Tx, table string, clusterID uuid.UUID, seen [][2]string) error {
	ns := make([]string, len(seen))
	nm := make([]string, len(seen))
	for i, s := range seen {
		ns[i], nm[i] = s[0], s[1]
	}
	// Zeilen löschen, deren (namespace||'/'||name) nicht im gelieferten Set ist.
	_, err := tx.Exec(ctx, `
		DELETE FROM `+table+` t
		WHERE t.cluster_id = $1
		  AND (t.namespace || '/' || t.name) <> ALL ($2::text[])`,
		clusterID, joinKeys(ns, nm))
	if err != nil {
		return fmt.Errorf("prune %s: %w", table, err)
	}
	return nil
}

func deleteMissingWorkloads(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID, seen [][3]string) error {
	keys := make([]string, len(seen))
	for i, s := range seen {
		keys[i] = s[0] + "/" + s[1] + "/" + s[2]
	}
	_, err := tx.Exec(ctx, `
		DELETE FROM workloads w
		WHERE w.cluster_id = $1
		  AND (w.namespace || '/' || w.kind || '/' || w.name) <> ALL ($2::text[])`,
		clusterID, keys)
	if err != nil {
		return fmt.Errorf("prune workloads: %w", err)
	}
	return nil
}

// joinKeys baut "namespace/name"-Schlüssel für den ALL-Vergleich.
func joinKeys(ns, nm []string) []string {
	out := make([]string, len(ns))
	for i := range ns {
		out[i] = ns[i] + "/" + nm[i]
	}
	return out
}

// ServiceMap aggregiert Workloads (Knoten) + Flows (Kanten) eines Clusters.
func (s *Store) ServiceMap(ctx context.Context, clusterID uuid.UUID) (model.ServiceMap, error) {
	out := model.ServiceMap{Namespaces: []string{}, Nodes: []model.MapNode{}, Edges: []model.MapEdge{}}

	rows, err := s.pool.Query(ctx, `
		SELECT namespace, kind, name, replicas_desired, replicas_ready,
		       COALESCE((SELECT sum(restarts) FROM pods p
		                 WHERE p.cluster_id=w.cluster_id AND p.namespace=w.namespace
		                   AND p.workload_kind=w.kind AND p.workload_name=w.name), 0) AS restarts,
		       COALESCE((SELECT max(image) FROM pods p
		                 WHERE p.cluster_id=w.cluster_id AND p.namespace=w.namespace
		                   AND p.workload_kind=w.kind AND p.workload_name=w.name), '') AS image,
		       w.icon
		FROM workloads w WHERE cluster_id=$1
		ORDER BY namespace, name`, clusterID)
	if err != nil {
		return out, fmt.Errorf("map nodes: %w", err)
	}
	defer rows.Close()

	nsSet := map[string]bool{}
	for rows.Next() {
		var n model.MapNode
		var restarts int
		if err := rows.Scan(&n.Namespace, &n.Kind, &n.Name, &n.PodsTotal, &n.PodsReady, &restarts, &n.Image, &n.Icon); err != nil {
			return out, fmt.Errorf("scan node: %w", err)
		}
		n.ID = n.Namespace + "/" + n.Kind + "/" + n.Name
		n.Restarts = restarts
		n.Health = deriveHealth(n.PodsReady, n.PodsTotal, restarts)
		out.Nodes = append(out.Nodes, n)
		nsSet[n.Namespace] = true
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for ns := range nsSet {
		out.Namespaces = append(out.Namespaces, ns)
	}

	// Kanten aus flows — nur frisch beobachtete (Aging), über Ports aggregiert.
	erows, err := s.pool.Query(ctx, `
		SELECT from_namespace, from_kind, from_name, to_namespace, to_kind, to_name, sum(conn_count)
		FROM flows
		WHERE cluster_id=$1 AND last_seen_at > now() - $2::interval
		GROUP BY from_namespace, from_kind, from_name, to_namespace, to_kind, to_name`, clusterID, flowFreshness)
	if err != nil {
		return out, fmt.Errorf("map edges: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var fns, fk, fn, tns, tk, tn string
		var cc int64
		if err := erows.Scan(&fns, &fk, &fn, &tns, &tk, &tn, &cc); err != nil {
			return out, fmt.Errorf("scan edge: %w", err)
		}
		out.Edges = append(out.Edges, model.MapEdge{
			From: fns + "/" + fk + "/" + fn, To: tns + "/" + tk + "/" + tn, ConnCount: cc,
		})
	}
	return out, erows.Err()
}

// deriveHealth klassifiziert einen Workload aus Ready-Verhältnis + Restarts.
func deriveHealth(ready, total, restarts int) string {
	if total == 0 {
		return "unknown"
	}
	if ready == 0 {
		return "critical"
	}
	if ready < total || restarts >= 5 {
		return "degraded"
	}
	return "healthy"
}

// SetWorkloadIcon persistiert den manuellen Icon-Override eines Workloads.
func (s *Store) SetWorkloadIcon(ctx context.Context, clusterID uuid.UUID, ns, kind, name, icon string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE workloads SET icon=$5 WHERE cluster_id=$1 AND namespace=$2 AND kind=$3 AND name=$4`,
		clusterID, ns, kind, name, icon)
	if err != nil {
		return fmt.Errorf("set workload icon: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
