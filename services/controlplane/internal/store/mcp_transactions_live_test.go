package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mcp_transactions_live_test.go verifies the MCP transaction envelope against
// real Postgres (RP_LIVE_PG-gated): one-open-per-token, commit, cancel with
// approval auto-reject, combined snapshot-restore enqueue (idempotent), and
// deadline expiry.
func TestMCPTransactionsLive(t *testing.T) {
	dsn := os.Getenv("RP_LIVE_PG")
	if dsn == "" {
		t.Skip("set RP_LIVE_PG to run the mcp-transactions test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	st := New(pool)

	u, _ := st.UpsertUserFromOIDC(ctx, "sub-"+uuid.NewString(), uuid.NewString()+"@txn.local", "Txn", "")
	org, _ := st.CreateOrg(ctx, "txn-"+uuid.NewString()[:8], u.ID)
	cl, _, _ := st.CreateClusterWithEnrollToken(ctx, org.ID, "txn-cluster", u.ID)
	tokenID := createTestToken(t, ctx, pool, org.ID, u.ID)

	// 1) begin: one open transaction per (token, cluster).
	txn, err := st.CreateMCPTransaction(ctx, org.ID, cl.ID, &tokenID, u.ID, nil, "fix checkout", time.Minute)
	if err != nil {
		t.Fatalf("create txn: %v", err)
	}
	if txn.Status != "open" {
		t.Fatalf("expected open, got %s", txn.Status)
	}
	dup, err := st.CreateMCPTransaction(ctx, org.ID, cl.ID, &tokenID, u.ID, nil, "second", time.Minute)
	if !errors.Is(err, ErrTxnAlreadyOpen) {
		t.Fatalf("expected ErrTxnAlreadyOpen, got %v", err)
	}
	if dup == nil || dup.ID != txn.ID {
		t.Fatalf("conflict must return the existing open txn")
	}

	// 2) commit closes it; a new begin works afterwards.
	if _, err := st.CommitMCPTransaction(ctx, cl.ID, txn.ID); err != nil {
		t.Fatalf("commit: %v", err)
	}
	txn2, err := st.CreateMCPTransaction(ctx, org.ID, cl.ID, &tokenID, u.ID, nil, "round two", time.Minute)
	if err != nil {
		t.Fatalf("re-begin after commit: %v", err)
	}

	// 3) actions inside the txn: one normal (with snapshots), one parked for
	// approval. Cancel must auto-reject the parked one, then enqueue ONE
	// snapshot_restore with the captures.
	g, _ := st.CreateGroup(ctx, cl.ID, u.ID, "mcp", "scale checkout", nil, "", "")
	if err := st.LinkGroupToTransaction(ctx, g.ID, txn2.ID); err != nil {
		t.Fatalf("link group: %v", err)
	}
	a1, _ := st.AppendAction(ctx, g.ID, cl.ID, u.ID, "scale", "shop", "Deployment", "checkout", "builtin", json.RawMessage(`{"replicas":3}`))
	// Simulate the agent: claim → snapshots → succeeded.
	if _, err := pool.Exec(ctx, `UPDATE cluster_actions SET status='running' WHERE id=$1`, a1.ID); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	caps := json.RawMessage(`[{"seq":0,"kind":"Deployment","namespace":"shop","name":"checkout","existed":true,"scope":"object","object":{"spec":{"replicas":2}}}]`)
	if err := st.AppendActionSnapshots(ctx, cl.ID, a1.ID, caps); err != nil {
		t.Fatalf("append snapshots: %v", err)
	}
	if err := st.CompleteAction(ctx, cl.ID, a1.ID, "succeeded", "scaled", nil, nil); err != nil {
		t.Fatalf("complete: %v", err)
	}

	g2, _ := st.CreateGroup(ctx, cl.ID, u.ID, "mcp", "delete pod", nil, "", "")
	_ = st.LinkGroupToTransaction(ctx, g2.ID, txn2.ID)
	a2, _ := st.AppendAction(ctx, g2.ID, cl.ID, u.ID, "delete_pod", "shop", "Pod", "checkout-abc", "builtin", json.RawMessage(`{}`))
	if err := st.ParkActionForApproval(ctx, cl.ID, a2.ID); err != nil {
		t.Fatalf("park: %v", err)
	}

	if err := st.MarkTxnCancelling(ctx, cl.ID, txn2.ID, "cancel"); err != nil {
		t.Fatalf("mark cancelling: %v", err)
	}
	if _, err := st.DriveTxnRollbacks(ctx); err != nil {
		t.Fatalf("drive rollbacks: %v", err)
	}
	// Parked action auto-rejected.
	status, approval, _, _ := st.GetActionApproval(ctx, cl.ID, a2.ID)
	if status != "cancelled" || approval != "rejected" {
		t.Fatalf("parked action not auto-rejected: %s/%s", status, approval)
	}
	// One restore run enqueued, carrying the capture.
	after, err := st.GetMCPTransaction(ctx, cl.ID, txn2.ID)
	if err != nil {
		t.Fatalf("get txn: %v", err)
	}
	if after.Status != "cancelling" || after.RollbackActionID == nil {
		t.Fatalf("expected cancelling with rollback action, got %s / %v", after.Status, after.RollbackActionID)
	}
	// Idempotency: a second drive pass must not enqueue another restore.
	if _, err := st.DriveTxnRollbacks(ctx); err != nil {
		t.Fatalf("drive again: %v", err)
	}
	var restores int
	_ = pool.QueryRow(ctx, `
		SELECT count(*) FROM cluster_actions a JOIN action_groups g ON g.id=a.group_id
		WHERE g.transaction_id=$1 AND a.kind='snapshot_restore'`, txn2.ID).Scan(&restores)
	if restores != 1 {
		t.Fatalf("expected exactly 1 restore run, got %d", restores)
	}
	// Restore succeeds → transaction rolled_back.
	if _, err := pool.Exec(ctx, `UPDATE cluster_actions SET status='running' WHERE id=$1`, *after.RollbackActionID); err != nil {
		t.Fatalf("restore running: %v", err)
	}
	if err := st.CompleteAction(ctx, cl.ID, *after.RollbackActionID, "succeeded", "restored", nil, nil); err != nil {
		t.Fatalf("restore complete: %v", err)
	}
	if _, err := st.DriveTxnRollbacks(ctx); err != nil {
		t.Fatalf("drive final: %v", err)
	}
	final, _ := st.GetMCPTransaction(ctx, cl.ID, txn2.ID)
	if final.Status != "rolled_back" {
		t.Fatalf("expected rolled_back, got %s", final.Status)
	}

	// 4) expiry: an open txn past its deadline flips to cancelling (and with
	// no actions straight to rolled_back on the same reap pass).
	txn3, err := st.CreateMCPTransaction(ctx, org.ID, cl.ID, &tokenID, u.ID, nil, "expiring", time.Minute)
	if err != nil {
		t.Fatalf("create txn3: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE mcp_transactions SET deadline=now()-interval '1 second' WHERE id=$1`, txn3.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if _, err := st.ReapTransactions(ctx); err != nil {
		t.Fatalf("reap: %v", err)
	}
	expired, _ := st.GetMCPTransaction(ctx, cl.ID, txn3.ID)
	if expired.Status != "rolled_back" || expired.CloseReason != "expired" {
		t.Fatalf("expected expired+rolled_back, got %s/%s", expired.Status, expired.CloseReason)
	}

	// 5) timeline has the lifecycle events.
	events, err := st.ListTxnEvents(ctx, txn2.ID, 0, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Type)
	}
	want := map[string]bool{"opened": false, "cancel_requested": false, "rollback_started": false, "rollback_done": false}
	for _, k := range kinds {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("missing timeline event %q (got %v)", k, kinds)
		}
	}
}

// createTestToken inserts a minimal api_tokens row (the store's CreateAPIToken
// hashes real secrets; the transaction tests only need the id + org linkage).
func createTestToken(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, userID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO api_tokens (id, org_id, name, role, token_hash, prefix, created_by)
		VALUES ($1,$2,'e2e','admin',$3,'rp_test',$4)`, id, orgID, uuid.NewString(), userID)
	if err != nil {
		t.Skipf("api_tokens schema differs, skipping token insert: %v", err)
	}
	return id
}
