package sync

import (
	"log"
	"strconv"

	"github.com/ti-mo/conntrack"
)

// flows.go erkennt Pod-zu-Pod-Informationsflüsse durch Auslesen der Kernel-
// Conntrack-Tabelle über NETLINK (nf_conntrack_netlink). Das ist robuster als das
// procfs-Interface (in vielen Kernels — z.B. minikube — nicht einkompiliert) und der
// Standard für zero-instrumentation Flow-Discovery (Cilium/Hubble-Ansatz). Funktioniert
// nur in-cluster mit hostNetwork + CAP_NET_ADMIN; sonst werden keine Flows erfasst.

// workloadRef identifiziert einen Workload (Map-Knoten).
type workloadRef struct{ namespace, kind, name string }

// flowEdge ist eine aggregierte Kante from → to:port mit Verbindungszahl.
type flowEdge struct {
	from      workloadRef
	to        workloadRef
	toPort    int
	connCount int64
}

// edgeItem ist die JSON-Form für den Agent→Control-Plane-Push (camelCase).
type edgeItem struct {
	FromNamespace string `json:"fromNamespace"`
	FromKind      string `json:"fromKind"`
	FromName      string `json:"fromName"`
	ToNamespace   string `json:"toNamespace"`
	ToKind        string `json:"toKind"`
	ToName        string `json:"toName"`
	ToPort        int    `json:"toPort"`
	ConnCount     int64  `json:"connCount"`
}

// conntrackAvailable meldet, ob die Conntrack-Tabelle per Netlink lesbar ist.
func conntrackAvailable() bool {
	c, err := conntrack.Dial(nil)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// readFlowEdges dumpt die conntrack-Tabelle per Netlink, mappt die ORIGINAL-Richtung
// (Initiator → Ziel) über ipToWorkload auf Workloads und aggregiert die TCP-
// Verbindungen zu gerichteten Kanten. Selbst-Verbindungen und unbekannte Endpunkte
// werden verworfen.
func readFlowEdges(ipToWorkload map[string]workloadRef) []edgeItem {
	c, err := conntrack.Dial(nil)
	if err != nil {
		log.Printf("flows: conntrack dial failed: %v", err)
		return nil
	}
	defer c.Close()

	flows, err := c.Dump(nil)
	if err != nil {
		log.Printf("flows: conntrack dump failed: %v", err)
		return nil
	}

	agg := map[string]*flowEdge{}
	for _, f := range flows {
		if f.TupleOrig.Proto.Protocol != 6 { // nur TCP
			continue
		}
		// Quelle = Original-Absender (Client). Ziel = Reply-Absender: das ist die
		// ECHTE Server-Pod-IP nach kube-proxy-DNAT (bei ClusterIP-Services zeigt
		// die Original-Ziel-IP nur auf die Service-VIP, nicht auf den Pod). Bei
		// direktem Pod-zu-Pod ist reply.src == orig.dst — funktioniert also universell.
		src := f.TupleOrig.IP.SourceAddress.String()
		dst := f.TupleReply.IP.SourceAddress.String()
		dport := int(f.TupleReply.Proto.SourcePort)

		from, okF := ipToWorkload[src]
		to, okT := ipToWorkload[dst]
		if !okF || !okT || from == to {
			continue
		}
		key := from.namespace + "/" + from.kind + "/" + from.name + ">" +
			to.namespace + "/" + to.kind + "/" + to.name + ":" + strconv.Itoa(dport)
		if e := agg[key]; e != nil {
			e.connCount++
		} else {
			agg[key] = &flowEdge{from: from, to: to, toPort: dport, connCount: 1}
		}
	}

	out := make([]edgeItem, 0, len(agg))
	for _, e := range agg {
		out = append(out, edgeItem{
			FromNamespace: e.from.namespace, FromKind: e.from.kind, FromName: e.from.name,
			ToNamespace: e.to.namespace, ToKind: e.to.kind, ToName: e.to.name,
			ToPort: e.toPort, ConnCount: e.connCount,
		})
	}
	return out
}
