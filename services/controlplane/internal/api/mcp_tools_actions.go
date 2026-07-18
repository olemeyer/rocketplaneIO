package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// mcp_tools_actions.go — the operation MCP tools. The catalog is gone: agents
// express arbitrary operations through kubectl-shaped primitives and
// rocketplaneIO classifies each one (actions_policy.go). Enforcement lives
// HERE, in one place: (a) anything above read level requires an open
// transaction, (b) the org's approval policy parks gated runs as
// awaiting_approval, (c) role comes from the token (mutations need an admin
// token — enforced inside createActionCore).

// actionResultView shapes a run for the agent.
func actionResultView(a *model.Action, level string, extra map[string]any) map[string]any {
	v := map[string]any{
		"actionId": a.ID,
		"kind":     a.Kind,
		"target":   fmt.Sprintf("%s/%s/%s", a.TargetNamespace, a.TargetKind, a.TargetName),
		"status":   a.Status,
		"level":    level,
	}
	if a.Result != "" {
		v["result"] = a.Result
	}
	for k, val := range extra {
		v[k] = val
	}
	return v
}

func (s *Server) registerMCPActionTools(srv *mcp.Server) {
	type getIn struct {
		Kind       string `json:"kind" jsonschema:"resource kind, e.g. Deployment, Pod, Certificate (any kind incl. CRDs)"`
		Name       string `json:"name" jsonschema:"object name"`
		Namespace  string `json:"namespace,omitempty" jsonschema:"namespace; omit for cluster-scoped resources"`
		APIVersion string `json:"apiVersion,omitempty" jsonschema:"apiVersion for non-core kinds, e.g. cert-manager.io/v1"`
		Resource   string `json:"resource,omitempty" jsonschema:"resource plural override for CRDs when the lowercase+s heuristic fails"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_get",
		Description: "Read one Kubernetes object of ANY kind (incl. CRDs). Secrets are redacted. " +
			"Runs without a transaction; logged when one is open.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		params, _ := json.Marshal(map[string]any{"apiVersion": in.APIVersion, "resource": in.Resource})
		return s.mcpRunOperation(ctx, sc, "k8s_get", createActionReq{
			Kind: "k8s_get", TargetNamespace: in.Namespace, TargetKind: in.Kind, TargetName: in.Name, Params: params,
		}, 60)
	})

	type listIn struct {
		Kind       string `json:"kind" jsonschema:"resource kind, e.g. Pod, Deployment (any kind incl. CRDs)"`
		Namespace  string `json:"namespace,omitempty" jsonschema:"namespace; omit for all namespaces / cluster-scoped"`
		Selector   string `json:"selector,omitempty" jsonschema:"label selector, e.g. app=checkout"`
		APIVersion string `json:"apiVersion,omitempty" jsonschema:"apiVersion for non-core kinds"`
		Resource   string `json:"resource,omitempty" jsonschema:"resource plural override for CRDs"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_list",
		Description: "List Kubernetes objects of ANY kind (incl. CRDs), optionally filtered by namespace " +
			"and label selector. Runs without a transaction; logged when one is open.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		params, _ := json.Marshal(map[string]any{"apiVersion": in.APIVersion, "resource": in.Resource, "selector": in.Selector})
		return s.mcpRunOperation(ctx, sc, "k8s_list", createActionReq{
			Kind: "k8s_list", TargetNamespace: in.Namespace, TargetKind: in.Kind, TargetName: "*", Params: params,
		}, 60)
	})

	type patchIn struct {
		Kind        string         `json:"kind" jsonschema:"resource kind (any kind incl. CRDs)"`
		Name        string         `json:"name" jsonschema:"object name"`
		Namespace   string         `json:"namespace,omitempty" jsonschema:"namespace; omit for cluster-scoped resources"`
		Patch       map[string]any `json:"patch" jsonschema:"JSON merge patch (or strategic merge patch if strategic=true), e.g. {\"spec\":{\"replicas\":3}}"`
		Strategic   bool           `json:"strategic,omitempty" jsonschema:"use strategic-merge-patch semantics (core kinds only)"`
		APIVersion  string         `json:"apiVersion,omitempty" jsonschema:"apiVersion for non-core kinds"`
		Resource    string         `json:"resource,omitempty" jsonschema:"resource plural override for CRDs"`
		WaitSeconds int            `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the result (default 60, max 300)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_patch",
		Description: "Patch ANY Kubernetes object (incl. CRDs). REQUIRES an open transaction — the " +
			"before-state is captured durably and restored on cancel/expiry. Classified reversible " +
			"(scale-to-0 counts as destructive and may need approval).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in patchIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		params, _ := json.Marshal(map[string]any{
			"patch": in.Patch, "strategic": in.Strategic, "apiVersion": in.APIVersion, "resource": in.Resource,
		})
		return s.mcpRunOperation(ctx, sc, "k8s_patch", createActionReq{
			Kind: "k8s_patch", TargetNamespace: in.Namespace, TargetKind: in.Kind, TargetName: in.Name, Params: params,
		}, in.WaitSeconds)
	})

	type applyIn struct {
		Manifest    map[string]any `json:"manifest" jsonschema:"full object manifest (apiVersion, kind, metadata.name required) — server-side apply"`
		Resource    string         `json:"resource,omitempty" jsonschema:"resource plural override for CRDs"`
		WaitSeconds int            `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the result (default 60, max 300)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_apply",
		Description: "Server-side-apply a full manifest for ANY kind (creates or updates; incl. CRDs). " +
			"REQUIRES an open transaction — the before-state (or absence) is captured, so rollback " +
			"restores or deletes the object.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in applyIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		params, _ := json.Marshal(map[string]any{"manifest": in.Manifest, "resource": in.Resource})
		return s.mcpRunOperation(ctx, sc, "k8s_apply", createActionReq{
			Kind: "k8s_apply", Params: params,
		}, in.WaitSeconds)
	})

	type deleteIn struct {
		Kind        string `json:"kind" jsonschema:"resource kind (any kind incl. CRDs)"`
		Name        string `json:"name" jsonschema:"object name"`
		Namespace   string `json:"namespace,omitempty" jsonschema:"namespace; omit for cluster-scoped resources"`
		APIVersion  string `json:"apiVersion,omitempty" jsonschema:"apiVersion for non-core kinds"`
		Resource    string `json:"resource,omitempty" jsonschema:"resource plural override for CRDs"`
		WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the result (default 60, max 300)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_delete",
		Description: "Delete ANY Kubernetes object. REQUIRES an open transaction — the whole object is " +
			"captured first, so rollback recreates it. Pods/Jobs are disruptive; everything else is " +
			"destructive and typically needs human approval.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deleteIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		params, _ := json.Marshal(map[string]any{"apiVersion": in.APIVersion, "resource": in.Resource})
		return s.mcpRunOperation(ctx, sc, "k8s_delete", createActionReq{
			Kind: "k8s_delete", TargetNamespace: in.Namespace, TargetKind: in.Kind, TargetName: in.Name, Params: params,
		}, in.WaitSeconds)
	})

	type execIn struct {
		Namespace      string   `json:"namespace" jsonschema:"pod namespace"`
		Pod            string   `json:"pod" jsonschema:"pod name"`
		Command        []string `json:"command" jsonschema:"argv array, e.g. [\"/bin/sh\",\"-c\",\"redis-cli flushall\"]"`
		Container      string   `json:"container,omitempty" jsonschema:"container name (default: first container)"`
		TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"command timeout 1..300s (default 30)"`
		WaitSeconds    int      `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the result (default 60, max 300)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "k8s_exec",
		Description: "Run an ARBITRARY command in a running container. NOT reversible — always classified " +
			"destructive: requires an open transaction AND (under the default policy) human approval.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		pm := map[string]any{"command": in.Command, "container": in.Container}
		if in.TimeoutSeconds > 0 {
			if in.TimeoutSeconds < 10 {
				in.TimeoutSeconds = 10
			}
			if in.TimeoutSeconds > 300 {
				in.TimeoutSeconds = 300
			}
			pm["timeoutSeconds"] = in.TimeoutSeconds
		}
		params, _ := json.Marshal(pm)
		return s.mcpRunOperation(ctx, sc, "k8s_exec", createActionReq{
			Kind: "k8s_exec", TargetNamespace: in.Namespace, TargetKind: "Pod", TargetName: in.Pod, Params: params,
		}, in.WaitSeconds)
	})

	type runScriptIn struct {
		Source         string            `json:"source" jsonschema:"Starlark script source (k8s.get/list/patch/apply/delete/exec_raw, snapshot(), step(), fail(), …)"`
		Args           map[string]string `json:"args,omitempty" jsonschema:"named string args passed to the script"`
		Title          string            `json:"title,omitempty" jsonschema:"short human-readable name for the run"`
		TimeoutSeconds int               `json:"timeoutSeconds,omitempty" jsonschema:"script timeout in seconds (30..1800, default 600)"`
		WaitSeconds    int               `json:"waitSeconds,omitempty" jsonschema:"how long to wait for the result (default 60, max 300)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name: "run_script",
		Description: "Run a multi-step Starlark workflow over the snapshot substrate (destructive: requires " +
			"an open transaction AND — under the default policy — human approval). Every mutation the " +
			"script performs is auto-snapshotted; the transaction rollback restores all of it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in runScriptIn) (*mcp.CallToolResult, any, error) {
		sc, err := mcpScopeFrom(ctx)
		if err != nil {
			return nil, nil, err
		}
		if in.Source == "" {
			return nil, nil, errors.New("source is required")
		}
		return s.mcpRunOperation(ctx, sc, "run_script", createActionReq{
			Kind: "script", Source: in.Source, Title: in.Title, TimeoutSeconds: in.TimeoutSeconds, ScriptArgs: in.Args,
		}, in.WaitSeconds)
	})
}

// mcpRunOperation is the shared body of every operation tool: transaction
// enforcement → core create (validation/classification/RBAC/gating) → event
// logging → bounded result wait.
func (s *Server) mcpRunOperation(ctx context.Context, sc *mcpScope, tool string, car createActionReq, waitSeconds int) (*mcp.CallToolResult, any, error) {
	// Pre-classify to decide whether a transaction is required. The core
	// re-classifies authoritatively after param normalization.
	var lvlParams map[string]any
	_ = json.Unmarshal(car.Params, &lvlParams)
	level := actionLevel(car.Kind, car.TargetKind, lvlParams)

	var txn *model.MCPTransaction
	var txnID *uuid.UUID
	var gateLevels []string
	if level != "read" {
		var err error
		txn, err = s.requireOpenTxn(ctx, sc)
		if err != nil {
			return nil, nil, err
		}
		txnID = &txn.ID
		gateLevels = s.store.GetMCPPolicy(ctx, sc.orgID).ApprovalLevels
	} else if sc.tokenID != nil {
		// Read-level runs join the open transaction's log when one exists.
		if t, err := s.store.GetOpenMCPTransactionForToken(ctx, sc.clusterID, *sc.tokenID); err == nil {
			txn = t
			txnID = &t.ID
		}
	}

	a, coreLevel, status, msg := s.createActionCore(ctx, sc.orgID, sc.clusterID, sc.role,
		sc.userID, sc.userEmail, car, txnID, gateLevels, "mcp")
	if status != 0 {
		return nil, nil, fmt.Errorf("%s", msg)
	}
	if txn != nil {
		payload := map[string]any{"kind": a.Kind, "target": a.TargetNamespace + "/" + a.TargetKind + "/" + a.TargetName, "level": coreLevel}
		if car.Kind == "script" {
			payload["title"] = a.TargetName
		}
		_ = s.store.AppendTxnEvent(ctx, txn.ID, "action_created", tool, &a.ID, payload)
		s.broker.Publish(sc.clusterID, "transactions", 0)
	}

	if a.Status == "awaiting_approval" {
		return mcpJSON(actionResultView(a, coreLevel, map[string]any{
			"note": "parked for human approval (org policy) — a human must approve it in the rocketplaneIO UI; " +
				"use wait_approval with this actionId, or get_transaction to poll",
		})), nil, nil
	}

	// Bounded wait for the agent's result.
	if waitSeconds <= 0 {
		waitSeconds = 60
	}
	if waitSeconds > 300 {
		waitSeconds = 300
	}
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	for {
		st, _, result, err := s.store.GetActionApproval(ctx, sc.clusterID, a.ID)
		if err != nil {
			return nil, nil, err
		}
		if st == "succeeded" || st == "failed" || st == "cancelled" {
			a.Status, a.Result = st, result
			if txn != nil {
				_ = s.store.AppendTxnEvent(ctx, txn.ID, "action_result", tool, &a.ID,
					map[string]any{"status": st, "result": truncateForEvent(result)})
				s.broker.Publish(sc.clusterID, "transactions", 0)
			}
			return mcpJSON(actionResultView(a, coreLevel, nil)), nil, nil
		}
		if time.Now().After(deadline) {
			a.Status = st
			return mcpJSON(actionResultView(a, coreLevel, map[string]any{
				"timeout": true,
				"note":    "still " + st + " — the run continues; poll get_transaction or list_actions for the result",
			})), nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(1200 * time.Millisecond):
		}
	}
}

// truncateForEvent keeps timeline payloads small; the full result lives on the
// action row.
func truncateForEvent(s string) string {
	if len(s) > 2048 {
		return s[:2048] + "… (truncated)"
	}
	return s
}
