package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// infra.go — Nodes + PVCs: Full-Sync-Persistenz (Teil des Topologie-Syncs)
// und die Reads für den Infrastructure-Bereich.

// syncInfra upsertet Nodes + PVCs innerhalb der Topologie-Transaktion.
func syncInfra(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID, t model.TopologySync) error {
	seenNodes := make([]string, 0, len(t.Nodes))
	for _, n := range t.Nodes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO nodes (cluster_id, name, role, kubelet_version, os_image, arch, internal_ip, ready, pressure,
				cpu_capacity_m, cpu_allocatable_m, mem_capacity, mem_allocatable, pod_capacity,
				cpu_usage_m, mem_usage, fs_used, fs_capacity, image_fs_used, unschedulable, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20, now())
			ON CONFLICT (cluster_id, name) DO UPDATE SET
				role=$3, kubelet_version=$4, os_image=$5, arch=$6, internal_ip=$7, ready=$8, pressure=$9,
				cpu_capacity_m=$10, cpu_allocatable_m=$11, mem_capacity=$12, mem_allocatable=$13, pod_capacity=$14,
				cpu_usage_m=$15, mem_usage=$16, fs_used=$17, fs_capacity=$18, image_fs_used=$19, unschedulable=$20, last_seen_at=now()`,
			clusterID, n.Name, n.Role, n.KubeletVersion, n.OSImage, n.Arch, n.InternalIP, n.Ready, n.Pressure,
			n.CPUCapacityM, n.CPUAllocatableM, n.MemCapacity, n.MemAllocatable, n.PodCapacity,
			n.CPUUsageM, n.MemUsage, n.FsUsed, n.FsCapacity, n.ImageFsUsed, n.Unschedulable); err != nil {
			return fmt.Errorf("upsert node: %w", err)
		}
		seenNodes = append(seenNodes, n.Name)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM nodes WHERE cluster_id=$1 AND name <> ALL($2::text[])`,
		clusterID, seenNodes); err != nil {
		return fmt.Errorf("prune nodes: %w", err)
	}

	seenPVCs := make([]string, 0, len(t.PVCs))
	for _, c := range t.PVCs {
		am := c.AccessModes
		if am == nil {
			am = []string{}
		}
		mb := c.MountedBy
		if mb == nil {
			mb = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pvcs (cluster_id, namespace, name, phase, storage_class, access_modes, volume_name,
				requested_bytes, capacity_bytes, used_bytes, mounted_by, last_seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
			ON CONFLICT (cluster_id, namespace, name) DO UPDATE SET
				phase=$4, storage_class=$5, access_modes=$6, volume_name=$7,
				requested_bytes=$8, capacity_bytes=$9, used_bytes=$10, mounted_by=$11, last_seen_at=now()`,
			clusterID, c.Namespace, c.Name, c.Phase, c.StorageClass, am, c.VolumeName,
			c.RequestedBytes, c.CapacityBytes, c.UsedBytes, mb); err != nil {
			return fmt.Errorf("upsert pvc: %w", err)
		}
		seenPVCs = append(seenPVCs, c.Namespace+"/"+c.Name)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM pvcs WHERE cluster_id=$1 AND (namespace || '/' || name) <> ALL($2::text[])`,
		clusterID, seenPVCs); err != nil {
		return fmt.Errorf("prune pvcs: %w", err)
	}
	return nil
}

// InfraNodes liefert die Nodes inkl. Pod-Zahl (aus der pods-Tabelle).
func (s *Store) InfraNodes(ctx context.Context, clusterID uuid.UUID) ([]model.NodeSync, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.name, n.role, n.kubelet_version, n.os_image, n.arch, n.internal_ip, n.ready, n.unschedulable, n.pressure,
		       n.cpu_capacity_m, n.cpu_allocatable_m, n.mem_capacity, n.mem_allocatable, n.pod_capacity,
		       n.cpu_usage_m, n.mem_usage, n.fs_used, n.fs_capacity, n.image_fs_used,
		       (SELECT count(*) FROM pods p WHERE p.cluster_id=n.cluster_id AND p.node_name=n.name)
		FROM nodes n WHERE n.cluster_id=$1 ORDER BY n.name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("infra nodes: %w", err)
	}
	defer rows.Close()
	out := []model.NodeSync{}
	for rows.Next() {
		var n model.NodeSync
		if err := rows.Scan(&n.Name, &n.Role, &n.KubeletVersion, &n.OSImage, &n.Arch, &n.InternalIP, &n.Ready, &n.Unschedulable, &n.Pressure,
			&n.CPUCapacityM, &n.CPUAllocatableM, &n.MemCapacity, &n.MemAllocatable, &n.PodCapacity,
			&n.CPUUsageM, &n.MemUsage, &n.FsUsed, &n.FsCapacity, &n.ImageFsUsed, &n.PodCount); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// InfraPVCs liefert die PVCs des Clusters.
func (s *Store) InfraPVCs(ctx context.Context, clusterID uuid.UUID) ([]model.PVCSync, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT namespace, name, phase, storage_class, access_modes, volume_name,
		       requested_bytes, capacity_bytes, used_bytes, mounted_by
		FROM pvcs WHERE cluster_id=$1 ORDER BY namespace, name`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("infra pvcs: %w", err)
	}
	defer rows.Close()
	out := []model.PVCSync{}
	for rows.Next() {
		var c model.PVCSync
		if err := rows.Scan(&c.Namespace, &c.Name, &c.Phase, &c.StorageClass, &c.AccessModes, &c.VolumeName,
			&c.RequestedBytes, &c.CapacityBytes, &c.UsedBytes, &c.MountedBy); err != nil {
			return nil, fmt.Errorf("scan pvc: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
