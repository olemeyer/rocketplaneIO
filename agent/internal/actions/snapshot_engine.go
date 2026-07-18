package actions

// snapshot_engine.go — the snapshot substrate (Safe Actions, snapshot-rollback
// design). One ordered, serializable capture log per run + ONE generic restore.
// It replaces the per-kind inverse matrix (revert.go): instead of hand-writing
// an inverse for every kind, a mutation records the MINIMAL restorable delta —
// the whole object, or the exact field paths it touched — and restore replays
// the log in reverse. Field-scoped captures make a key-level mutation (a
// ConfigMap key, one env var) restore only its key, never clobbering concurrent
// edits to sibling keys.
//
// This file is pure engine (capture + restore over the dynamic client). The
// Starlark builtins that call it live in script_snapshot.go; nothing here
// touches plan()/executeManifest.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// FieldDelta is one restorable field path + its before-value. Existed=false means
// the path was absent (restore removes it via a JSON-merge null).
type FieldDelta struct {
	Path    []string `json:"path"`
	Before  any      `json:"before,omitempty"`
	Existed bool     `json:"existed"`
}

// Capture is one entry in the log: either a whole stripped object (Scope=object)
// or a set of field deltas (Scope=field). It is JSON-serializable so it can be
// persisted to the control plane for crash-recovery + later manual revert.
//
// APIVersion and Resource are optional: when set they override the static kindGVR
// map so the Restore path resolves arbitrary GVKs (CRDs etc.). Old persisted
// captures that lack these fields continue to resolve via kindGVR (backward compat).
type Capture struct {
	Seq        int            `json:"seq"`
	Kind       string         `json:"kind"`
	Namespace  string         `json:"namespace"`
	Name       string         `json:"name"`
	APIVersion string         `json:"apiVersion,omitempty"`
	Resource   string         `json:"resource,omitempty"`
	Existed    bool           `json:"existed"` // whole object existed before (Scope=object)
	Scope      string         `json:"scope"`   // "object" | "field"
	Object     map[string]any `json:"object,omitempty"`
	Fields     []FieldDelta   `json:"fields,omitempty"`
	// Enc holds the AES-GCM ciphertext of {object,fields} for Secret captures, so
	// the durable copy persisted on the control plane never carries plaintext. The
	// in-memory log keeps Object/Fields for same-run auto-rollback; only the durable
	// report is encrypted, and Restore decrypts it.
	Enc string `json:"enc,omitempty"`
}

// CaptureLog is the ordered undo state bound to one run. First-capture wins per
// (kind/ns/name[/path]) so the earliest (true before-state) is what restore uses.
type CaptureLog struct {
	mu      sync.Mutex
	seq     int
	entries []Capture
	seen    map[string]bool
	// onAdd fires the instant a capture is recorded — the hook execute() uses to
	// report it DURABLY to the control plane (append to the ordered snapshot
	// list) before the mutation's effect is even observed, so a crash mid-run is
	// fully restorable. nil in unit tests.
	onAdd func(Capture)
}

func newCaptureLog() *CaptureLog { return &CaptureLog{seen: map[string]bool{}} }

func (l *CaptureLog) add(c Capture) Capture {
	l.mu.Lock()
	c.Seq = l.seq
	l.seq++
	l.entries = append(l.entries, c)
	hook := l.onAdd
	l.mu.Unlock()
	if hook != nil {
		hook(c) // durable-report outside the lock
	}
	return c
}

// List returns a copy of the log in capture order.
func (l *CaptureLog) List() []Capture {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Capture(nil), l.entries...)
}

func (l *CaptureLog) markSeen(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[key] {
		return false
	}
	l.seen[key] = true
	return true
}

// dynIface resolves the dynamic client interface for the given kind. apiVersion
// and resource are optional overrides: when both are empty and the kind is in
// the static map the fast path is used; otherwise rawGVR resolves the GVR.
func (r *Runner) dynIface(apiVersion, kind, namespace, resource string) (dynamicResource, error) {
	if r.dyn == nil {
		return nil, fmt.Errorf("generic access unavailable (no dynamic client)")
	}
	gvr, err := resolveGVR(apiVersion, kind, resource)
	if err != nil {
		return nil, err
	}
	if clusterScopedKind(kind) || namespace == "-" || namespace == "" {
		return r.dyn.Resource(gvr), nil
	}
	return r.dyn.Resource(gvr).Namespace(namespace), nil
}

// captureObject records the whole stripped before-state of a resource (or that
// it was absent). Used before an object-level mutation (patch, apply, create,
// delete). First-wins per (apiVersion/kind/ns/name). apiVersion and resource are
// optional: leave empty for well-known kinds in kindGVR.
func (r *Runner) captureObject(ctx context.Context, log *CaptureLog, apiVersion, kind, namespace, name, resource string) (*Capture, error) {
	key := "obj|" + apiVersion + "|" + kind + "|" + namespace + "|" + name + "|" + resource
	if !log.markSeen(key) {
		return nil, nil // already captured — keep the earlier before-state
	}
	u, err := r.getUnstructured(ctx, apiVersion, kind, namespace, name, resource)
	if apierrors.IsNotFound(err) {
		c := log.add(Capture{Kind: kind, Namespace: namespace, Name: name, APIVersion: apiVersion, Resource: resource, Existed: false, Scope: "object"})
		return &c, nil
	}
	if err != nil {
		return nil, err
	}
	obj := stripForSnapshot(u)
	c := log.add(Capture{Kind: kind, Namespace: namespace, Name: name, APIVersion: apiVersion, Resource: resource, Existed: true, Scope: "object", Object: obj})
	return &c, nil
}

// captureFields records the before-value of the exact paths a key-level mutation
// will touch (ConfigMap data[key], one container's env var, a metadata key). The
// restore is surgical — sibling keys are untouched. apiVersion and resource are
// optional overrides (same semantics as captureObject).
func (r *Runner) captureFields(ctx context.Context, log *CaptureLog, apiVersion, kind, namespace, name, resource string, paths [][]string) (*Capture, error) {
	key := "fld|" + apiVersion + "|" + kind + "|" + namespace + "|" + name + "|" + resource + "|" + fieldsKey(paths)
	if !log.markSeen(key) {
		return nil, nil
	}
	u, err := r.getUnstructured(ctx, apiVersion, kind, namespace, name, resource)
	if err != nil {
		return nil, err // a field mutation on an absent object is the caller's error
	}
	deltas := make([]FieldDelta, 0, len(paths))
	for _, p := range paths {
		before, ok := nestedGet(u.Object, p)
		deltas = append(deltas, FieldDelta{Path: append([]string(nil), p...), Before: before, Existed: ok})
	}
	c := log.add(Capture{Kind: kind, Namespace: namespace, Name: name, APIVersion: apiVersion, Resource: resource, Existed: true, Scope: "field", Fields: deltas})
	return &c, nil
}

// RestoreResult is the per-entry outcome (best-effort: restore continues past a
// failed entry and reports exactly what could not be undone).
type RestoreResult struct {
	Kind, Namespace, Name, Detail string
	OK                            bool
}

// Restore replays the capture log in REVERSE order — the ONE generic rollback,
// used for both auto-rollback-on-failure and later manual revert. No per-kind
// logic: absent→delete, field→surgical merge-patch, object→server-side apply.
// Each capture's own APIVersion/Resource fields (when set) drive resolution so
// the restore works for arbitrary GVKs, including CRDs.
func (r *Runner) Restore(ctx context.Context, captures []Capture) []RestoreResult {
	out := make([]RestoreResult, 0, len(captures))
	for i := len(captures) - 1; i >= 0; i-- {
		c := r.decryptCapture(captures[i]) // Secret payloads arrive encrypted
		res := RestoreResult{Kind: c.Kind, Namespace: c.Namespace, Name: c.Name, OK: true}
		iface, err := r.dynIface(c.APIVersion, c.Kind, c.Namespace, c.Resource)
		if err != nil {
			res.OK, res.Detail = false, err.Error()
			out = append(out, res)
			continue
		}
		switch {
		case !c.Existed && c.Scope == "object":
			// We created it → remove it.
			if err := iface.Delete(ctx, c.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				res.OK, res.Detail = false, err.Error()
			} else {
				res.Detail = "deleted (was created by this run)"
			}
		case c.Scope == "field":
			patch := fieldsMergePatch(c.Fields)
			data, _ := json.Marshal(patch)
			if _, err := iface.Patch(ctx, c.Name, types.MergePatchType, data, metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
				res.OK, res.Detail = false, err.Error()
			} else {
				res.Detail = fmt.Sprintf("restored %d field(s)", len(c.Fields))
			}
		default: // Scope=object, existed → whole-object restore via MERGE-PATCH.
			// Merge-patch (not Update/replace) so restore uses the `patch` verb the
			// agent's RBAC already grants for its mutations — Update needs a
			// separate `update` verb the agent does not have. The captured object
			// is identity/status-stripped, so the patch carries no resourceVersion.
			u := (&unstructured.Unstructured{Object: c.Object}).DeepCopy()
			ensureGVK(u, c.Kind, c.APIVersion)
			got, gerr := iface.Get(ctx, c.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(gerr) {
				// deleted since → recreate best-effort (new identity, orphaned).
				u.SetResourceVersion("")
				if _, err := iface.Create(ctx, u, metav1.CreateOptions{FieldManager: "rocketplane-agent"}); err != nil && !apierrors.IsAlreadyExists(err) {
					res.OK, res.Detail = false, err.Error()
				} else {
					res.Detail = "recreated from before-state"
				}
			} else if gerr != nil {
				res.OK, res.Detail = false, gerr.Error()
			} else {
				// Diff against the CURRENT (stripped) object so keys the run ADDED
				// (a taint, spec.unschedulable, spec.paused) are explicitly nulled —
				// a plain merge-patch of the before-object alone cannot remove them.
				cur := stripForSnapshot(got)
				patch := restoreMergePatch(c.Object, cur)
				data, _ := json.Marshal(patch)
				if _, err := iface.Patch(ctx, c.Name, types.MergePatchType, data, metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
					res.OK, res.Detail = false, err.Error()
				} else {
					res.Detail = "restored object to before-state"
				}
			}
		}
		out = append(out, res)
	}
	return out
}

// ── helpers ─────────────────────────────────────────────────────────────────

func fieldsKey(paths [][]string) string {
	parts := make([]string, len(paths))
	for i, p := range paths {
		parts[i] = strings.Join(p, ".")
	}
	return strings.Join(parts, ",")
}

// nestedGet reads obj at path; ok=false if any segment is missing.
func nestedGet(obj map[string]any, path []string) (any, bool) {
	cur := any(obj)
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[seg]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// fieldsMergePatch builds one JSON-merge-patch that restores each field to its
// before-value (or null to remove a field that didn't exist before).
func fieldsMergePatch(fields []FieldDelta) map[string]any {
	root := map[string]any{}
	for _, f := range fields {
		var val any
		if f.Existed {
			val = f.Before
		} else {
			val = nil // JSON-merge null deletes the key
		}
		setNested(root, f.Path, val)
	}
	return root
}

// restoreMergePatch builds a JSON-merge-patch that turns `current` back into
// `before`: it carries before's values AND nulls any key present in current but
// absent in before. Plain merge-patch of the before-object alone restores changed
// values but CANNOT remove a key the run added (a node taint, spec.unschedulable,
// spec.paused, a new ConfigMap/Secret key) — this closes that gap while staying a
// merge-patch (the `patch` verb, which the agent's RBAC grants). Nested maps
// recurse; arrays/scalars are replaced wholesale (merge-patch semantics), so
// before's value wins.
func restoreMergePatch(before, current map[string]any) map[string]any {
	out := map[string]any{}
	for k, bv := range before {
		cv, inCur := current[k]
		bm, bok := bv.(map[string]any)
		cm, cok := cv.(map[string]any)
		if bok && cok && inCur {
			out[k] = restoreMergePatch(bm, cm)
		} else {
			out[k] = bv
		}
	}
	for k := range current {
		if _, ok := before[k]; !ok {
			out[k] = nil // remove keys added since the snapshot
		}
	}
	return out
}

func setNested(root map[string]any, path []string, val any) {
	cur := root
	for i, seg := range path {
		if i == len(path)-1 {
			cur[seg] = val
			return
		}
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
}

// ensureGVK makes a stripped object re-applyable (apiVersion+kind must be set).
// captureAPIVersion (from Capture.APIVersion) is preferred when the static map
// does not cover the kind, enabling CRD captures to round-trip correctly.
func ensureGVK(u *unstructured.Unstructured, kind, captureAPIVersion string) {
	if u.GetKind() == "" {
		u.SetKind(kind)
	}
	if u.GetAPIVersion() == "" {
		if gvr, ok := kindGVR[kind]; ok {
			gv := schema.GroupVersion{Group: gvr.Group, Version: gvr.Version}
			u.SetAPIVersion(gv.String())
		} else if captureAPIVersion != "" {
			u.SetAPIVersion(captureAPIVersion)
		}
	}
}
