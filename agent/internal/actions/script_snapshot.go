package actions

// script_snapshot.go — the snapshot-aware Starlark surface (Phase 1 proof).
// Mutators auto-capture their target BEFORE writing (strict mode: reversibility
// is a construction guarantee, not a runtime hope), appending to a run-bound
// CaptureLog. runCapturedScript runs a script and, on any failure, replays the
// log via the ONE generic Restore. This is the whole snapshot substrate, end to
// end, over the real Starlark host — nothing here touches plan()/executeManifest.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	starlarkjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// applyMergePatch is the low-level mutate primitive the snapshot mutators share.
func (r *Runner) applyMergePatch(ctx context.Context, kind, namespace, name string, patch map[string]any) error {
	return r.applyPatch(ctx, kind, namespace, name, patch, types.MergePatchType)
}

// applyPatch applies a patch of the given type. Strategic-merge (for container
// arrays like set_image/set_env) merges arrays by their patch-merge-key so it
// updates one container without dropping the others; the whole-object snapshot
// captured before still restores correctly (a JSON-merge restore replaces the
// array back to the captured one).
func (r *Runner) applyPatch(ctx context.Context, kind, namespace, name string, patch map[string]any, pt types.PatchType) error {
	iface, err := r.dynIface(kind, namespace)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(patch)
	_, err = iface.Patch(ctx, name, pt, data, metav1.PatchOptions{FieldManager: "rocketplane-agent"})
	return err
}

// snapshotGlobals builds the Starlark surface backed by the capture log. The
// mutators strict-capture before writing.
func (r *Runner) snapshotGlobals(ctx context.Context, log *CaptureLog, report func(string)) starlark.StringDict {
	rep := func(s string) {
		if report != nil {
			report(s)
		}
	}

	// snapshot(namespace, kind, name) — explicit whole-object capture, bound to
	// the run. Returns the redacted before-state so the script can read it.
	snapshot := starlark.NewBuiltin("snapshot", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		if err := starlark.UnpackArgs("snapshot", args, kwargs, "namespace", &ns, "kind", &kind, "name", &name); err != nil {
			return nil, err
		}
		c, err := r.captureObject(ctx, log, kind, ns, name)
		if err != nil {
			return nil, err
		}
		rep("snapshot " + kind + "/" + name)
		if c == nil || !c.Existed {
			return starlark.None, nil
		}
		obj := c.Object
		if kind == "Secret" {
			cp := map[string]any{}
			for k, v := range obj {
				cp[k] = v
			}
			redactSecretData(cp) // plaintext never crosses into script logic
			obj = cp
		}
		return goToStar(obj), nil
	})

	// k8s.scale — strict-capture the whole object, then patch spec.replicas.
	k8sScale := starlark.NewBuiltin("scale", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		var replicas int
		if err := starlark.UnpackArgs("scale", args, kwargs, "namespace", &ns, "kind", &kind, "name", &name, "replicas", &replicas); err != nil {
			return nil, err
		}
		if _, err := r.captureObject(ctx, log, kind, ns, name); err != nil {
			return nil, fmt.Errorf("scale: cannot snapshot %s/%s (strict mode): %w", kind, name, err)
		}
		if err := r.applyMergePatch(ctx, kind, ns, name, map[string]any{"spec": map[string]any{"replicas": replicas}}); err != nil {
			return nil, err
		}
		rep(fmt.Sprintf("scaled %s/%s to %d", kind, name, replicas))
		return starlark.None, nil
	})

	// k8s.patch_configmap — strict-capture the exact key (field-scoped), then set it.
	k8sPatchCM := starlark.NewBuiltin("patch_configmap", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, name, key, value string
		if err := starlark.UnpackArgs("patch_configmap", args, kwargs, "namespace", &ns, "name", &name, "key", &key, "value", &value); err != nil {
			return nil, err
		}
		if _, err := r.captureFields(ctx, log, "ConfigMap", ns, name, [][]string{{"data", key}}); err != nil {
			return nil, fmt.Errorf("patch_configmap: cannot snapshot data[%s] (strict mode): %w", key, err)
		}
		if err := r.applyMergePatch(ctx, "ConfigMap", ns, name, map[string]any{"data": map[string]any{key: value}}); err != nil {
			return nil, err
		}
		rep(fmt.Sprintf("patched configmap %s data[%s]", name, key))
		return starlark.None, nil
	})

	failFn := starlark.NewBuiltin("fail", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var msg string
		_ = starlark.UnpackArgs("fail", args, kwargs, "msg", &msg)
		return nil, fmt.Errorf("%s", msg)
	})
	stepFn := starlark.NewBuiltin("step", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		_ = starlark.UnpackArgs("step", args, kwargs, "name", &name)
		rep("step: " + name)
		return starlark.None, nil
	})

	// k8s.patch — the GENERIC mutate primitive. Strict-capture the whole object,
	// then apply a merge patch the SCRIPT provides. One primitive expresses most
	// mutating kinds (scale/cordon/hpa/set_image/annotate/…): the engine only
	// executes + snapshots; the per-kind "what to change" lives in the script.
	k8sPatch := starlark.NewBuiltin("patch", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		var patch *starlark.Dict
		strategic := false
		if err := starlark.UnpackArgs("patch", args, kwargs, "namespace", &ns, "kind", &kind, "name", &name, "patch", &patch, "strategic?", &strategic); err != nil {
			return nil, err
		}
		if _, err := r.captureObject(ctx, log, kind, ns, name); err != nil {
			return nil, fmt.Errorf("patch: cannot snapshot %s/%s (strict mode): %w", kind, name, err)
		}
		obj, err := starToGo(patch)
		if err != nil {
			return nil, err
		}
		m, _ := obj.(map[string]any)
		pt := types.MergePatchType
		if strategic {
			pt = types.StrategicMergePatchType
		}
		if err := r.applyPatch(ctx, kind, ns, name, m, pt); err != nil {
			return nil, err
		}
		rep(fmt.Sprintf("patched %s/%s", kind, name))
		return starlark.None, nil
	})

	// k8s.set_field — the GENERIC field-scoped mutate primitive. Captures the
	// exact path (surgical), then sets it. Restore is field-scoped: it removes a
	// key this run ADDED (merge-null) or restores a changed key — never touching
	// sibling keys. Use for key-level kinds (annotate/set_label/set_env/
	// patch_configmap) where whole-object restore would clobber or leak.
	k8sSetField := starlark.NewBuiltin("set_field", func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		var path *starlark.List
		var value starlark.Value
		if err := starlark.UnpackArgs("set_field", args, kwargs, "namespace", &ns, "kind", &kind, "name", &name, "path", &path, "value", &value); err != nil {
			return nil, err
		}
		segs := make([]string, 0, path.Len())
		for i := 0; i < path.Len(); i++ {
			s, ok := path.Index(i).(starlark.String)
			if !ok {
				return nil, fmt.Errorf("set_field: path segments must be strings")
			}
			segs = append(segs, string(s))
		}
		if len(segs) == 0 {
			return nil, fmt.Errorf("set_field: empty path")
		}
		if _, err := r.captureFields(ctx, log, kind, ns, name, [][]string{segs}); err != nil {
			return nil, fmt.Errorf("set_field: cannot snapshot %v (strict mode): %w", segs, err)
		}
		goVal, err := starToGo(value)
		if err != nil {
			return nil, err
		}
		patch := map[string]any{}
		setNested(patch, segs, goVal)
		if err := r.applyMergePatch(ctx, kind, ns, name, patch); err != nil {
			return nil, err
		}
		rep(fmt.Sprintf("set %s/%s %v", kind, name, segs))
		return starlark.None, nil
	})

	// k8s.delete — snapshot the WHOLE object, then delete it. The generic Restore
	// recreates it from the capture (Get→NotFound→Create), so a delete is
	// reversible like any other mutation. Used by delete_pod/delete_job/
	// delete_configmap/cleanup_* (owners recreate pods; the snapshot recreates the
	// rest). NOTE: an owner-managed pod is recreated by its controller too — undo
	// is idempotent (Create is a no-op if it already exists again).
	k8sDelete := starlark.NewBuiltin("delete", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var ns, kind, name string
		if err := starlark.UnpackArgs("delete", a, kw, "namespace", &ns, "kind", &kind, "name", &name); err != nil {
			return nil, err
		}
		c, err := r.captureObject(ctx, log, kind, ns, name)
		if err != nil {
			return nil, fmt.Errorf("delete: cannot snapshot %s/%s (strict mode): %w", kind, name, err)
		}
		if c != nil && !c.Existed {
			rep(fmt.Sprintf("%s/%s already gone", kind, name))
			return starlark.None, nil
		}
		iface, err := r.dynIface(kind, ns)
		if err != nil {
			return nil, err
		}
		if err := iface.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
		rep(fmt.Sprintf("deleted %s/%s", kind, name))
		return starlark.None, nil
	})

	// k8s.create — snapshot the (absent) target, then create it. The generic
	// Restore deletes what this run created. Used by create_configmap/run_debug_pod.
	k8sCreate := starlark.NewBuiltin("create", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var manifest *starlark.Dict
		if err := starlark.UnpackArgs("create", a, kw, "manifest", &manifest); err != nil {
			return nil, err
		}
		obj, err := starToGo(manifest)
		if err != nil {
			return nil, err
		}
		m, _ := obj.(map[string]any)
		u := &unstructured.Unstructured{Object: m}
		kind, name, ns := u.GetKind(), u.GetName(), u.GetNamespace()
		if kind == "" || name == "" {
			return nil, fmt.Errorf("create: manifest needs kind + metadata.name")
		}
		if _, err := r.captureObject(ctx, log, kind, ns, name); err != nil {
			return nil, fmt.Errorf("create: cannot snapshot %s/%s (strict mode): %w", kind, name, err)
		}
		iface, err := r.dynIface(kind, ns)
		if err != nil {
			return nil, err
		}
		u.SetResourceVersion("")
		if _, err := iface.Create(ctx, u, metav1.CreateOptions{FieldManager: "rocketplane-agent"}); err != nil {
			return nil, err
		}
		rep(fmt.Sprintf("created %s/%s", kind, name))
		return starlark.None, nil
	})

	// readIface resolves a dynamic client for READS across any kind — whitelisted
	// or via api_version(+resource). Reads are safe, so (unlike mutators) they are
	// not whitelist-bound; this backs get/list of CRDs, ReplicaSets, Helm secrets.
	readIface := func(apiVersion, kind, ns, resource string) (dynamicResource, error) {
		if r.dyn == nil {
			return nil, fmt.Errorf("generic access unavailable (no dynamic client)")
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		if ns == "" || ns == "-" {
			return r.dyn.Resource(gvr), nil
		}
		return r.dyn.Resource(gvr).Namespace(ns), nil
	}

	// k8s.raw_get — read ANY object (CRDs too). Read-only, no capture. Secret
	// values are redacted before crossing into script logic.
	k8sRawGet := starlark.NewBuiltin("raw_get", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, name, resource string
		if err := starlark.UnpackArgs("raw_get", a, kw, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "name", &name, "resource?", &resource); err != nil {
			return nil, err
		}
		iface, err := readIface(apiVersion, kind, ns, resource)
		if err != nil {
			return nil, err
		}
		u, err := iface.Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return starlark.None, nil
		}
		if err != nil {
			return nil, err
		}
		obj := stripForSnapshot(u)
		if kind == "Secret" {
			redactSecretData(obj)
		}
		return goToStar(obj), nil
	})

	// k8s.raw_list — list ANY kind by namespace (+optional label selector). Read-only.
	k8sRawList := starlark.NewBuiltin("raw_list", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, selector, resource string
		if err := starlark.UnpackArgs("raw_list", a, kw, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "selector?", &selector, "resource?", &resource); err != nil {
			return nil, err
		}
		iface, err := readIface(apiVersion, kind, ns, resource)
		if err != nil {
			return nil, err
		}
		list, err := iface.List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(list.Items))
		for i := range list.Items {
			obj := stripForSnapshot(&list.Items[i])
			if kind == "Secret" {
				redactSecretData(obj)
			}
			out = append(out, goToStar(obj))
		}
		return starlark.NewList(out), nil
	})

	// k8s.pods — pod-level status list (name/ready/phase/restarts/node). Read-only.
	k8sPods := starlark.NewBuiltin("pods", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var ns, selector string
		if err := starlark.UnpackArgs("pods", a, kw, "namespace", &ns, "selector?", &selector); err != nil {
			return nil, err
		}
		list, err := r.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(list.Items))
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

	// k8s.events — recent events (optionally filtered to one involved object). Read-only.
	k8sEvents := starlark.NewBuiltin("events", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var ns, name string
		if err := starlark.UnpackArgs("events", a, kw, "namespace", &ns, "name?", &name); err != nil {
			return nil, err
		}
		list, err := r.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(list.Items))
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

	k8s := &starlarkstruct.Module{Name: "k8s", Members: starlark.StringDict{
		"patch":           k8sPatch,
		"set_field":       k8sSetField,
		"scale":           k8sScale,
		"patch_configmap": k8sPatchCM,
		"delete":          k8sDelete,
		"create":          k8sCreate,
		"raw_get":         k8sRawGet,
		"raw_list":        k8sRawList,
		"pods":            k8sPods,
		"events":          k8sEvents,
		"get":             starlark.NewBuiltin("get", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
			var ns, kind, name string
			if err := starlark.UnpackArgs("get", a, kw, "namespace", &ns, "kind", &kind, "name", &name); err != nil {
				return nil, err
			}
			u, err := r.getUnstructured(ctx, kind, ns, name)
			if err != nil {
				return starlark.None, nil
			}
			obj := stripForSnapshot(u)
			if kind == "Secret" {
				redactSecretData(obj)
			}
			return goToStar(obj), nil
		}),
	}}

	// Observers (read-only) — owned pod-level convergence the script calls; they
	// return False on timeout so the script decides (fail() → auto-rollback).
	mkWait := func(name string, done func(st workloadState, sum podsSummary) bool) *starlark.Builtin {
		return starlark.NewBuiltin(name, func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var ns, kind, wname string
			timeout := 120
			if err := starlark.UnpackArgs(name, sargs, kwargs, "namespace", &ns, "kind", &kind, "name", &wname, "timeout?", &timeout); err != nil {
				return nil, err
			}
			wctx, wcancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer wcancel()
			tk := time.NewTicker(observeEvery)
			defer tk.Stop()
			for {
				st, e1 := r.readStateOf(wctx, ns, kind, wname)
				pods, e2 := r.workloadPods(wctx, Action{TargetNamespace: ns, TargetKind: kind, TargetName: wname})
				if e1 == nil && e2 == nil {
					sum := summarize(pods)
					rep(name + ": " + sum.String())
					if done(st, sum) {
						return starlark.Bool(true), nil
					}
				}
				select {
				case <-wctx.Done():
					return starlark.Bool(false), nil
				case <-tk.C:
				}
			}
		})
	}
	waitRollout := mkWait("wait_rollout", func(st workloadState, _ podsSummary) bool { return st.rolledOut() })
	waitReady := mkWait("wait_ready", func(st workloadState, sum podsSummary) bool {
		return st.generationCaughtUp && st.ready == st.desired && sum.terminating == 0 && int32(sum.total) == st.desired
	})

	// report(msg) — a human progress line (same channel as step()).
	reportFn := starlark.NewBuiltin("report", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var msg string
		if err := starlark.UnpackArgs("report", a, kw, "msg", &msg); err != nil {
			return nil, err
		}
		rep(msg)
		return starlark.None, nil
	})

	// sleep(seconds) — bounded cooperative wait (capped at maxSleep). Cancels with
	// the run so a hung poll cannot outlive the action timeout.
	sleepFn := starlark.NewBuiltin("sleep", func(_ *starlark.Thread, _ *starlark.Builtin, a starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		var secsVal starlark.Value
		if err := starlark.UnpackArgs("sleep", a, kw, "seconds", &secsVal); err != nil {
			return nil, err
		}
		secs, ok := starlark.AsFloat(secsVal)
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

	return starlark.StringDict{
		"snapshot":     snapshot,
		"k8s":          k8s,
		"json":         starlarkjson.Module, // json.decode/encode for generic patches
		"fail":         failFn,
		"step":         stepFn,
		"report":       reportFn,
		"sleep":        sleepFn,
		"wait_rollout": waitRollout,
		"wait_ready":   waitReady,
	}
}

// reportSnapshots durably appends a batch of captures to the run's snapshot list
// on the control plane — called the instant each capture is recorded (log.onAdd),
// so a crash mid-run leaves a fully restorable ordered list. Best-effort.
func (r *Runner) reportSnapshots(ctx context.Context, actionID string, captures []Capture) {
	if len(captures) == 0 {
		return
	}
	snaps, _ := json.Marshal(captures)
	body, _ := json.Marshal(map[string]any{"status": "running", "snapshots": json.RawMessage(snaps)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.baseURL+"/api/agent/actions/"+actionID+"/result", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)
	if resp, err := r.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// executeSnapshotScript is the real run path for a snapshot-substrate action: it
// runs the Starlark source, reports every capture DURABLY as it happens, and on
// any failure auto-rolls-back via the ONE generic Restore. On success the
// mutations stand; the durable list remains for a later manual revert.
func (r *Runner) executeSnapshotScript(ctx context.Context, a Action) {
	var p struct {
		Source string            `json:"source"`
		Args   map[string]string `json:"args"`
	}
	if err := json.Unmarshal(a.Params, &p); err != nil || p.Source == "" {
		r.report(ctx, a.ID, "failed", "snapshot_script: missing source", "", nil)
		return
	}
	r.runSnapshotAction(ctx, a, p.Source, p.Args)
}

// runSnapshotAction is the shared run path for BOTH a custom snapshot_script and
// a dispatched built-in .star: run the source, report captures durably as they
// happen, auto-rollback via the generic Restore on any failure.
func (r *Runner) runSnapshotAction(ctx context.Context, a Action, source string, args map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, monitorTimeout)
	defer cancel()

	var states []stepState
	push := func(line string) { r.report(ctx, a.ID, "running", "", line, states) }

	log := newCaptureLog()
	log.onAdd = func(c Capture) {
		// durable the instant a mutation is captured — survives a crash mid-run.
		r.reportSnapshots(context.WithoutCancel(ctx), a.ID, []Capture{c})
	}

	err := r.execCapturedScript(ctx, log, source, args, push)
	if err == nil {
		r.report(ctx, a.ID, "succeeded", "workflow completed", "", states)
		if r.onExecuted != nil {
			r.onExecuted()
		}
		return
	}
	// failure → generic auto-rollback from the run's capture log (detached ctx
	// so a cancel/timeout does not also kill the rollback).
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer rcancel()
	results := r.Restore(rctx, log.List())
	undone := 0
	for _, x := range results {
		st := "ok"
		if x.OK {
			undone++
		} else {
			st = "failed"
		}
		states = append(states, stepState{Name: "rollback " + x.Kind + "/" + x.Name, Status: st, Detail: x.Detail})
	}
	r.report(ctx, a.ID, "failed", fmt.Sprintf("%s — rolled back %d/%d mutation(s)", err.Error(), undone, len(results)), "", states)
	if r.onExecuted != nil {
		r.onExecuted()
	}
}

// restoreFromParams parses a `{"snapshots":[...]}` param blob (the durable list
// the CP persisted) and replays it via the ONE generic Restore. Used by the
// snapshot_restore action the reaper enqueues on a crash, and by manual revert.
func (r *Runner) restoreFromParams(ctx context.Context, params json.RawMessage) ([]RestoreResult, error) {
	var p struct {
		Snapshots []Capture `json:"snapshots"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("snapshot_restore: bad params: %w", err)
	}
	return r.Restore(ctx, p.Snapshots), nil
}

// executeSnapshotRestore runs a `snapshot_restore` action: replay a durable
// snapshot list. Best-effort; reports exactly what could not be undone.
func (r *Runner) executeSnapshotRestore(ctx context.Context, a Action) {
	results, err := r.restoreFromParams(ctx, a.Params)
	if err != nil {
		r.report(ctx, a.ID, "failed", err.Error(), "", nil)
		return
	}
	states := make([]stepState, 0, len(results))
	failed := 0
	for _, x := range results {
		st := "ok"
		if !x.OK {
			st, failed = "failed", failed+1
		}
		states = append(states, stepState{Name: "restore " + x.Kind + "/" + x.Name, Status: st, Detail: x.Detail})
	}
	status, result := "succeeded", fmt.Sprintf("restored %d resource(s) from the snapshot list", len(results))
	if failed > 0 {
		status, result = "failed", fmt.Sprintf("%d of %d restores failed", failed, len(results))
	}
	r.report(ctx, a.ID, status, result, "", states)
	if r.onExecuted != nil {
		r.onExecuted()
	}
}

// runCapturedScript executes a snapshot-aware script against a fresh log and
// returns the run's capture log + the terminal error (nil on success). It does
// NOT auto-restore — the caller decides (mirrors executeManifest's finish/
// rollback split).
func (r *Runner) runCapturedScript(ctx context.Context, source string, args map[string]string, report func(string)) (*CaptureLog, error) {
	log := newCaptureLog()
	err := r.execCapturedScript(ctx, log, source, args, report)
	return log, err
}

// execCapturedScript runs a script against a CALLER-OWNED log — so the caller can
// set log.onAdd to report captures durably as they happen. Returns the terminal
// error (nil on success).
func (r *Runner) execCapturedScript(ctx context.Context, log *CaptureLog, source string, args map[string]string, report func(string)) error {
	globals := r.snapshotGlobals(ctx, log, report)
	argsDict := starlark.NewDict(len(args))
	for k, v := range args {
		_ = argsDict.SetKey(starlark.String(k), starlark.String(v))
	}
	globals["args"] = argsDict

	thread := &starlark.Thread{Name: "snapshot-script"}
	thread.SetMaxExecutionSteps(maxExecutionSteps)
	if _, err := starlark.ExecFileOptions(scriptFileOptions(), thread, "action.star", source, globals); err != nil {
		if ee, ok := err.(*starlark.EvalError); ok {
			return fmt.Errorf("%s", ee.Backtrace())
		}
		return err
	}
	return nil
}
