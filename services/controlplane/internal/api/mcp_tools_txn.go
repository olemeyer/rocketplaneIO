package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// mcp_tools_txn.go — the transaction lifecycle tools. A transaction is the
// unit of trust an external agent operates under: everything is logged under
// it, every mutation is snapshotted, and cancel/expiry rolls it all back.

// txnView is what the agent sees of a transaction — enough to reason about
// state, deadline pressure and pending approvals.
func txnView(t *model.MCPTransaction) map[string]any {
	v := map[string]any{
		"transactionId": t.ID,
		"title":         t.Title,
		"status":        t.Status,
		"deadline":      t.Deadline.UTC().Format(time.RFC3339),
		"createdAt":     t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.CloseReason != "" {
		v["closeReason"] = t.CloseReason
	}
	if t.IncidentID != nil {
		v["incidentId"] = t.IncidentID
	}
	if t.RollbackActionID != nil {
		v["rollbackActionId"] = t.RollbackActionID
	}
	if len(t.Actions) > 0 {
		acts := make([]map[string]any, 0, len(t.Actions))
		for _, a := range t.Actions {
			acts = append(acts, map[string]any{
				"actionId": a.ID,
				"kind":     a.Kind,
				"target":   fmt.Sprintf("%s/%s/%s", a.TargetNamespace, a.TargetKind, a.TargetName),
				"status":   a.Status,
				"result":   a.Result,
			})
		}
		v["actions"] = acts
	}
	return v
}

func (s *Server) registerMCPTxnTools(srv *mcp.Server) {
	type beginIn struct {
		Title      string `json:"title" jsonschema:"what you intend to do — shown to humans in the transaction list"`
		TTLSeconds int    `json:"ttlSeconds,omitempty" jsonschema:"transaction lifetime in seconds; if you neither commit nor cancel before the deadline, EVERYTHING is rolled back automatically (default/max come from the org policy)"`
		IncidentID string `json:"incidentId,omitempty" jsonschema:"optional incident UUID to attach this transaction to"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "begin_transaction",
		Description: "Open the transaction that all mutations MUST run under. Every change is snapshotted " +
			"durably; cancel_transaction (or the deadline) restores every before-state in reverse order. " +
			"One open transaction per token — a second begin returns the existing one.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in beginIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		if in.Title == "" {
			return nil, nil, errors.New("title is required — state what you intend to do")
		}
		pol := s.store.GetMCPPolicy(ctx, sc.orgID)
		ttl := in.TTLSeconds
		if ttl <= 0 {
			ttl = pol.DefaultTTLSeconds
		}
		if ttl > pol.MaxTTLSeconds {
			ttl = pol.MaxTTLSeconds
		}
		if ttl < 30 {
			ttl = 30
		}
		var incidentID *uuid.UUID
		if in.IncidentID != "" {
			id, perr := uuid.Parse(in.IncidentID)
			if perr != nil {
				return nil, nil, errors.New("incidentId is not a valid UUID")
			}
			incidentID = &id
		}
		txn, err := s.store.CreateMCPTransaction(ctx, sc.orgID, sc.clusterID, sc.tokenID, sc.userID,
			incidentID, in.Title, time.Duration(ttl)*time.Second)
		if errors.Is(err, store.ErrTxnAlreadyOpen) {
			v := txnView(txn)
			v["note"] = "this token already has an open transaction on this cluster — reusing it; commit or cancel it to start fresh"
			return mcpJSON(v), nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		s.broker.Publish(sc.clusterID, "transactions", 0)
		v := txnView(txn)
		v["approvalPolicy"] = map[string]any{
			"levelsRequiringApproval": pol.ApprovalLevels,
			"note":                    "actions at these risk levels pause as awaiting_approval until a human approves in the rocketplaneIO UI",
		}
		return mcpJSON(v), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "commit_transaction",
		Description: "Close the open transaction KEEPING all changes. Snapshots stay available for manual " +
			"revert by humans.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		txn, err := s.requireOpenTxn(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
		// A commit with runs still in-flight would detach them from the
		// rollback guarantee mid-run — force the agent to wait for them.
		for _, a := range txn.Actions {
			if a.Status == "pending" || a.Status == "running" || a.Status == "awaiting_approval" {
				return nil, nil, fmt.Errorf("action %s (%s) is still %s — wait for it to finish (get_transaction) or cancel_transaction", a.ID, a.Kind, a.Status)
			}
		}
		out, err := s.store.CommitMCPTransaction(ctx, sc.clusterID, txn.ID)
		if err != nil {
			return nil, nil, err
		}
		s.broker.Publish(sc.clusterID, "transactions", 0)
		return mcpJSON(txnView(out)), nil, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "cancel_transaction",
		Description: "Close the open transaction ROLLING BACK every change: running actions are aborted, " +
			"pending approvals rejected, and one restore run replays all snapshots in reverse (LIFO). " +
			"Poll get_transaction until status is rolled_back.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		txn, err := s.requireOpenTxn(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
		if err := s.store.MarkTxnCancelling(ctx, sc.clusterID, txn.ID, "cancel"); err != nil {
			return nil, nil, err
		}
		// Fast path: drive the rollback immediately — the leader reaper picks
		// up whatever is not terminal yet (≤30s cadence).
		s.driveTxnRollbacksNow(ctx)
		out, err := s.store.GetMCPTransaction(ctx, sc.clusterID, txn.ID)
		if err != nil {
			return nil, nil, err
		}
		s.broker.Publish(sc.clusterID, "transactions", 0)
		return mcpJSON(txnView(out)), nil, nil
	})

	type getIn struct {
		TransactionID string `json:"transactionId,omitempty" jsonschema:"transaction UUID; omit for this token's open transaction"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_transaction",
		Description: "Read a transaction's state: status (open/committed/cancelling/rolled_back/" +
			"rollback_failed), deadline, and every action with its status incl. awaiting_approval.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		var txn *model.MCPTransaction
		if in.TransactionID != "" {
			id, perr := uuid.Parse(in.TransactionID)
			if perr != nil {
				return nil, nil, errors.New("transactionId is not a valid UUID")
			}
			txn, err = s.store.GetMCPTransaction(ctx, sc.clusterID, id)
		} else {
			txn, err = s.requireOpenTxn(ctx, sc)
			if err == nil {
				txn, err = s.store.GetMCPTransaction(ctx, sc.clusterID, txn.ID)
			}
		}
		if err != nil {
			return nil, nil, err
		}
		return mcpJSON(txnView(txn)), nil, nil
	})

	type waitIn struct {
		ActionID       string `json:"actionId" jsonschema:"the awaiting_approval action to wait for"`
		TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"max seconds to wait (default 60, max 120)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "wait_approval",
		Description: "Block until a human approves or rejects an awaiting_approval action (bounded " +
			"long-poll). Returns the final state, or timeout — then poll again or use get_transaction.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		actionID, perr := uuid.Parse(in.ActionID)
		if perr != nil {
			return nil, nil, errors.New("actionId is not a valid UUID")
		}
		timeout := in.TimeoutSeconds
		if timeout <= 0 {
			timeout = 60
		}
		if timeout > 120 {
			timeout = 120
		}
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		for {
			status, approval, result, err := s.store.GetActionApproval(ctx, sc.clusterID, actionID)
			if err != nil {
				return nil, nil, err
			}
			if status != "awaiting_approval" {
				return mcpJSON(map[string]any{
					"actionId": actionID, "status": status, "approvalState": approval, "result": result,
				}), nil, nil
			}
			if time.Now().After(deadline) {
				return mcpJSON(map[string]any{
					"actionId": actionID, "status": status, "approvalState": approval,
					"timeout": true, "note": "still awaiting approval — a human has not decided yet",
				}), nil, nil
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}
	})
}

// driveTxnRollbacksNow runs one best-effort rollback pass and publishes the
// resulting broker signals (cancel/dispatch/invalidations). Shared by the
// cancel tool (fast path) and the leader reaper loop.
func (s *Server) driveTxnRollbacksNow(ctx context.Context) {
	signals, err := s.store.DriveTxnRollbacks(ctx)
	if err != nil {
		return
	}
	s.publishTxnSignals(signals)
}

func (s *Server) publishTxnSignals(signals []store.TxnSignal) {
	for _, sig := range signals {
		if sig.ActionID != uuid.Nil {
			s.broker.PublishData(sig.ClusterID, "cancel", fmt.Sprintf(`{"actionId":%q}`, sig.ActionID))
		}
		if sig.Dispatch {
			s.broker.Publish(sig.ClusterID, "dispatch", 0)
			s.broker.Publish(sig.ClusterID, "actions", 0)
		}
		s.broker.Publish(sig.ClusterID, "transactions", 0)
	}
}
