package sync

// infra.go — Infrastruktur-Sicht für den Infrastructure-Bereich der UI:
// Nodes (Kapazität + ECHTE Auslastung aus der kubelet stats/summary API via
// API-Server-Proxy — kein metrics-server nötig) und PVCs (inkl. echter
// Volume-Belegung aus denselben Stats + wer sie mountet).

import (
	"context"
	"encoding/json"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const statsRefresh = 25 * time.Second

// nodeItem spiegelt model.NodeSync.
type nodeItem struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	KubeletVersion  string `json:"kubeletVersion"`
	OSImage         string `json:"osImage"`
	Arch            string `json:"arch"`
	InternalIP      string `json:"internalIp"`
	Ready           bool   `json:"ready"`
	Unschedulable   bool   `json:"unschedulable"`
	Pressure        string `json:"pressure"` // "", "memory", "disk", "pid" (kommasepariert)
	CPUCapacityM    int64  `json:"cpuCapacityM"`
	CPUAllocatableM int64  `json:"cpuAllocatableM"`
	MemCapacity     int64  `json:"memCapacity"`
	MemAllocatable  int64  `json:"memAllocatable"`
	PodCapacity     int64  `json:"podCapacity"`
	CPUUsageM       int64  `json:"cpuUsageM"`   // -1 = unbekannt
	MemUsage        int64  `json:"memUsage"`    // -1 = unbekannt
	FsUsed          int64  `json:"fsUsed"`      // -1 = unbekannt
	FsCapacity      int64  `json:"fsCapacity"`  // -1 = unbekannt
	ImageFsUsed     int64  `json:"imageFsUsed"` // -1 = unbekannt
}

// pvcItem spiegelt model.PVCSync.
type pvcItem struct {
	Namespace     string   `json:"namespace"`
	Name          string   `json:"name"`
	Phase         string   `json:"phase"`
	StorageClass  string   `json:"storageClass"`
	AccessModes   []string `json:"accessModes"`
	VolumeName    string   `json:"volumeName"`
	RequestedByte int64    `json:"requestedBytes"`
	CapacityByte  int64    `json:"capacityBytes"`
	UsedBytes     int64    `json:"usedBytes"` // -1 = unbekannt (kein Mount/Stats)
	MountedBy     []string `json:"mountedBy"` // Workload-Namen
}

/* ── kubelet stats/summary (via API-Server nodes/proxy) ─────────────────── */

type statsSummary struct {
	Node struct {
		CPU struct {
			UsageNanoCores int64 `json:"usageNanoCores"`
		} `json:"cpu"`
		Memory struct {
			WorkingSetBytes int64 `json:"workingSetBytes"`
		} `json:"memory"`
		Fs struct {
			UsedBytes     int64 `json:"usedBytes"`
			CapacityBytes int64 `json:"capacityBytes"`
		} `json:"fs"`
		Runtime struct {
			ImageFs struct {
				UsedBytes int64 `json:"usedBytes"`
			} `json:"imageFs"`
		} `json:"runtime"`
	} `json:"node"`
	Pods []struct {
		Volume []struct {
			UsedBytes     int64 `json:"usedBytes"`
			CapacityBytes int64 `json:"capacityBytes"`
			PVCRef        *struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"pvcRef"`
		} `json:"volume"`
	} `json:"pods"`
}

type nodeStats struct {
	fetchedAt time.Time
	summary   statsSummary
}

// fetchStats holt stats/summary eines Nodes (gecached, statsRefresh).
func (s *Syncer) fetchStats(ctx context.Context, nodeName string) *statsSummary {
	if s.statsCache == nil {
		s.statsCache = map[string]nodeStats{}
	}
	if c, ok := s.statsCache[nodeName]; ok && time.Since(c.fetchedAt) < statsRefresh {
		return &c.summary
	}
	raw, err := s.clientset.CoreV1().RESTClient().Get().
		Resource("nodes").Name(nodeName).SubResource("proxy").
		Suffix("stats/summary").DoRaw(ctx)
	if err != nil {
		log.Printf("infra: stats/summary %s failed: %v", nodeName, err)
		return nil
	}
	var sum statsSummary
	if err := json.Unmarshal(raw, &sum); err != nil {
		return nil
	}
	s.statsCache[nodeName] = nodeStats{fetchedAt: time.Now(), summary: sum}
	return &sum
}

/* ── Builder ────────────────────────────────────────────────────────────── */

func nodeRole(n *corev1.Node) string {
	for l := range n.Labels {
		if l == "node-role.kubernetes.io/control-plane" || l == "node-role.kubernetes.io/master" {
			return "control-plane"
		}
	}
	return "worker"
}

// buildNodes liest Kapazität/Conditions aus den Node-Objekten und ECHTE
// Auslastung aus stats/summary. pvcUsage sammelt nebenbei die Volume-
// Belegung je "namespace/name" für buildPVCs.
func (s *Syncer) buildNodes(ctx context.Context, nodes []*corev1.Node) ([]nodeItem, map[string][2]int64) {
	out := make([]nodeItem, 0, len(nodes))
	pvcUsage := map[string][2]int64{} // ns/name → [used, capacity]
	for _, n := range nodes {
		it := nodeItem{
			Name:            n.Name,
			Role:            nodeRole(n),
			Unschedulable:   n.Spec.Unschedulable,
			KubeletVersion:  n.Status.NodeInfo.KubeletVersion,
			OSImage:         n.Status.NodeInfo.OSImage,
			Arch:            n.Status.NodeInfo.Architecture,
			CPUCapacityM:    n.Status.Capacity.Cpu().MilliValue(),
			CPUAllocatableM: n.Status.Allocatable.Cpu().MilliValue(),
			MemCapacity:     n.Status.Capacity.Memory().Value(),
			MemAllocatable:  n.Status.Allocatable.Memory().Value(),
			PodCapacity:     n.Status.Capacity.Pods().Value(),
			CPUUsageM:       -1,
			MemUsage:        -1,
			FsUsed:          -1,
			FsCapacity:      -1,
			ImageFsUsed:     -1,
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				it.InternalIP = a.Address
			}
		}
		pressure := ""
		for _, c := range n.Status.Conditions {
			switch c.Type {
			case corev1.NodeReady:
				it.Ready = c.Status == corev1.ConditionTrue
			case corev1.NodeMemoryPressure:
				if c.Status == corev1.ConditionTrue {
					pressure += "memory,"
				}
			case corev1.NodeDiskPressure:
				if c.Status == corev1.ConditionTrue {
					pressure += "disk,"
				}
			case corev1.NodePIDPressure:
				if c.Status == corev1.ConditionTrue {
					pressure += "pid,"
				}
			}
		}
		if len(pressure) > 0 {
			it.Pressure = pressure[:len(pressure)-1]
		}

		if sum := s.fetchStats(ctx, n.Name); sum != nil {
			it.CPUUsageM = sum.Node.CPU.UsageNanoCores / 1_000_000
			it.MemUsage = sum.Node.Memory.WorkingSetBytes
			it.FsUsed = sum.Node.Fs.UsedBytes
			it.FsCapacity = sum.Node.Fs.CapacityBytes
			it.ImageFsUsed = sum.Node.Runtime.ImageFs.UsedBytes
			for _, p := range sum.Pods {
				for _, v := range p.Volume {
					if v.PVCRef != nil {
						pvcUsage[v.PVCRef.Namespace+"/"+v.PVCRef.Name] = [2]int64{v.UsedBytes, v.CapacityBytes}
					}
				}
			}
		}
		out = append(out, it)
	}
	return out, pvcUsage
}

// buildPVCs kombiniert PVC-Objekte mit Volume-Stats und den mountenden
// Workloads (aus den Pod-Specs).
func (s *Syncer) buildPVCs(pvcs []*corev1.PersistentVolumeClaim, pods []*corev1.Pod, pvcUsage map[string][2]int64) []pvcItem {
	// claim (ns/name) → Workloads, die ihn mounten
	mounts := map[string]map[string]bool{}
	for _, p := range pods {
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim == nil {
				continue
			}
			key := p.Namespace + "/" + v.PersistentVolumeClaim.ClaimName
			_, wlName := workloadOf(p)
			if mounts[key] == nil {
				mounts[key] = map[string]bool{}
			}
			mounts[key][wlName] = true
		}
	}

	out := make([]pvcItem, 0, len(pvcs))
	for _, c := range pvcs {
		key := c.Namespace + "/" + c.Name
		it := pvcItem{
			Namespace:  c.Namespace,
			Name:       c.Name,
			Phase:      string(c.Status.Phase),
			VolumeName: c.Spec.VolumeName,
			UsedBytes:  -1,
		}
		if c.Spec.StorageClassName != nil {
			it.StorageClass = *c.Spec.StorageClassName
		}
		for _, m := range c.Spec.AccessModes {
			it.AccessModes = append(it.AccessModes, string(m))
		}
		if req, ok := c.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			it.RequestedByte = req.Value()
		}
		if cap, ok := c.Status.Capacity[corev1.ResourceStorage]; ok {
			it.CapacityByte = cap.Value()
		}
		if u, ok := pvcUsage[key]; ok {
			it.UsedBytes = u[0]
			if it.CapacityByte == 0 {
				it.CapacityByte = u[1]
			}
		}
		for wl := range mounts[key] {
			it.MountedBy = append(it.MountedBy, wl)
		}
		out = append(out, it)
	}
	return out
}

// listInfra sammelt Nodes + PVCs für den Topologie-Push.
func (s *Syncer) listInfra(ctx context.Context, pods []*corev1.Pod) ([]nodeItem, []pvcItem) {
	if s.nodeLister == nil || s.pvcLister == nil {
		return nil, nil
	}
	nodes, err1 := s.nodeLister.List(labels.Everything())
	pvcs, err2 := s.pvcLister.List(labels.Everything())
	if err1 != nil || err2 != nil {
		return nil, nil
	}
	ni, pvcUsage := s.buildNodes(ctx, nodes)
	return ni, s.buildPVCs(pvcs, pods, pvcUsage)
}
