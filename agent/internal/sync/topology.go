package sync

import (
	"context"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// topology.go erfasst die Cluster-Topologie für die Service-Map: Pods (mit
// Owner-Ableitung zum Workload) + Services. Anders als der Namespace-Sync pusht es
// PERIODISCH (Pods ändern sich häufig — ein Debounce je Event wäre zu geschwätzig).

const (
	topologyInterval = 20 * time.Second
	// Nach einem Trigger (Safe-Action ausgeführt) pusht der Agent im schnellen
	// Takt weiter, bis der Rollout die UI erreicht hat — „alles aktualisiert
	// sich sofort" statt bis zu 20s Latenz.
	topologyBurstEvery = 4 * time.Second
	topologyBurstFor   = 45 * time.Second
)

// podItem / svcItem spiegeln model.Pod / model.K8sService (camelCase).
type podItem struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	IP           string `json:"ip"`
	Image        string `json:"image"`
	NodeName     string `json:"nodeName"`
	Phase        string `json:"phase"`
	Ready        bool   `json:"ready"`
	Restarts     int    `json:"restarts"`
	WorkloadKind string `json:"workloadKind"`
	WorkloadName string `json:"workloadName"`
}

type svcItem struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ClusterIP string            `json:"clusterIp"`
	Selector  map[string]string `json:"selector"`
}

// wlItem spiegelt model.WorkloadSync — direkt vom Workload-Objekt gelesen,
// damit auch scaled-to-zero-Workloads (0 Pods) als Knoten existieren.
type wlItem struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	ReplicasDesired int    `json:"replicasDesired"`
	ReplicasReady   int    `json:"replicasReady"`
}

// RunTopology startet Pod- + Service-Informer und pusht die Topologie periodisch,
// bis ctx abbricht. Von cmd/agent parallel zu Syncer.Run gestartet.
func (s *Syncer) RunTopology(ctx context.Context) error {
	factory := informers.NewSharedInformerFactory(s.clientset, informerResync)
	podInf := factory.Core().V1().Pods()
	svcInf := factory.Core().V1().Services()
	depInf := factory.Apps().V1().Deployments()
	stsInf := factory.Apps().V1().StatefulSets()
	dsInf := factory.Apps().V1().DaemonSets()
	nodeInf := factory.Core().V1().Nodes()
	pvcInf := factory.Core().V1().PersistentVolumeClaims()
	podLister := podInf.Lister()
	svcLister := svcInf.Lister()
	s.depLister = depInf.Lister()
	s.stsLister = stsInf.Lister()
	s.dsLister = dsInf.Lister()
	s.nodeLister = nodeInf.Lister()
	s.pvcLister = pvcInf.Lister()

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), podInf.Informer().HasSynced, svcInf.Informer().HasSynced,
		depInf.Informer().HasSynced, stsInf.Informer().HasSynced, dsInf.Informer().HasSynced,
		nodeInf.Informer().HasSynced, pvcInf.Informer().HasSynced) {
		log.Printf("topology: informer cache sync failed")
		return nil
	}
	log.Printf("topology: pod+service+workload informers synced")

	// Pod-Lister für den Log-Collector freigeben (Pod→Workload-Mapping).
	s.podLister = podLister
	close(s.podReady)

	ticker := time.NewTicker(topologyInterval)
	defer ticker.Stop()
	burst := time.NewTicker(topologyBurstEvery)
	defer burst.Stop()
	var burstUntil time.Time

	s.pushTopology(ctx, podLister, svcLister) // initial
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.topoTrigger:
			burstUntil = time.Now().Add(topologyBurstFor)
			s.pushTopology(ctx, podLister, svcLister)
		case <-burst.C:
			if time.Now().Before(burstUntil) {
				s.pushTopology(ctx, podLister, svcLister)
			}
		case <-ticker.C:
			s.pushTopology(ctx, podLister, svcLister)
		}
	}
}

// pushTopology listet Pods + Services aus dem Cache und POSTet sie als Full-Sync.
func (s *Syncer) pushTopology(ctx context.Context, pods listersv1.PodLister, svcs listersv1.ServiceLister) {
	podList, err := pods.List(labels.Everything())
	if err != nil {
		log.Printf("topology: list pods failed: %v", err)
		return
	}
	svcList, err := svcs.List(labels.Everything())
	if err != nil {
		log.Printf("topology: list services failed: %v", err)
		return
	}

	pi := make([]podItem, 0, len(podList))
	for _, p := range podList {
		kind, name := workloadOf(p)
		image := ""
		if len(p.Spec.Containers) > 0 {
			image = p.Spec.Containers[0].Image
		}
		pi = append(pi, podItem{
			Namespace:    p.Namespace,
			Name:         p.Name,
			IP:           p.Status.PodIP,
			Image:        image,
			NodeName:     p.Spec.NodeName,
			Phase:        podPhase(p),
			Ready:        podReady(p) && p.DeletionTimestamp == nil,
			Restarts:     podRestarts(p),
			WorkloadKind: kind,
			WorkloadName: name,
		})
	}

	si := make([]svcItem, 0, len(svcList))
	for _, sv := range svcList {
		sel := sv.Spec.Selector
		if sel == nil {
			sel = map[string]string{}
		}
		si = append(si, svcItem{
			Namespace: sv.Namespace,
			Name:      sv.Name,
			Type:      string(sv.Spec.Type),
			ClusterIP: sv.Spec.ClusterIP,
			Selector:  sel,
		})
	}

	// Flows aus conntrack (nur in-cluster mit hostNetwork verfügbar): Pod-IPs auf
	// Workloads mappen und die beobachteten TCP-Verbindungen zu Kanten aggregieren.
	ipToWorkload := make(map[string]workloadRef, len(podList))
	for _, p := range podList {
		if p.Status.PodIP == "" {
			continue
		}
		kind, name := workloadOf(p)
		ipToWorkload[p.Status.PodIP] = workloadRef{namespace: p.Namespace, kind: kind, name: name}
	}
	edges := readFlowEdges(ipToWorkload)

	// Workload-Objekte direkt lesen: desired aus spec, ready aus status —
	// die Wahrheit auch ohne Pods (scaled to zero).
	wl := []wlItem{}
	if s.depLister != nil {
		if deps, err := s.depLister.List(labels.Everything()); err == nil {
			for _, d := range deps {
				desired := 1
				if d.Spec.Replicas != nil {
					desired = int(*d.Spec.Replicas)
				}
				wl = append(wl, wlItem{Namespace: d.Namespace, Name: d.Name, Kind: "Deployment",
					ReplicasDesired: desired, ReplicasReady: int(d.Status.ReadyReplicas)})
			}
		}
	}
	if s.stsLister != nil {
		if stss, err := s.stsLister.List(labels.Everything()); err == nil {
			for _, st := range stss {
				desired := 1
				if st.Spec.Replicas != nil {
					desired = int(*st.Spec.Replicas)
				}
				wl = append(wl, wlItem{Namespace: st.Namespace, Name: st.Name, Kind: "StatefulSet",
					ReplicasDesired: desired, ReplicasReady: int(st.Status.ReadyReplicas)})
			}
		}
	}
	if s.dsLister != nil {
		if dss, err := s.dsLister.List(labels.Everything()); err == nil {
			for _, d := range dss {
				wl = append(wl, wlItem{Namespace: d.Namespace, Name: d.Name, Kind: "DaemonSet",
					ReplicasDesired: int(d.Status.DesiredNumberScheduled), ReplicasReady: int(d.Status.NumberReady)})
			}
		}
	}

	// Infrastruktur (Nodes + PVCs, inkl. kubelet-Stats) — Teil desselben
	// Pushes: das eine topology-Signal hält auch den Infrastructure-Bereich live.
	nodesI, pvcsI := s.listInfra(ctx, podList)

	body := map[string]any{"pods": pi, "services": si, "edges": edges, "workloads": wl, "nodes": nodesI, "pvcs": pvcsI}
	for attempt := 1; attempt <= pushMaxAttempts; attempt++ {
		if err := s.postJSON(ctx, "/api/agent/topology", body); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("topology: push attempt %d/%d failed: %v", attempt, pushMaxAttempts, err)
			if attempt < pushMaxAttempts {
				select {
				case <-ctx.Done():
					return
				case <-time.After(pushRetryBackoff):
				}
			}
			continue
		}
		log.Printf("topology: pushed %d pods, %d services, %d flows", len(pi), len(si), len(edges))
		return
	}
}

// workloadOf leitet den steuernden Workload eines Pods aus den OwnerReferences ab.
// ReplicaSet ⇒ Deployment (Pod-Template-Hash-Suffix entfernt); StatefulSet/DaemonSet/
// Job direkt; ownerlos ⇒ ("Pod", pod.Name).
func workloadOf(p *corev1.Pod) (kind, name string) {
	for _, o := range p.OwnerReferences {
		if o.Controller == nil || !*o.Controller {
			continue
		}
		switch o.Kind {
		case "ReplicaSet":
			return "Deployment", stripHashSuffix(o.Name)
		case "StatefulSet", "DaemonSet", "Job", "ReplicationController":
			return o.Kind, o.Name
		case "Node":
			// Statische/Mirror-Pods (kube-apiserver, etcd, …) sind vom Node
			// „besessen" — als eigenständige Pod-Knoten führen, nicht unter dem Node.
			return "Pod", p.Name
		default:
			return o.Kind, o.Name
		}
	}
	return "Pod", p.Name
}

// stripHashSuffix entfernt das letzte "-<hash>"-Segment (ReplicaSet → Deployment).
func stripHashSuffix(name string) string {
	if i := strings.LastIndexByte(name, '-'); i > 0 {
		return name[:i]
	}
	return name
}

// podPhase: die UI-Wahrheit — ein Pod mit DeletionTimestamp FÄHRT RAUS,
// egal was Status.Phase noch sagt (K8s lässt ihn bis zum Ende „Running").
func podPhase(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	return string(p.Status.Phase)
}

// podReady meldet, ob die PodReady-Condition True ist.
func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podRestarts summiert die Container-Restarts eines Pods.
func podRestarts(p *corev1.Pod) int {
	var n int
	for _, cs := range p.Status.ContainerStatuses {
		n += int(cs.RestartCount)
	}
	return n
}
