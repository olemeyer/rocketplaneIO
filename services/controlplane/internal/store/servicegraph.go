package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// servicegraph.go löst rohe Trace-Kanten (Client-Workload → server.address) gegen
// die persistierte Cluster-Topologie auf gültige Map-Kanten (Workload→Workload)
// auf. server.address ist bei Beyla meist ein k8s-Service-Name (evtl. FQDN oder
// mit Suffix wie -rw), seltener eine IP. Nicht auflösbare Ziele werden VERWORFEN
// (keine Phantom-Knoten, die nicht auf der Map existieren).

var ipRe = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

// serviceSuffixes: gängige Service-Namens-Suffixe, die auf denselben Workload
// zeigen (CNPG postgres-rw/-ro, Headless-Services, …). Exakter Treffer geht vor;
// erst wenn der fehlschlägt, wird EIN Suffix abgeschnitten.
var serviceSuffixes = []string{"-rw", "-ro", "-r", "-headless", "-primary", "-replica", "-master", "-svc", "-service"}

// ResolveTraceEdges wandelt aggregierte RawTraceEdges in Map-Kanten mit RED um.
// windowSecs ist das Aggregationsfenster (für Requests/Sekunde).
//
// Zwei-Pass: CLIENT-Span-Kanten zuerst (sie tragen die Aufrufer-Sicht), danach
// füllen SERVER-Span-Kanten nur (from,to)-Paare, die die Client-Pässe noch NICHT
// erzeugt haben. So zählt derselbe Request (der je einen Client- UND einen Server-
// Span erzeugt) nicht doppelt, während Server-Spans zusätzliche Kanten liefern, die
// client-seitig gar nicht erst erfasst wurden.
func (s *Store) ResolveTraceEdges(ctx context.Context, clusterID uuid.UUID, raw []model.RawTraceEdge, windowSecs float64) ([]model.MapEdge, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if windowSecs <= 0 {
		windowSecs = 1
	}
	res, err := s.loadTopologyResolver(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	type acc struct {
		reqs, errs int64
		p95        float64
		protocol   string
		fromClient bool // Kante stammt (auch) aus einem Client-Span
	}
	agg := map[string]*acc{}
	order := []string{}

	add := func(e model.RawTraceEdge) {
		known := res.workload(e.KnownNs, e.KnownName)
		if known == "" {
			return // berichtender Workload nicht in der Topologie
		}
		peer := res.resolveServer(e.KnownNs, e.Peer)
		if peer == "" || peer == known {
			return // Gegenseite nicht auflösbar (z.B. Node-Name, apiserver) oder Selbst-Kante
		}
		from, to := known, peer
		if !e.KnownIsClient {
			from, to = peer, known // Server-Span: die Gegenseite ist der Aufrufer
		}
		key := from + "\x00" + to
		a := agg[key]
		if a == nil {
			// Server-Span-Kante nur aufnehmen, wenn kein Client-Span sie schon lieferte
			// wird über die zwei Pässe garantiert (siehe unten) — hier reicht Neuanlage.
			a = &acc{protocol: e.Protocol}
			agg[key] = a
			order = append(order, key)
		} else if !e.KnownIsClient && a.fromClient {
			return // Client-Sicht existiert bereits → Server-Span nicht dazurechnen
		}
		a.reqs += e.Reqs
		a.errs += e.Errs
		if e.P95Ms > a.p95 {
			a.p95 = e.P95Ms
		}
		if a.protocol == "" {
			a.protocol = e.Protocol
		}
		if e.KnownIsClient {
			a.fromClient = true
		}
	}

	// Pass 1: Client-Span-Kanten. Pass 2: Server-Span-Kanten (füllen Lücken).
	for _, e := range raw {
		if e.KnownIsClient {
			add(e)
		}
	}
	for _, e := range raw {
		if !e.KnownIsClient {
			add(e)
		}
	}

	out := make([]model.MapEdge, 0, len(order))
	for _, key := range order {
		a := agg[key]
		fromTo := strings.SplitN(key, "\x00", 2)
		var errRate float64
		if a.reqs > 0 {
			errRate = float64(a.errs) / float64(a.reqs)
		}
		out = append(out, model.MapEdge{
			From:     fromTo[0],
			To:       fromTo[1],
			Source:   "trace",
			Protocol: a.protocol,
			ReqRate:  float64(a.reqs) / windowSecs,
			ErrRate:  errRate,
			P95Ms:    a.p95,
		})
	}
	return out, nil
}

// topologyResolver hält die für die Auflösung nötigen Topologie-Indizes eines
// Clusters (einmal geladen, dann rein in-memory aufgelöst).
type topologyResolver struct {
	// "ns\x00name" → "ns/kind/name"
	wlByNsName map[string]string
	// name → []node-id (für namespace-lose Namensauflösung als letzte Instanz)
	wlByName map[string][]string
	// service "ns\x00name" → service-name (Existenz-Check + Alias auf Workload)
	svcByNsName map[string]bool
	// service cluster_ip → "ns\x00name"
	svcByIP map[string]string
	// pod ip → "ns/kind/name"
	podByIP map[string]string
	// bekannte Namespaces (für FQDN-Zerlegung name.namespace.svc…)
	namespaces map[string]bool
}

func (s *Store) loadTopologyResolver(ctx context.Context, clusterID uuid.UUID) (*topologyResolver, error) {
	r := &topologyResolver{
		wlByNsName:  map[string]string{},
		wlByName:    map[string][]string{},
		svcByNsName: map[string]bool{},
		svcByIP:     map[string]string{},
		podByIP:     map[string]string{},
		namespaces:  map[string]bool{},
	}

	wr, err := s.pool.Query(ctx, `SELECT namespace, kind, name FROM workloads WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load workloads: %w", err)
	}
	for wr.Next() {
		var ns, kind, name string
		if err := wr.Scan(&ns, &kind, &name); err != nil {
			wr.Close()
			return nil, err
		}
		id := ns + "/" + kind + "/" + name
		r.wlByNsName[ns+"\x00"+name] = id
		r.wlByName[name] = append(r.wlByName[name], id)
		r.namespaces[ns] = true
	}
	wr.Close()
	if err := wr.Err(); err != nil {
		return nil, err
	}

	sr, err := s.pool.Query(ctx, `SELECT namespace, name, cluster_ip FROM k8s_services WHERE cluster_id=$1`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load services: %w", err)
	}
	for sr.Next() {
		var ns, name, ip string
		if err := sr.Scan(&ns, &name, &ip); err != nil {
			sr.Close()
			return nil, err
		}
		r.svcByNsName[ns+"\x00"+name] = true
		if ip != "" && ip != "None" {
			r.svcByIP[ip] = ns + "\x00" + name
		}
		r.namespaces[ns] = true
	}
	sr.Close()
	if err := sr.Err(); err != nil {
		return nil, err
	}

	pr, err := s.pool.Query(ctx, `SELECT namespace, name, ip, workload_kind, workload_name FROM pods WHERE cluster_id=$1 AND ip <> ''`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("load pods: %w", err)
	}
	for pr.Next() {
		var ns, name, ip, wk, wn string
		if err := pr.Scan(&ns, &name, &ip, &wk, &wn); err != nil {
			pr.Close()
			return nil, err
		}
		if wk == "" || wn == "" {
			wk, wn = "Pod", name
		}
		r.podByIP[ip] = ns + "/" + wk + "/" + wn
	}
	pr.Close()
	if err := pr.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

// workload löst (namespace, name) auf einen Workload-Knoten auf — exakt, dann mit
// abgeschnittenem Service-Suffix.
func (r *topologyResolver) workload(ns, name string) string {
	if id, ok := r.wlByNsName[ns+"\x00"+name]; ok {
		return id
	}
	if base := stripServiceSuffix(name); base != name {
		if id, ok := r.wlByNsName[ns+"\x00"+base]; ok {
			return id
		}
	}
	return ""
}

// resolveServer löst eine server.address (Name/FQDN/IP) auf einen Workload-Knoten
// im Kontext des Aufrufer-Namespace auf. Reihenfolge: IP → Pod/Service; Name →
// (FQDN-Namespace|Client-Namespace) Workload/Service; letzte Instanz: eindeutiger
// clusterweiter Namenstreffer.
func (r *topologyResolver) resolveServer(clientNs, addr string) string {
	if addr == "" {
		return ""
	}
	// IP-Ziel
	if ipRe.MatchString(addr) {
		if id, ok := r.podByIP[addr]; ok {
			return id
		}
		if nsName, ok := r.svcByIP[addr]; ok {
			seg := strings.SplitN(nsName, "\x00", 2)
			if id := r.workload(seg[0], seg[1]); id != "" {
				return id
			}
		}
		return ""
	}

	// FQDN zerlegen: name.namespace.svc.cluster.local → (name, namespace)
	name, ns := addr, clientNs
	if strings.Contains(addr, ".") {
		segs := strings.Split(addr, ".")
		name = segs[0]
		if len(segs) >= 2 && r.namespaces[segs[1]] {
			ns = segs[1]
		}
	}

	// Direkter Workload-Treffer im (FQDN- oder Client-)Namespace.
	if id := r.workload(ns, name); id != "" {
		return id
	}
	// Ziel ist ein bekannter Service in diesem Namespace → auf seinen Workload
	// (namensgleich, evtl. suffix-bereinigt) auflösen.
	if r.svcByNsName[ns+"\x00"+name] {
		if id := r.workload(ns, name); id != "" {
			return id
		}
	}
	// Letzte Instanz: clusterweit EINDEUTIGER Namenstreffer (sonst mehrdeutig → drop).
	if ids := r.wlByName[name]; len(ids) == 1 {
		return ids[0]
	}
	if base := stripServiceSuffix(name); base != name {
		if ids := r.wlByName[base]; len(ids) == 1 {
			return ids[0]
		}
	}
	return ""
}

func stripServiceSuffix(name string) string {
	for _, suf := range serviceSuffixes {
		if strings.HasSuffix(name, suf) && len(name) > len(suf) {
			return strings.TrimSuffix(name, suf)
		}
	}
	return name
}
