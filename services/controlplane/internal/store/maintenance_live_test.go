package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maintenance_live_test.go verifies the maintenance-window suppression logic
// (InMaintenance) against real Postgres. RP_LIVE_PG-gated, like the other live
// tests. Covers cluster-wide vs namespace-scoped windows, scheduled (not yet
// active) windows, and ending an active window.
func TestMaintenanceLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the maintenance test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@maint.local", "Maint", "")
	org, _ := st.CreateOrg(ctx, "maint-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "maint-cluster", u.ID)
	now := time.Now().UTC()

	// No windows → not in maintenance.
	if in, _ := st.InMaintenance(ctx, cl.ID, "payments", now); in {
		t.Fatalf("expected not in maintenance initially")
	}

	// Cluster-wide active window suppresses ANY namespace.
	wide, err := st.CreateMaintenanceWindow(ctx, org.ID, cl.ID, "cluster deploy", "", now.Add(-time.Minute), now.Add(time.Hour), &u.ID)
	if err != nil {
		t.Fatalf("create wide: %v", err)
	}
	if wide.Status != "active" {
		t.Fatalf("expected active, got %s", wide.Status)
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "anything", now); !in {
		t.Fatalf("cluster-wide window should suppress any namespace")
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "", now); !in {
		t.Fatalf("cluster-wide window should suppress empty namespace")
	}
	// End it — no longer active. EndMaintenanceWindow sets ends_at to the DB's
	// now(), a few ms after the test's captured `now`, so re-read a fresh `now`
	// for all subsequent checks (otherwise the just-ended window still looks
	// active relative to the stale timestamp).
	if err := st.EndMaintenanceWindow(ctx, cl.ID, wide.ID); err != nil {
		t.Fatalf("end wide: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	now = time.Now().UTC()
	if in, _ := st.InMaintenance(ctx, cl.ID, "anything", now); in {
		t.Fatalf("ended window must not suppress")
	}

	// Namespace-scoped active window suppresses only that namespace.
	nsw, err := st.CreateMaintenanceWindow(ctx, org.ID, cl.ID, "payments upgrade", "payments", now.Add(-time.Minute), now.Add(time.Hour), &u.ID)
	if err != nil {
		t.Fatalf("create ns: %v", err)
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "payments", now); !in {
		t.Fatalf("ns window should suppress its namespace")
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "checkout", now); in {
		t.Fatalf("ns window must NOT suppress a different namespace")
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "", now); in {
		t.Fatalf("ns window must NOT suppress an empty (cluster-wide) rule")
	}
	_ = nsw

	// Scheduled (future) window is not active yet.
	sched, err := st.CreateMaintenanceWindow(ctx, org.ID, cl.ID, "next week", "", now.Add(24*time.Hour), now.Add(25*time.Hour), &u.ID)
	if err != nil {
		t.Fatalf("create scheduled: %v", err)
	}
	if sched.Status != "scheduled" {
		t.Fatalf("expected scheduled, got %s", sched.Status)
	}
	if in, _ := st.InMaintenance(ctx, cl.ID, "anything", now); in {
		t.Fatalf("scheduled window must not suppress yet")
	}
	// Ending a scheduled (not active) window deletes it.
	if err := st.EndMaintenanceWindow(ctx, cl.ID, sched.ID); err != nil {
		t.Fatalf("end scheduled: %v", err)
	}
	t.Logf("maintenance suppression OK")
}
