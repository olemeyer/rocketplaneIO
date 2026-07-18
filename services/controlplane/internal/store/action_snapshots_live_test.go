package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// action_snapshots_live_test.go verifies the durable ordered snapshot list
// (RP_LIVE_PG-gated): AppendActionSnapshots preserves order across batches and
// GetActionSnapshots returns the full list — the substrate for generic
// crash-restore + manual revert.
func TestActionSnapshotsLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the action-snapshots test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@sn.local", "Sn", "")
	org, _ := st.CreateOrg(ctx, "sn-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "sn-cluster", u.ID)
	g, _ := st.CreateGroup(ctx, cl.ID, u.ID, "manual_single", "snap", nil, "", "")
	a, _ := st.AppendAction(ctx, g.ID, cl.ID, u.ID, "scale", "shop", "Deployment", "checkout", "builtin", json.RawMessage(`{}`))
	// Snapshots are only appendable while the run is executing (the agent
	// reports them as mutations commit) — claim the run like the agent would.
	if _, err := pool.Exec(ctx, `UPDATE cluster_actions SET status='running' WHERE id=$1`, a.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	// two append batches — order must be preserved across them.
	b1 := json.RawMessage(`[{"seq":0,"kind":"Deployment","name":"checkout","scope":"object"}]`)
	b2 := json.RawMessage(`[{"seq":1,"kind":"ConfigMap","name":"cfg","scope":"field"}]`)
	if err := st.AppendActionSnapshots(ctx, cl.ID, a.ID, b1); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := st.AppendActionSnapshots(ctx, cl.ID, a.ID, b2); err != nil {
		t.Fatalf("append 2: %v", err)
	}

	got, err := st.GetActionSnapshots(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var list []map[string]any
	if err := json.Unmarshal(got, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 2 || list[0]["kind"] != "Deployment" || list[1]["kind"] != "ConfigMap" {
		t.Fatalf("snapshot list not appended in order: %s", got)
	}

	// a malformed (non-array) batch must be rejected, not corrupt the list.
	if err := st.AppendActionSnapshots(ctx, cl.ID, a.ID, json.RawMessage(`{"not":"array"}`)); err == nil {
		t.Fatal("expected non-array batch to be rejected")
	}
}
