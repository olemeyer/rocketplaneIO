package sync

import (
	"log"
	"strconv"
	"sync/atomic"

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

// flowsDisabled latches once conntrack turns out to be unreadable (no
// NET_ADMIN / no hostNetwork): the service map simply has no flow edges, which
// is the documented behaviour of the non-privileged install.
var flowsDisabled atomic.Bool

func disableFlows(what string, err error) {
	if flowsDisabled.CompareAndSwap(false, true) {
		log.Printf("flows: %s: %v — flow discovery disabled "+
			"(needs NET_ADMIN + hostNetwork; the service map will have no edges)", what, err)
	}
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
	// Flow discovery needs NET_ADMIN + hostNetwork; the hardened Deployment has
	// neither. Log the loss of capability ONCE instead of every sync tick — the
	// permission cannot appear at runtime, so repeating it is pure noise in the
	// logs the operator is trying to read.
	if flowsDisabled.Load() {
		return nil
	}
	c, err := conntrack.Dial(nil)
	if err != nil {
		disableFlows("conntrack dial failed", err)
		return nil
	}
	defer c.Close()

	flows, err := c.Dump(nil)
	if err != nil {
		disableFlows("conntrack dump failed", err)
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
