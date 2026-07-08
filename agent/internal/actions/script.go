package actions

// script.go — die Starlark-Engine für CUSTOM-Workflows: hermetischer Python-
// Dialekt (kein I/O, kein Import, kein while ⇒ garantierte Terminierung),
// eingebettet via go.starlark.net. Das Script spricht dieselbe Sprache wie
// die eingebauten Pipelines: step()/report() erzeugen die Live-Timeline in
// der UI, k8s.* sind die whitelisted Cluster-Handgriffe, wait_* die Observer.
//
// Vertrag (im UI-Editor dokumentiert):
//   args                                  — dict der Eingabe-Parameter (strings)
//   step("name")                          — beginnt einen neuen Timeline-Schritt
//   report("detail")                      — Live-Zeile des aktuellen Schritts
//   fail("msg")                           — bricht den Workflow ab (failed)
//   sleep(seconds)                        — max 30s je Aufruf
//   k8s.get(ns, kind, name)               — {"desired","ready","updated","available"}
//   k8s.pods(ns, selector="a=b")          — [{"name","ready","phase","restarts","node"}]
//   k8s.rollout_restart(ns, kind, name)
//   k8s.scale(ns, kind, name, replicas)
//   k8s.delete_pod(ns, name)
//   k8s.set_image(ns, kind, name, image, container="")  — Undo: Vorher-Image
//   k8s.rollout_undo(ns, name)                          — vorherige Revision
//   k8s.pause(ns, name) / k8s.resume(ns, name)          — pause: Undo=resume
//   k8s.cordon(node) / k8s.uncordon(node)               — cordon: Undo=uncordon
//   k8s.hpa_set(ns, name, min, max)                     — Undo: alte Grenzen
//   k8s.cronjob_trigger(ns, name) → job-name            — einmaliger Job-Lauf
//   k8s.cronjob_suspend(ns, name) / k8s.cronjob_resume(ns, name)
//   k8s.taint(node, key, value="", effect="NoSchedule") — Undo: untaint
//   k8s.untaint(node, key)
//   k8s.events(ns, name) → [{type,reason,message,count,object}]
//   k8s.raw_get/raw_list/raw_apply/raw_patch/raw_delete — generisch (script_raw.go):
//     kubectl-Äquivalenz mit Auto-Snapshot + Undo, Denylist + Mutations-Budget.
//   wait_rollout(ns, kind, name, timeout=120)    — bis neue Generation komplett
//   wait_ready(ns, kind, name, timeout=120)      — bis Pods == desired, alle ready
//   → geben True/False zurück (False = Timeout; das Script entscheidet, z.B. Rollback)

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	scriptTimeout  = 10 * time.Minute // Scripts orchestrieren mehrere Rollouts
	maxScriptSteps = 40               // Timeline-Obergrenze (UI + Sanity)
	maxSleep       = 30 * time.Second
	// Starlark-Interpreter-Budget: verhindert heiße Schleifen (for über große
	// ranges) — Cluster-Waits laufen in Go-Builtins und kosten kaum Budget.
	maxExecutionSteps = 5_000_000
)

type scriptParams struct {
	Source         string            `json:"source"`
	Args           map[string]string `json:"args"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
}

// undoEntry ist eine automatisch registrierte Kompensation: jeder mutierende
// Builtin legt beim Ausführen sein Gegenteil auf den Stack — bei Cancel oder
// Timeout rollt die Engine LIFO zurück.
type undoEntry struct {
	desc string
	fn   func(ctx context.Context) error
}

// executeScript fährt eine script-Action: Steps entstehen DYNAMISCH aus dem
// Workflow (step()-Builtin), Fortschritt fließt live in dieselbe Timeline.
func (r *Runner) executeScript(ctx context.Context, a Action) {
	var p scriptParams
	if err := json.Unmarshal(a.Params, &p); err != nil || strings.TrimSpace(p.Source) == "" {
		r.report(ctx, a.ID, "failed", "invalid script params (missing source)", "", nil)
		return
	}
	// Timeoutable: Budget aus der Definition (clamp 30s..30min).
	timeout := scriptTimeout
	if p.TimeoutSeconds > 0 {
		timeout = time.Duration(p.TimeoutSeconds) * time.Second
		if timeout < 30*time.Second {
			timeout = 30 * time.Second
		}
		if timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Cancel kommt auf zwei Wegen: SOFORT als SSE-Signal (Cancel-Registry,
	// andere Goroutine) oder als Antwort auf Progress-Reports (Fallback).
	// threadMu schützt den activeThread-Pointer; Thread.Cancel selbst ist
	// goroutine-sicher.
	var cancelled atomic.Bool
	var threadMu sync.Mutex
	var activeThread *starlark.Thread
	requestCancel := func() {
		if cancelled.CompareAndSwap(false, true) {
			threadMu.Lock()
			if activeThread != nil {
				activeThread.Cancel("cancelled by user")
			}
			threadMu.Unlock()
			cancel()
		}
	}
	r.registerCancel(a.ID, requestCancel)
	defer r.unregisterCancel(a.ID)

	undoStack := []undoEntry{}

	states := []stepState{}
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
	cur := -1 // Index des laufenden Steps

	failNow := func(msg string) {
		if cur >= 0 {
			states[cur].Status = "failed"
			if states[cur].Detail == "" {
				states[cur].Detail = msg
			}
		}
		log.Printf("actions: script %s → failed (%s)", a.TargetName, msg)
		r.report(ctx, a.ID, "failed", msg, "", states)
		if r.onExecuted != nil {
			r.onExecuted()
		}
	}

	/* ── Builtins ── */

	stepFn := starlark.NewBuiltin("step", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs("step", sargs, kwargs, "name", &name); err != nil {
			return nil, err
		}
		if len(states) >= maxScriptSteps {
			return nil, fmt.Errorf("too many steps (max %d)", maxScriptSteps)
		}
		if cur >= 0 && states[cur].Status == "running" {
			states[cur].Status = "ok"
		}
		states = append(states, stepState{Name: name, Status: "running"})
		cur = len(states) - 1
		push(name+"…", true)
		return starlark.None, nil
	})

	reportFn := starlark.NewBuiltin("report", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var detail string
		if err := starlark.UnpackArgs("report", sargs, kwargs, "detail", &detail); err != nil {
			return nil, err
		}
		if cur >= 0 {
			states[cur].Detail = detail
			push(fmt.Sprintf("%s: %s", states[cur].Name, detail), false)
		} else {
			push(detail, false)
		}
		return starlark.None, nil
	})

	failFn := starlark.NewBuiltin("fail", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var msg string
		if err := starlark.UnpackArgs("fail", sargs, kwargs, "msg", &msg); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s", msg)
	})

	sleepFn := starlark.NewBuiltin("sleep", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var secsVal starlark.Value
		if err := starlark.UnpackArgs("sleep", sargs, kwargs, "seconds", &secsVal); err != nil {
			return nil, err
		}
		secs, ok := starlark.AsFloat(secsVal) // akzeptiert int UND float
		if !ok || secs < 0 {
			return nil, fmt.Errorf("sleep: seconds must be a non-negative number")
		}
		d := time.Duration(secs * float64(time.Second))
		if d > maxSleep {
			d = maxSleep
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout during sleep")
		case <-time.After(d):
		}
		return starlark.None, nil
	})

	k8sGet := starlark.NewBuiltin("get", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		if err := starlark.UnpackArgs("get", sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &name); err != nil {
			return nil, err
		}
		st, err := r.readStateOf(ctx, ns, kind, name)
		if err != nil {
			return nil, err
		}
		d := starlark.NewDict(4)
		_ = d.SetKey(starlark.String("desired"), starlark.MakeInt(int(st.desired)))
		_ = d.SetKey(starlark.String("ready"), starlark.MakeInt(int(st.ready)))
		_ = d.SetKey(starlark.String("updated"), starlark.MakeInt(int(st.updated)))
		_ = d.SetKey(starlark.String("available"), starlark.MakeInt(int(st.available)))
		return d, nil
	})

	k8sPods := starlark.NewBuiltin("pods", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, selector string
		if err := starlark.UnpackArgs("pods", sargs, kwargs, "namespace", &ns, "selector?", &selector); err != nil {
			return nil, err
		}
		list, err := r.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		out := []starlark.Value{}
		for i := range list.Items {
			p := &list.Items[i]
			d := starlark.NewDict(5)
			_ = d.SetKey(starlark.String("name"), starlark.String(p.Name))
			_ = d.SetKey(starlark.String("ready"), starlark.Bool(isPodReady(p)))
			_ = d.SetKey(starlark.String("phase"), starlark.String(string(p.Status.Phase)))
			restarts := 0
			for _, cs := range p.Status.ContainerStatuses {
				restarts += int(cs.RestartCount)
			}
			_ = d.SetKey(starlark.String("restarts"), starlark.MakeInt(restarts))
			_ = d.SetKey(starlark.String("node"), starlark.String(p.Spec.NodeName))
			out = append(out, d)
		}
		return starlark.NewList(out), nil
	})

	k8sRestart := starlark.NewBuiltin("rollout_restart", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		if err := starlark.UnpackArgs("rollout_restart", sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &name); err != nil {
			return nil, err
		}
		undoStack = append(undoStack, undoEntry{
			desc: fmt.Sprintf("rollout restart %s/%s cannot be undone", ns, name),
			fn:   nil, // Vermerk — Restarts sind idempotent-harmlos
		})
		_, err := r.triggerRestart(ctx, Action{TargetNamespace: ns, TargetKind: kind, TargetName: name})
		return starlark.None, err
	})

	k8sScale := starlark.NewBuiltin("scale", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		var replicas int
		if err := starlark.UnpackArgs("scale", sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &name, "replicas", &replicas); err != nil {
			return nil, err
		}
		if replicas < 0 || replicas > 50 {
			return nil, fmt.Errorf("replicas must be 0..50")
		}
		// Automatische Kompensation: Vorher-Wert auf den Undo-Stack.
		if st, err := r.readStateOf(ctx, ns, kind, name); err == nil {
			prev := st.desired
			nsC, kindC, nameC := ns, kind, name
			undoStack = append(undoStack, undoEntry{
				desc: fmt.Sprintf("scale %s/%s back to %d", nsC, nameC, prev),
				fn: func(uctx context.Context) error {
					up, _ := json.Marshal(map[string]int32{"replicas": prev})
					if _, err := r.triggerScale(uctx, Action{TargetNamespace: nsC, TargetKind: kindC, TargetName: nameC, Params: up}); err != nil {
						return err
					}
					_, err := r.observeScale(uctx, Action{TargetNamespace: nsC, TargetKind: kindC, TargetName: nameC}, func(string) {})
					return err
				},
			})
		}
		params, _ := json.Marshal(map[string]int{"replicas": replicas})
		_, err := r.triggerScale(ctx, Action{TargetNamespace: ns, TargetKind: kind, TargetName: name, Params: params})
		return starlark.None, err
	})

	k8sDeletePod := starlark.NewBuiltin("delete_pod", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("delete_pod", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		return starlark.None, r.clientset.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
	})

	// wait_*: beobachten mit eigenem Timeout; False bei Timeout (Script
	// entscheidet über Rollback), Fehler nur bei API-Problemen.
	mkWait := func(name string, done func(st workloadState, sum podsSummary) bool) *starlark.Builtin {
		return starlark.NewBuiltin(name, func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var ns, kind, wname string
			timeout := 120
			if err := starlark.UnpackArgs(name, sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &wname, "timeout?", &timeout); err != nil {
				return nil, err
			}
			wctx, wcancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer wcancel()
			t := time.NewTicker(observeEvery)
			defer t.Stop()
			for {
				st, err1 := r.readStateOf(wctx, ns, kind, wname)
				pods, err2 := r.workloadPods(wctx, Action{TargetNamespace: ns, TargetKind: kind, TargetName: wname})
				if err1 == nil && err2 == nil {
					sum := summarize(pods)
					if cur >= 0 {
						states[cur].Detail = sum.String()
						push(fmt.Sprintf("%s: %s", states[cur].Name, sum.String()), false)
					}
					if done(st, sum) {
						return starlark.Bool(true), nil
					}
				}
				select {
				case <-wctx.Done():
					return starlark.Bool(false), nil // Timeout — Script entscheidet
				case <-t.C:
				}
			}
		})
	}
	waitRollout := mkWait("wait_rollout", func(st workloadState, sum podsSummary) bool {
		return st.rolledOut() && sum.terminating == 0 && int32(sum.ready) == st.desired
	})
	waitReady := mkWait("wait_ready", func(st workloadState, sum podsSummary) bool {
		return st.generationCaughtUp && sum.terminating == 0 &&
			int32(sum.total) == st.desired && int32(sum.ready) == st.desired
	})

	// k8s.set_image(namespace, kind, name, image, container="") — Container-Image
	// setzen; Undo stellt das Vorher-Image wieder her.
	k8sSetImage := starlark.NewBuiltin("set_image", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name, image, container string
		if err := starlark.UnpackArgs("set_image", sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &name, "image", &image, "container?", &container); err != nil {
			return nil, err
		}
		a := Action{TargetNamespace: ns, TargetKind: kind, TargetName: name}
		prev, err := r.currentImages(ctx, a)
		if err != nil {
			return nil, err
		}
		target := container
		if target == "" {
			if len(prev) != 1 {
				return nil, fmt.Errorf("container required (%d containers)", len(prev))
			}
			for n := range prev {
				target = n
			}
		} else if _, ok := prev[target]; !ok {
			return nil, fmt.Errorf("no container %q in %s/%s", target, kind, name)
		}
		prevImg := prev[target]
		nsC, kindC, nameC, tgt := ns, kind, name, target
		undoStack = append(undoStack, undoEntry{
			desc: fmt.Sprintf("restore %s image to %s", tgt, prevImg),
			fn: func(uctx context.Context) error {
				return r.applyImages(uctx, Action{TargetNamespace: nsC, TargetKind: kindC, TargetName: nameC}, map[string]string{tgt: prevImg})
			},
		})
		return starlark.None, r.applyImages(ctx, a, map[string]string{target: image})
	})

	// k8s.cordon(node) / k8s.uncordon(node) — Schedulability; cordon registriert
	// uncordon als Undo.
	k8sCordon := starlark.NewBuiltin("cordon", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var node string
		if err := starlark.UnpackArgs("cordon", sargs, kwargs, "node", &node); err != nil {
			return nil, err
		}
		nodeC := node
		undoStack = append(undoStack, undoEntry{desc: "uncordon " + nodeC, fn: func(uctx context.Context) error {
			_, err := r.setUnschedulable(uctx, nodeC, false)
			return err
		}})
		_, err := r.setUnschedulable(ctx, node, true)
		return starlark.None, err
	})
	k8sUncordon := starlark.NewBuiltin("uncordon", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var node string
		if err := starlark.UnpackArgs("uncordon", sargs, kwargs, "node", &node); err != nil {
			return nil, err
		}
		_, err := r.setUnschedulable(ctx, node, false)
		return starlark.None, err
	})

	// k8s.hpa_set(namespace, name, min, max) — HPA-Grenzen; Undo stellt sie her.
	k8sHpaSet := starlark.NewBuiltin("hpa_set", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		var min, max int
		if err := starlark.UnpackArgs("hpa_set", sargs, kwargs, "namespace", &ns, "name", &name, "min", &min, "max", &max); err != nil {
			return nil, err
		}
		if min < 1 || max < min || max > 200 {
			return nil, fmt.Errorf("invalid bounds: min %d, max %d", min, max)
		}
		a := Action{TargetNamespace: ns, TargetName: name}
		if pmin, pmax, err := r.currentHPABounds(ctx, a); err == nil {
			nsC, nameC := ns, name
			undoStack = append(undoStack, undoEntry{
				desc: fmt.Sprintf("restore HPA bounds %d–%d", pmin, pmax),
				fn:   func(uctx context.Context) error { return r.patchHPABounds(uctx, nsC, nameC, pmin, pmax) },
			})
		}
		return starlark.None, r.patchHPABounds(ctx, ns, name, int32(min), int32(max))
	})

	// k8s.pause / k8s.resume — Deployment-Rollout einfrieren/freigeben.
	k8sPause := starlark.NewBuiltin("pause", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("pause", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		nsC, nameC := ns, name
		undoStack = append(undoStack, undoEntry{desc: "resume " + nameC, fn: func(uctx context.Context) error {
			return r.setPaused(uctx, Action{TargetNamespace: nsC, TargetName: nameC}, false)
		}})
		return starlark.None, r.setPaused(ctx, Action{TargetNamespace: ns, TargetName: name}, true)
	})
	k8sResume := starlark.NewBuiltin("resume", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("resume", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		return starlark.None, r.setPaused(ctx, Action{TargetNamespace: ns, TargetName: name}, false)
	})

	// k8s.rollout_undo(namespace, name) — auf die vorherige Revision zurück.
	k8sRolloutUndo := starlark.NewBuiltin("rollout_undo", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("rollout_undo", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		_, err := r.triggerRolloutUndo(ctx, ns, name)
		return starlark.None, err
	})

	// k8s.cronjob_trigger(namespace, name) → Job-Name (string). suspend/resume.
	k8sCronTrigger := starlark.NewBuiltin("cronjob_trigger", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("cronjob_trigger", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		jobName, err := r.createJobFromCronJob(ctx, ns, name)
		if err != nil {
			return nil, err
		}
		return starlark.String(jobName), nil
	})
	k8sCronSuspend := starlark.NewBuiltin("cronjob_suspend", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("cronjob_suspend", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		nsC, nameC := ns, name
		undoStack = append(undoStack, undoEntry{desc: "resume cronjob " + nameC, fn: func(uctx context.Context) error {
			return r.setCronjobSuspend(uctx, Action{TargetNamespace: nsC, TargetName: nameC}, false)
		}})
		return starlark.None, r.setCronjobSuspend(ctx, Action{TargetNamespace: ns, TargetName: name}, true)
	})
	k8sCronResume := starlark.NewBuiltin("cronjob_resume", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("cronjob_resume", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		return starlark.None, r.setCronjobSuspend(ctx, Action{TargetNamespace: ns, TargetName: name}, false)
	})

	// k8s.taint / k8s.untaint — Node-Taints (taint registriert untaint als Undo).
	k8sTaint := starlark.NewBuiltin("taint", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var node, key, value, effect string
		if err := starlark.UnpackArgs("taint", sargs, kwargs, "node", &node, "key", &key, "value?", &value, "effect?", &effect); err != nil {
			return nil, err
		}
		if effect == "" {
			effect = "NoSchedule"
		}
		params, _ := json.Marshal(map[string]string{"key": key, "value": value, "effect": effect})
		nodeC, keyC := node, key
		undoStack = append(undoStack, undoEntry{desc: "untaint " + nodeC, fn: func(uctx context.Context) error {
			up, _ := json.Marshal(map[string]string{"key": keyC})
			return r.applyTaint(uctx, Action{TargetName: nodeC, Params: up}, true)
		}})
		return starlark.None, r.applyTaint(ctx, Action{TargetName: node, Params: params}, false)
	})
	k8sUntaint := starlark.NewBuiltin("untaint", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var node, key string
		if err := starlark.UnpackArgs("untaint", sargs, kwargs, "node", &node, "key", &key); err != nil {
			return nil, err
		}
		params, _ := json.Marshal(map[string]string{"key": key})
		return starlark.None, r.applyTaint(ctx, Action{TargetName: node, Params: params}, true)
	})

	// k8s.events(namespace, name) → [{type,reason,message,count,object}] — die
	// Read-Grundlage für debug_bundle/pod_events als Starlark.
	k8sEvents := starlark.NewBuiltin("events", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("events", sargs, kwargs, "namespace", &ns, "name", &name); err != nil {
			return nil, err
		}
		list, err := r.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := []starlark.Value{}
		for i := range list.Items {
			e := &list.Items[i]
			if name != "" && e.InvolvedObject.Name != name {
				continue
			}
			d := starlark.NewDict(5)
			_ = d.SetKey(starlark.String("type"), starlark.String(e.Type))
			_ = d.SetKey(starlark.String("reason"), starlark.String(e.Reason))
			_ = d.SetKey(starlark.String("message"), starlark.String(e.Message))
			_ = d.SetKey(starlark.String("count"), starlark.MakeInt(int(e.Count)))
			_ = d.SetKey(starlark.String("object"), starlark.String(e.InvolvedObject.Kind+"/"+e.InvolvedObject.Name))
			out = append(out, d)
		}
		return starlark.NewList(out), nil
	})

	// Generische raw_*-Builtins (kubectl-Äquivalenz, Auto-Snapshot + Undo-LIFO).
	rawMembers := r.rawBuiltins(ctx,
		func(e undoEntry) { undoStack = append(undoStack, e) },
		func(line string) {
			if cur >= 0 {
				states[cur].Detail = line
			}
			push(line, false)
		})

	// Echtes Modul (kein Dict!): k8s.get muss MEIN Builtin treffen, nicht die
	// eingebaute dict-Methode get(key, default).
	k8sModule := &starlarkstruct.Module{
		Name: "k8s",
		Members: starlark.StringDict{
			"get":             k8sGet,
			"pods":            k8sPods,
			"rollout_restart": k8sRestart,
			"rollout_undo":    k8sRolloutUndo,
			"scale":           k8sScale,
			"delete_pod":      k8sDeletePod,
			"set_image":       k8sSetImage,
			"pause":           k8sPause,
			"resume":          k8sResume,
			"cordon":          k8sCordon,
			"uncordon":        k8sUncordon,
			"hpa_set":         k8sHpaSet,
			"cronjob_trigger": k8sCronTrigger,
			"cronjob_suspend": k8sCronSuspend,
			"cronjob_resume":  k8sCronResume,
			"taint":           k8sTaint,
			"untaint":         k8sUntaint,
			"events":          k8sEvents,
		},
	}
	for name, fn := range rawMembers {
		k8sModule.Members[name] = fn
	}

	argsDict := starlark.NewDict(len(p.Args))
	for k, v := range p.Args {
		_ = argsDict.SetKey(starlark.String(k), starlark.String(v))
	}

	globals := starlark.StringDict{
		"args":         argsDict,
		"step":         stepFn,
		"report":       reportFn,
		"fail":         failFn,
		"sleep":        sleepFn,
		"k8s":          k8sModule,
		"wait_rollout": waitRollout,
		"wait_ready":   waitReady,
	}

	thread := &starlark.Thread{Name: "action:" + a.TargetName}
	thread.SetMaxExecutionSteps(maxExecutionSteps)
	threadMu.Lock()
	activeThread = thread
	threadMu.Unlock()
	// Cancel kam evtl. schon, BEVOR der Thread stand — dann sofort abbrechen,
	// statt das Script erst loslaufen zu lassen.
	if cancelled.Load() {
		thread.Cancel("cancelled by user")
	}
	push("starting workflow…", true)
	_, err := starlark.ExecFileOptions(scriptFileOptions(), thread, a.TargetName+".star", p.Source, globals)
	if err != nil {
		// Cancel/Timeout → AUTOMATISCHER ROLLBACK des Undo-Stacks (LIFO).
		if cancelled.Load() || ctx.Err() != nil {
			reason := "cancelled by user"
			if !cancelled.Load() {
				reason = "timeout after " + timeout.String()
			}
			if cur >= 0 && states[cur].Status == "running" {
				states[cur].Status = "failed"
				states[cur].Detail = reason
			}
			r.rollbackScript(ctx, a, reason, undoStack, &states)
			return
		}
		msg := err.Error()
		if evalErr, ok := err.(*starlark.EvalError); ok {
			msg = evalErr.Backtrace()
			if len(msg) > 1200 {
				msg = msg[:1200]
			}
		}
		failNow(msg)
		return
	}

	if cur >= 0 && states[cur].Status == "running" {
		states[cur].Status = "ok"
	}
	final := "workflow completed"
	if cur >= 0 && states[cur].Detail != "" {
		final = states[cur].Detail
	}
	log.Printf("actions: script %s → succeeded (%s)", a.TargetName, final)
	r.report(ctx, a.ID, "succeeded", final, "", states)
	if r.onExecuted != nil {
		r.onExecuted()
	}
}

// scriptFileOptions: hermetische Sprach-Optionen — Sets ja, while/Rekursion
// NEIN (Terminierung), Top-Level-Kontrollfluss für ergonomische Workflows.
func scriptFileOptions() *syntax.FileOptions {
	return &syntax.FileOptions{
		Set:             true,
		GlobalReassign:  true,
		TopLevelControl: true,
		While:           false,
		Recursion:       false,
	}
}

// rollbackScript fährt den Undo-Stack LIFO ab (frischer Kontext — der Action-
// Kontext ist bereits abgebrochen) und meldet den Endstatus 'cancelled'.
func (r *Runner) rollbackScript(ctx context.Context, a Action, reason string, undoStack []undoEntry, states *[]stepState) {
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer rcancel()
	done := 0
	for i := len(undoStack) - 1; i >= 0; i-- {
		u := undoStack[i]
		st := stepState{Name: "rollback", Status: "running", Detail: u.desc}
		*states = append(*states, st)
		idx := len(*states) - 1
		if u.fn == nil {
			(*states)[idx].Status = "ok" // Vermerk: nichts zu kompensieren
			continue
		}
		r.report(rctx, a.ID, "running", "", "rollback: "+u.desc, *states)
		if err := u.fn(rctx); err != nil {
			(*states)[idx].Status = "failed"
			(*states)[idx].Detail = u.desc + " — " + err.Error()
			r.report(rctx, a.ID, "cancelled", reason+" — rollback FAILED: "+err.Error(), "", *states)
			if r.onExecuted != nil {
				r.onExecuted()
			}
			return
		}
		(*states)[idx].Status = "ok"
		done++
	}
	result := reason
	if done > 0 {
		result = fmt.Sprintf("%s — rolled back %d change(s)", reason, done)
	}
	log.Printf("actions: script %s → cancelled (%s)", a.TargetName, result)
	r.report(rctx, a.ID, "cancelled", result, "", *states)
	if r.onExecuted != nil {
		r.onExecuted()
	}
}
