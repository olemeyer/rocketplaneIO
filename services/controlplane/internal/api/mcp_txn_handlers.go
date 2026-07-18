package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/alerts"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// mcp_txn_handlers.go — the browser-facing API for MCP transactions: the
// transactions list + timeline pages, the approval buttons, the org policy
// editor and a human cancel (kill switch for a runaway agent).

// handleListTransactions — GET /api/orgs/{org}/clusters/{cluster}/transactions
func (s *Server) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	txns, err := s.store.ListMCPTransactions(r.Context(), clusterID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list transactions")
		return
	}
	writeJSON(w, http.StatusOK, txns)
}

// handleGetTransaction — GET /api/orgs/{org}/clusters/{cluster}/transactions/{txn}
func (s *Server) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("txn"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid transaction id")
		return
	}
	txn, err := s.store.GetMCPTransaction(r.Context(), clusterID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "transaction not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load transaction")
		return
	}
	writeJSON(w, http.StatusOK, txn)
}

// handleListTransactionEvents — GET …/transactions/{txn}/events?from=<seq>
func (s *Server) handleListTransactionEvents(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("txn"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid transaction id")
		return
	}
	// Scope check: the transaction must belong to the cluster.
	if _, err := s.store.GetMCPTransaction(r.Context(), clusterID, id); err != nil {
		writeErr(w, http.StatusNotFound, "transaction not found")
		return
	}
	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	events, err := s.store.ListTxnEvents(r.Context(), id, from, 0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list events")
		return
	}
	writeJSON(w, http.StatusOK, events)
}

// handleCancelTransaction — POST …/transactions/{txn}/cancel
// The human kill switch: closes an external agent's transaction and rolls
// everything back, exactly like the agent's own cancel_transaction.
func (s *Server) handleCancelTransaction(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("txn"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid transaction id")
		return
	}
	if err := s.store.MarkTxnCancelling(r.Context(), clusterID, id, "cancel"); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusConflict, "transaction is not open")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to cancel transaction")
		return
	}
	s.driveTxnRollbacksNow(r.Context())
	s.audit(r, &orgID, "mcp.transaction_cancelled", "transaction", id.String(), "", nil)
	s.broker.Publish(clusterID, "transactions", 0)
	txn, err := s.store.GetMCPTransaction(r.Context(), clusterID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load transaction")
		return
	}
	writeJSON(w, http.StatusOK, txn)
}

// handleRevertTransaction — POST …/transactions/{txn}/revert
// Undo a COMMITTED transaction after the fact: opens a "revert: …" transaction
// carrying one snapshot_restore with the source's concatenated captures.
func (s *Server) handleRevertTransaction(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("txn"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid transaction id")
		return
	}
	user, _ := auth.UserFrom(r.Context())
	revertTxn, err := s.store.RevertMCPTransaction(r.Context(), clusterID, id, user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, &orgID, "mcp.transaction_reverted", "transaction", id.String(), "", nil)
	s.broker.Publish(clusterID, "transactions", 0)
	s.broker.Publish(clusterID, "actions", 0)
	s.broker.Publish(clusterID, "dispatch", 0)
	writeJSON(w, http.StatusOK, revertTxn)
}

// handleApproveAction — POST …/actions/{action}/approve
func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	s.decideAction(w, r, true)
}

// handleRejectAction — POST …/actions/{action}/reject
func (s *Server) handleRejectAction(w http.ResponseWriter, r *http.Request) {
	s.decideAction(w, r, false)
}

func (s *Server) decideAction(w http.ResponseWriter, r *http.Request, approve bool) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	// Approval is a human act: an API token must not approve its own proposals.
	if _, isToken := auth.TokenFrom(r.Context()); isToken {
		writeErr(w, http.StatusForbidden, "approvals require an interactive session — an api token cannot approve actions")
		return
	}
	actionID, err := uuid.Parse(r.PathValue("action"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid action id")
		return
	}
	user, _ := auth.UserFrom(r.Context())

	var txnID *uuid.UUID
	verb := "approved"
	if approve {
		txnID, err = s.store.ApproveAction(r.Context(), clusterID, actionID, user.ID)
	} else {
		verb = "rejected"
		txnID, err = s.store.RejectAction(r.Context(), clusterID, actionID, user.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusConflict, "action is not awaiting approval")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to decide approval")
		return
	}
	if txnID != nil {
		_ = s.store.AppendTxnEvent(r.Context(), *txnID, "approval_decided", "", &actionID,
			map[string]any{"decision": verb, "by": user.Email})
		s.broker.Publish(clusterID, "transactions", 0)
	}
	s.audit(r, &orgID, "action."+verb, "action", actionID.String(), "", nil)
	s.broker.Publish(clusterID, "actions", 0)
	if approve {
		// The released run is claimable now — wake the agent stream.
		s.broker.Publish(clusterID, "dispatch", 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actionId": actionID, "decision": verb})
}

// notifyApprovalRequested pings the org's configured alert providers when an
// external agent's action parks for approval — humans should not have to keep
// the transactions page open to notice. Best-effort, runs detached.
func (s *Server) notifyApprovalRequested(orgID, clusterID uuid.UUID, a *model.Action, level string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pol := s.store.GetMCPPolicy(ctx, orgID)
	if len(pol.NotifyProviderIDs) == 0 {
		return
	}
	provs, err := s.store.ProvidersByIDs(ctx, pol.NotifyProviderIDs)
	if err != nil {
		return
	}
	target := a.TargetNamespace + "/" + a.TargetKind + "/" + a.TargetName
	p := &alerts.Payload{
		Rule:      "mcp approval required",
		State:     "firing",
		Severity:  level,
		Kind:      a.Kind,
		ClusterID: clusterID.String(),
		At:        time.Now(),
		Message: fmt.Sprintf("An external AI agent proposed %s on %s (%s level) — approve or reject it: %s/clusters/%s/transactions",
			a.Kind, target, level, s.cfg.PublicURL, clusterID),
	}
	client := &http.Client{Timeout: 8 * time.Second}
	for i := range provs {
		_ = alerts.SendNotification(ctx, client, &provs[i], p)
	}
}

// handleGetMCPPolicy — GET /api/orgs/{org}/settings/mcp
func (s *Server) handleGetMCPPolicy(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.resolveOrgRole(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.GetMCPPolicy(r.Context(), orgID))
}

// handleSetMCPPolicy — PUT /api/orgs/{org}/settings/mcp (admin, session only)
func (s *Server) handleSetMCPPolicy(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	if _, isToken := auth.TokenFrom(r.Context()); isToken {
		writeErr(w, http.StatusForbidden, "the mcp policy is managed in an interactive session only")
		return
	}
	var pol model.MCPPolicy
	if !decode(w, r, &pol) {
		return
	}
	for _, l := range pol.ApprovalLevels {
		if l != "reversible" && l != "disruptive" && l != "destructive" {
			writeErr(w, http.StatusBadRequest, "approvalLevels may contain only reversible, disruptive, destructive")
			return
		}
	}
	if pol.DefaultTTLSeconds < 30 || pol.DefaultTTLSeconds > 86400 {
		writeErr(w, http.StatusBadRequest, "defaultTtlSeconds must be 30..86400")
		return
	}
	if pol.MaxTTLSeconds < pol.DefaultTTLSeconds || pol.MaxTTLSeconds > 86400 {
		writeErr(w, http.StatusBadRequest, "maxTtlSeconds must be defaultTtlSeconds..86400")
		return
	}
	raw, _ := json.Marshal(pol)
	if err := s.store.SetOrgSetting(r.Context(), orgID, "mcp", raw); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save policy")
		return
	}
	s.audit(r, &orgID, "mcp.policy_updated", "org", orgID.String(), "", map[string]any{"policy": pol})
	writeJSON(w, http.StatusOK, pol)
}
