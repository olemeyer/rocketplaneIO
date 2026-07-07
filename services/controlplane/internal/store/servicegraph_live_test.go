package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// servicegraph_live_test.go verifiziert den Trace-Kanten-Resolver gegen eine ECHTE
// Cluster-Topologie in Postgres. Läuft NUR wenn RP_LIVE_PG + RP_LIVE_CLUSTER gesetzt
// sind (sonst Skip) — kein CI-Lauf. Aufruf z.B.:
//
//	RP_LIVE_PG='postgres://rocketplane:PASS@localhost:15432/rocketplane?sslmode=disable' \
//	RP_LIVE_CLUSTER=2177befb-e978-4b18-9875-c233275f7e0d \
//	go test ./services/controlplane/internal/store -run TestResolveTraceEdgesLive -v
func TestResolveTraceEdgesLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	clStr := os.Getenv("RP_LIVE_CLUSTER")
	if dsn == "" || clStr == "" {
		t.Skip("set RP_LIVE_PG + RP_LIVE_CLUSTER to run the live resolver test")
	}
	clusterID, err := uuid.Parse(clStr)
	if err != nil {
		t.Fatalf("bad RP_LIVE_CLUSTER: %v", err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	// Rohkanten wie Beyla sie real liefert (Peer = Service-Name/IP, ohne Port).
	raw := []model.RawTraceEdge{
		// CLIENT-Spans: KnownName=Aufrufer, Peer=server.address.
		{KnownNs: "rocketplane", KnownName: "controlplane", Peer: "clickhouse", KnownIsClient: true, Protocol: "http", Reqs: 30, Errs: 0, P95Ms: 12},
		{KnownNs: "rocketplane", KnownName: "controlplane", Peer: "postgres", KnownIsClient: true, Protocol: "postgresql", Reqs: 60, Errs: 3, P95Ms: 4},
		{KnownNs: "modelstudio", KnownName: "modelstudio", Peer: "postgres-rw", KnownIsClient: true, Protocol: "postgresql", Reqs: 10, Errs: 0, P95Ms: 5}, // Suffix -rw → Cluster/postgres
		{KnownNs: "kube-system", KnownName: "coredns", Peer: "100.64.0.1", KnownIsClient: true, Protocol: "http", Reqs: 8, Errs: 0, P95Ms: 26},           // apiserver-IP → nicht auflösbar → drop
		{KnownNs: "modelstudio", KnownName: "postgres", Peer: "postgres-rw", KnownIsClient: true, Protocol: "postgresql", Reqs: 4, Errs: 0, P95Ms: 3},    // Self-Edge → drop
		// SERVER-Span mit Node-Name-Aufrufer (kube-proxy-SNAT) → Peer nicht auflösbar → drop.
		{KnownNs: "rocketplane", KnownName: "controlplane", Peer: "default-c4ike2bvcc", KnownIsClient: false, Protocol: "http", Reqs: 5, Errs: 0, P95Ms: 9},
		// SERVER-Span, dessen Client-Sicht schon existiert (controlplane→clickhouse):
		// darf NICHT doppelt zählen (Pass-2 überspringt bekannte Paare).
		{KnownNs: "rocketplane", KnownName: "clickhouse", Peer: "controlplane", KnownIsClient: false, Protocol: "http", Reqs: 999, Errs: 0, P95Ms: 999},
		// GEFLIPPTER Server-Span: würde die GESPIEGELTE Kante clickhouse→controlplane
		// erzeugen — der Reverse-Pair-Guard muss sie verwerfen (Client-Kante
		// controlplane→clickhouse existiert).
		{KnownNs: "rocketplane", KnownName: "controlplane", Peer: "clickhouse", KnownIsClient: false, Protocol: "http", Reqs: 50, Errs: 0, P95Ms: 1},
	}

	edges, err := st.ResolveTraceEdges(ctx, clusterID, raw, 60)
	if err != nil {
		t.Fatalf("ResolveTraceEdges: %v", err)
	}

	got := map[string]model.MapEdge{}
	for _, e := range edges {
		got[e.From+" -> "+e.To] = e
		t.Logf("edge %s -> %s [%s] req/s=%.2f err=%.3f p95=%.1f", e.From, e.To, e.Protocol, e.ReqRate, e.ErrRate, e.P95Ms)
	}

	want := []string{
		"rocketplane/Deployment/controlplane -> rocketplane/StatefulSet/clickhouse",
		"rocketplane/Deployment/controlplane -> rocketplane/StatefulSet/postgres",
		"modelstudio/Cluster/postgres -> modelstudio/Cluster/postgres", // darf NICHT existieren (self)
	}
	// Positive: die beiden echten App→Infra-Kanten müssen da sein.
	if e, ok := got[want[0]]; !ok {
		t.Errorf("missing edge: %s", want[0])
	} else if e.ReqRate != 0.5 { // 30 reqs / 60s — der 999-reqs-Server-Span darf NICHT doppelt zählen
		t.Errorf("controlplane->clickhouse reqRate = %v, want 0.5 (server-span double-count leaked?)", e.ReqRate)
	}
	if e, ok := got[want[1]]; !ok {
		t.Errorf("missing edge: %s", want[1])
	} else {
		if e.Source != "trace" {
			t.Errorf("edge source = %q, want trace", e.Source)
		}
		if e.ReqRate != 1.0 { // 60 reqs / 60s
			t.Errorf("reqRate = %v, want 1.0", e.ReqRate)
		}
		if e.ErrRate < 0.049 || e.ErrRate > 0.051 { // 3/60
			t.Errorf("errRate = %v, want ~0.05", e.ErrRate)
		}
	}
	// modelstudio postgres-rw → Cluster/postgres (Suffix-Strip), NICHT als Self-Edge.
	if _, ok := got["modelstudio/Cluster/postgres -> modelstudio/Cluster/postgres"]; ok {
		t.Errorf("self-edge leaked (postgres → postgres-rw should be dropped)")
	}
	// apiserver-IP darf keine Kante erzeugen.
	for k := range got {
		if k == "kube-system/Deployment/coredns -> " || len(k) > 0 && contains(k, "100.64.0.1") {
			t.Errorf("apiserver IP produced an edge: %s", k)
		}
	}
	// Reverse-Pair-Guard: die gespiegelte Server-Span-Kante darf nicht existieren.
	if _, ok := got["rocketplane/StatefulSet/clickhouse -> rocketplane/Deployment/controlplane"]; ok {
		t.Errorf("mirrored server-span edge leaked (reverse-pair guard failed)")
	}
	t.Logf("resolved %d edges from %d raw", len(edges), len(raw))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
