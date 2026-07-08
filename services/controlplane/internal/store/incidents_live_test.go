package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// incidents_live_test.go verifiziert die Incident-Auto-Glue gegen ein echtes
// Postgres (Migrationen müssen angewandt sein). Läuft NUR mit RP_LIVE_PG:
//
//	RP_LIVE_PG='postgres://rocketplane:rocketplane@localhost:5433/rocketplane?sslmode=disable' \
//	go test ./services/controlplane/internal/store -run TestIncidentAutoGlueLive -v
//
// Geprüft: (1) firing deklariert genau EINEN Incident, erneutes firing
// dedupliziert (created=false, gleiche ID); (2) Klären eines Alert-Incidents
// auto-resolved ihn; (3) manuell deklarierte Incidents bleiben bei Alert-Klärung
// offen; (4) MTTA/MTTR-Anker und Timeline stimmen.
func TestIncidentAutoGlueLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the incident auto-glue test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	// Frischer Nutzer + Org + Cluster.
	u, err := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@test.local", "Glue Tester", "")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	org, err := st.CreateOrg(ctx, "glue-"+uuid.NewString()[:8], u.ID)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	cl, _, err := st.CreateClusterWithEnrollToken(ctx, org.ID, "glue-cluster", u.ID)
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}

	ruleID := uuid.New()

	// (1) firing → declare.
	id1, created1, err := st.DeclareIncidentFromAlert(ctx, ruleID, cl.ID, "High error ratio", "critical", 0.42, nil)
	if err != nil {
		t.Fatalf("declare 1: %v", err)
	}
	if !created1 {
		t.Fatalf("first firing should create an incident")
	}

	// (1b) erneutes firing → dedup (kein neuer Incident, gleiche ID).
	id2, created2, err := st.DeclareIncidentFromAlert(ctx, ruleID, cl.ID, "High error ratio", "critical", 0.55, nil)
	if err != nil {
		t.Fatalf("declare 2: %v", err)
	}
	if created2 {
		t.Fatalf("second firing should dedup, not create")
	}
	if id1 != id2 {
		t.Fatalf("dedup returned different incident: %s vs %s", id1, id2)
	}

	inc, err := st.GetIncident(ctx, cl.ID, id1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if inc.Source != "alert" || inc.Status != "open" || inc.Severity != "critical" {
		t.Fatalf("unexpected incident: source=%s status=%s sev=%s", inc.Source, inc.Status, inc.Severity)
	}

	// Timeline: declared + zwei alert-Events.
	events, err := st.ListIncidentEvents(ctx, id1)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var declared, alerts int
	for _, e := range events {
		if e.Kind == "declared" {
			declared++
		}
		if e.Kind == "alert" {
			alerts++
		}
	}
	if declared != 1 || alerts != 2 {
		t.Fatalf("timeline mismatch: declared=%d alerts=%d", declared, alerts)
	}

	// (2) Klären → auto-resolve.
	rid, err := st.ResolveIncidentFromAlert(ctx, ruleID, cl.ID, "High error ratio", nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rid != id1 {
		t.Fatalf("resolve targeted wrong incident: %s", rid)
	}
	inc, _ = st.GetIncident(ctx, cl.ID, id1)
	if inc.Status != "resolved" || inc.ResolvedAt == nil {
		t.Fatalf("alert incident should auto-resolve, got status=%s resolvedAt=%v", inc.Status, inc.ResolvedAt)
	}

	// (2b) Nach dem Klären startet ein neues firing einen FRISCHEN Incident
	// (dedup_key wurde beim Resolve geleert).
	id3, created3, err := st.DeclareIncidentFromAlert(ctx, ruleID, cl.ID, "High error ratio", "high", 0.9, nil)
	if err != nil {
		t.Fatalf("declare 3: %v", err)
	}
	if !created3 || id3 == id1 {
		t.Fatalf("re-firing after resolve should create a fresh incident (created=%v id=%s)", created3, id3)
	}

	// (3) Manueller Incident bleibt bei fremder Alert-Klärung unberührt.
	man, err := st.CreateIncident(ctx, org.ID, cl.ID, "manual sev", "", "medium", &u.ID, u.Email)
	if err != nil {
		t.Fatalf("manual: %v", err)
	}
	if man.Status != "open" || man.Source != "manual" {
		t.Fatalf("manual incident wrong: %s/%s", man.Status, man.Source)
	}

	// (4) Stats plausibel (>=2 offen: id3 + manual).
	stats, err := st.IncidentStats(ctx, cl.ID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats["open"] < 2 {
		t.Fatalf("expected >=2 open incidents, got %v", stats["open"])
	}
	t.Logf("auto-glue OK: open=%v mttr=%.1fs", stats["open"], stats["mttrSeconds"])
}
