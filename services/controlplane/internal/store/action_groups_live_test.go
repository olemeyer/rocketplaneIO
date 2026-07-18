package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// action_groups_live_test.go verifies Safe Actions v2 grouping against real
// Postgres (RP_LIVE_PG-gated, like the other live tests): group-of-one default
// and atomic group_seq on AppendAction.
func TestActionGroupsLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the action-groups test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@grp.local", "Grp", "")
	org, _ := st.CreateOrg(ctx, "grp-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "grp-cluster", u.ID)
	p := json.RawMessage(`{"replicas":3}`)

	// 1) group-of-one: CreateAction opens its own group at seq 0.
	a1, err := st.CreateAction(ctx, cl.ID, u.ID, "scale", "shop", "Deployment", "checkout", p)
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	if a1.GroupID == nil {
		t.Fatalf("group-of-one: expected a group_id, got nil")
	}
	if a1.GroupSeq != 0 {
		t.Fatalf("group-of-one: expected seq 0, got %d", a1.GroupSeq)
	}
	a2, _ := st.CreateAction(ctx, cl.ID, u.ID, "restart", "shop", "Deployment", "catalog", p)
	if *a2.GroupID == *a1.GroupID {
		t.Fatalf("group-of-one: two lone actions must NOT share a group")
	}

	// 2) explicit group: AppendAction assigns atomic increasing seq 0,1,2.
	g, err := st.CreateGroup(ctx, cl.ID, u.ID, "manual_batch", "batch", nil, "all_or_nothing", "stop")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if g.Atomicity != "all_or_nothing" || g.OnFailure != "stop" {
		t.Fatalf("group flags not persisted: %+v", g)
	}
	for i := 0; i < 3; i++ {
		a, err := st.AppendAction(ctx, g.ID, cl.ID, u.ID, "scale", "shop", "Deployment", "svc", "builtin", p)
		if err != nil {
			t.Fatalf("AppendAction %d: %v", i, err)
		}
		if a.GroupSeq != i {
			t.Fatalf("AppendAction: expected seq %d, got %d", i, a.GroupSeq)
		}
		if *a.GroupID != g.ID {
			t.Fatalf("AppendAction: wrong group")
		}
	}
}
