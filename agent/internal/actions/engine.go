package actions

// engine.go is the runtime half of the v4 substrate. The recipe package
// guarantees a manifest is SAFE at parse (a recipe that declares itself
// reversible must name a compensation; a mutation must own a verify). This file
// RUNS it: executeManifest drives the audited pipeline (plan) and inverse
// (prepareRevert/prepareUndo) helpers under the manifest's declared contract,
// with three deltas over the legacy execute():
//
//  1. rollback runs on ANY step failure after the mutation (incl. a failed
//     verify), not only cancel/timeout;
//  2. the durable compensation is reported the instant it exists (a `running`
//     tick carries the revert), so an agent crash mid-run leaves a revertible
//     row for the CP's ReapCrashedAgents;
//  3. the manifest's declared reversibility is cross-checked against the inverse
//     the builder actually produced — a promise the code can't keep is logged.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/rocketplaneio/rocketplane/agent/internal/actions/recipe"
)

func (r *Runner) executeManifest(ctx context.Context, a Action, m *recipe.Manifest) {
	timeout := monitorTimeout
	var tp struct {
		TimeoutSeconds int `json:"timeoutSeconds"`
	}
	if len(a.Params) > 0 && json.Unmarshal(a.Params, &tp) == nil && tp.TimeoutSeconds > 0 {
		timeout = time.Duration(tp.TimeoutSeconds) * time.Second
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
		if timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
	}
	ctx, cancelFn := context.WithTimeout(ctx, timeout)
	defer cancelFn()

	var cancelled atomic.Bool
	requestCancel := func() {
		if cancelled.CompareAndSwap(false, true) {
			cancelFn()
		}
	}
	r.registerCancel(a.ID, requestCancel)
	defer r.unregisterCancel(a.ID)

	// The inverse is captured from before-state BEFORE the pipeline runs, so it
	// exists the moment anything could change. undoFn is the in-process rollback
	// (mutation only); revertSpec is the serializable inverse persisted for the
	// reaper + the UI.
	undoDesc, undoFn := r.prepareUndo(ctx, a)
	revertSpec := r.prepareRevert(ctx, a)
	snapshot := r.prepareSnapshot(ctx, a)

	// Cross-check the parse-time promise: if the manifest says the recipe is
	// reversible, the inverse builder must have produced something. A nil here
	// means the pre-mutation read failed — surface it, don't silently proceed
	// un-revertible.
	if m.Compensation == "builtin" && len(revertSpec) == 0 {
		log.Printf("actions: [v4] %s %s/%s — manifest declares reversible but the inverse builder yielded nothing (before-state unreadable?)",
			a.Kind, a.TargetNamespace, a.TargetName)
	}

	steps := r.plan(a)
	states := make([]stepState, len(steps))
	for i, s := range steps {
		states[i] = stepState{Name: s.name, Status: "pending"}
	}

	// Report the durable compensation once, up front (status still running). A
	// crash after any mutation now finds a revertible row.
	if len(revertSpec) > 0 {
		r.reportFull(ctx, a.ID, "running", "", "compensation armed", states, revertSpec, nil)
	}

	lastSent := time.Time{}
	push := func(progress string, force bool) {
		if !force && time.Since(lastSent) < progressMinGap {
			return
		}
		lastSent = time.Now()
		if r.report(ctx, a.ID, "running", "", progress, states) {
			requestCancel()
		}
		if r.onExecuted != nil {
			r.onExecuted()
		}
	}

	finish := func(status, result string) {
		log.Printf("actions: [v4] %s %s/%s → %s (%s)", a.Kind, a.TargetNamespace, a.TargetName, status, result)
		// Unlike execute(), the revert is attached on EVERY terminal status — it
		// is a durable inverse of before-state, valid whether the run succeeded,
		// failed, or was rolled back.
		r.reportFull(ctx, a.ID, status, result, "", states, revertSpec, snapshot)
		if r.onExecuted != nil {
			r.onExecuted()
		}
	}

	// rollback compensates via the pre-mutation undoFn. It runs on cancel,
	// timeout, AND a genuine step failure (delta 1).
	rollback := func(status, reason string) {
		rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer rcancel()
		if undoFn == nil {
			finish(status, reason+" — nothing to undo")
			return
		}
		states = append(states, stepState{Name: "rollback", Status: "running", Detail: undoDesc})
		idx := len(states) - 1
		push("rollback…", true)
		if err := undoFn(rctx); err != nil {
			states[idx].Status = "failed"
			states[idx].Detail = undoDesc + " — " + err.Error()
			finish(status, reason+" — rollback FAILED: "+err.Error())
			return
		}
		states[idx].Status = "ok"
		finish(status, reason+" — "+undoDesc)
	}

	var finalResult string
	for i, s := range steps {
		if cancelled.Load() {
			rollback("cancelled", "cancelled by user")
			return
		}
		states[i].Status = "running"
		push(s.name+"…", true)
		detail, err := s.run(ctx, func(d string) {
			states[i].Detail = d
			push(fmt.Sprintf("%s: %s", s.name, d), false)
		})
		if err != nil {
			states[i].Status = "failed"
			if cancelled.Load() {
				states[i].Detail = "cancelled"
				rollback("cancelled", "cancelled by user")
				return
			}
			if ctx.Err() == context.DeadlineExceeded {
				states[i].Detail = "timeout"
				rollback("cancelled", "timeout after "+timeout.String())
				return
			}
			if states[i].Detail == "" {
				states[i].Detail = err.Error()
			}
			// DELTA 1: a real step failure (e.g. verify never converged) rolls
			// back the committed mutation instead of leaving it standing.
			rollback("failed", fmt.Sprintf("%s failed: %v", s.name, err))
			return
		}
		states[i].Status = "ok"
		if detail != "" {
			states[i].Detail = detail
		}
		finalResult = states[i].Detail
		push(s.name+" ✓", true)
	}
	if cancelled.Load() {
		rollback("cancelled", "cancelled by user")
		return
	}
	finish("succeeded", finalResult)
}
