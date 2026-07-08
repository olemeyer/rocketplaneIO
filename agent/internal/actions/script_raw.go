package actions

// script_raw.go — die generischen k8s.raw_*-Builtins der Starlark-Engine:
// kubectl-Äquivalenz im Untergrund. Ein Action-Script kann damit JEDE
// (whitelisted bzw. explizit adressierte) Ressource lesen, applyen, patchen
// und löschen — hermetisch, mit Auto-Snapshot + Undo-LIFO wie alle Mutationen:
//
//   k8s.raw_get(api_version, kind, namespace, name)            → dict|None
//   k8s.raw_list(api_version, kind, namespace, selector="", resource="") → [dict]
//   k8s.raw_apply(manifest)                                    — SSA; Undo: restore/delete
//   k8s.raw_patch(api_version, kind, namespace, name, patch)   — Merge; Undo: restore
//   k8s.raw_delete(api_version, kind, namespace, name)         — Undo: recreate
//
// Guards: RBAC-/Webhook-/CRD-Kinds sind für Mutationen GESPERRT (keine
// Selbst-Eskalation), Node/Namespace-Delete ebenso; max. 20 Mutationen pro
// Script; für Kinds außerhalb der statischen Karte gibt `resource=` (Plural)
// den expliziten Pfad — volle Abdeckung ohne Discovery-Magie.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"go.starlark.net/starlark"
)

// dynamicResource: gemeinsames Interface für namespaced/cluster-scoped Zugriff.
type dynamicResource = dynamic.ResourceInterface

const maxRawMutations = 20

// rawMutationDenied: Kinds, die ein Script NIE mutieren darf — Privilegien-
// Eskalation (RBAC/Webhooks/CRDs) und Cluster-Grundgerüst.
var rawMutationDenied = map[string]bool{
	"ClusterRole": true, "ClusterRoleBinding": true, "Role": true, "RoleBinding": true,
	"MutatingWebhookConfiguration": true, "ValidatingWebhookConfiguration": true,
	"CustomResourceDefinition": true, "Node": true, "Namespace": true,
	"ServiceAccount": true, "PriorityClass": true, "APIService": true,
}

// rawGVR löst apiVersion+kind auf: erst die statische Karte, sonst das
// explizite resource-Plural (CRDs etc.).
func rawGVR(apiVersion, kind, resourceOverride string) (schema.GroupVersionResource, error) {
	if gvr, ok := kindGVR[kind]; ok && resourceOverride == "" {
		return gvr, nil
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("invalid api_version %q: %v", apiVersion, err)
	}
	res := resourceOverride
	if res == "" {
		// konservative Plural-Heuristik als Komfort — für Sonderfälle resource= setzen.
		res = strings.ToLower(kind) + "s"
	}
	return gv.WithResource(res), nil
}

// starToGo konvertiert Starlark-Werte in JSON-fähige Go-Werte.
func starToGo(v starlark.Value) (any, error) {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.Int:
		if i, ok := x.Int64(); ok {
			return i, nil
		}
		return nil, fmt.Errorf("int too large")
	case starlark.Float:
		return float64(x), nil
	case starlark.String:
		return string(x), nil
	case *starlark.List:
		out := make([]any, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			e, err := starToGo(x.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, len(x))
		for _, e := range x {
			g, err := starToGo(e)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, x.Len())
		for _, k := range x.Keys() {
			ks, ok := k.(starlark.String)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			val, _, _ := x.Get(k)
			g, err := starToGo(val)
			if err != nil {
				return nil, err
			}
			out[string(ks)] = g
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported value type %s", v.Type())
}

// goToStar konvertiert JSON-artige Go-Werte in Starlark.
func goToStar(v any) starlark.Value {
	switch x := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(x)
	case int64:
		return starlark.MakeInt64(x)
	case float64:
		if x == float64(int64(x)) {
			return starlark.MakeInt64(int64(x))
		}
		return starlark.Float(x)
	case string:
		return starlark.String(x)
	case []any:
		out := make([]starlark.Value, 0, len(x))
		for _, e := range x {
			out = append(out, goToStar(e))
		}
		return starlark.NewList(out)
	case map[string]any:
		d := starlark.NewDict(len(x))
		for k, e := range x {
			_ = d.SetKey(starlark.String(k), goToStar(e))
		}
		return d
	}
	return starlark.String(fmt.Sprint(v))
}

// rawBuiltins baut die raw_*-Builtins; undoPush hängt Kompensationen auf den
// LIFO-Stack von executeScript.
func (r *Runner) rawBuiltins(ctx context.Context, undoPush func(undoEntry), reportLine func(string)) starlark.StringDict {
	mutations := 0
	guardMutation := func(kind string) error {
		if r.dyn == nil {
			return fmt.Errorf("raw access unavailable (no dynamic client)")
		}
		if rawMutationDenied[kind] {
			return fmt.Errorf("mutating %s from a script is not allowed", kind)
		}
		mutations++
		if mutations > maxRawMutations {
			return fmt.Errorf("raw mutation budget exhausted (max %d per script)", maxRawMutations)
		}
		return nil
	}
	resIface := func(gvr schema.GroupVersionResource, ns string) dynamicResource {
		if ns == "" || ns == "-" {
			return r.dyn.Resource(gvr)
		}
		return r.dyn.Resource(gvr).Namespace(ns)
	}

	rawGet := starlark.NewBuiltin("raw_get", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, name, resource string
		if err := starlark.UnpackArgs("raw_get", sargs, kwargs, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "name", &name, "resource?", &resource); err != nil {
			return nil, err
		}
		if r.dyn == nil {
			return nil, fmt.Errorf("raw access unavailable")
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		u, err := resIface(gvr, ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return starlark.None, nil
			}
			return nil, err
		}
		return goToStar(stripForSnapshot(u)), nil
	})

	rawList := starlark.NewBuiltin("raw_list", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, selector, resource string
		if err := starlark.UnpackArgs("raw_list", sargs, kwargs, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "selector?", &selector, "resource?", &resource); err != nil {
			return nil, err
		}
		if r.dyn == nil {
			return nil, fmt.Errorf("raw access unavailable")
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		list, err := resIface(gvr, ns).List(ctx, metav1.ListOptions{LabelSelector: selector, Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]starlark.Value, 0, len(list.Items))
		for i := range list.Items {
			out = append(out, goToStar(stripForSnapshot(&list.Items[i])))
		}
		return starlark.NewList(out), nil
	})

	rawApply := starlark.NewBuiltin("raw_apply", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var manifest *starlark.Dict
		var resource string
		if err := starlark.UnpackArgs("raw_apply", sargs, kwargs, "manifest", &manifest, "resource?", &resource); err != nil {
			return nil, err
		}
		obj, err := starToGo(manifest)
		if err != nil {
			return nil, err
		}
		m, _ := obj.(map[string]any)
		u := &unstructured.Unstructured{Object: m}
		kind, apiVersion, name, ns := u.GetKind(), u.GetAPIVersion(), u.GetName(), u.GetNamespace()
		if kind == "" || apiVersion == "" || name == "" {
			return nil, fmt.Errorf("manifest needs apiVersion, kind and metadata.name")
		}
		if err := guardMutation(kind); err != nil {
			return nil, err
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		iface := resIface(gvr, ns)

		// Before-Zustand für Undo sichern.
		before, getErr := iface.Get(ctx, name, metav1.GetOptions{})
		data, _ := json.Marshal(m)
		if _, err := iface.Patch(ctx, name, types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: "rocketplane-agent", Force: boolPtr(true)}); err != nil {
			return nil, err
		}
		if getErr == nil && before != nil {
			beforeJSON, _ := json.Marshal(stripForSnapshot(before))
			undoPush(undoEntry{desc: "restore " + kind + "/" + name, fn: func(uctx context.Context) error {
				_, err := iface.Patch(uctx, name, types.ApplyPatchType, beforeJSON, metav1.PatchOptions{FieldManager: "rocketplane-agent", Force: boolPtr(true)})
				return err
			}})
		} else {
			undoPush(undoEntry{desc: "delete created " + kind + "/" + name, fn: func(uctx context.Context) error {
				err := iface.Delete(uctx, name, metav1.DeleteOptions{})
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}})
		}
		if reportLine != nil {
			reportLine("raw_apply " + kind + "/" + name)
		}
		return starlark.None, nil
	})

	rawPatch := starlark.NewBuiltin("raw_patch", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, name, resource string
		var patch *starlark.Dict
		if err := starlark.UnpackArgs("raw_patch", sargs, kwargs, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "name", &name, "patch", &patch, "resource?", &resource); err != nil {
			return nil, err
		}
		if err := guardMutation(kind); err != nil {
			return nil, err
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		iface := resIface(gvr, ns)
		before, err := iface.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		obj, err := starToGo(patch)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(obj)
		if _, err := iface.Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{FieldManager: "rocketplane-agent"}); err != nil {
			return nil, err
		}
		beforeJSON, _ := json.Marshal(stripForSnapshot(before))
		undoPush(undoEntry{desc: "restore " + kind + "/" + name, fn: func(uctx context.Context) error {
			_, err := iface.Patch(uctx, name, types.ApplyPatchType, beforeJSON, metav1.PatchOptions{FieldManager: "rocketplane-agent", Force: boolPtr(true)})
			return err
		}})
		if reportLine != nil {
			reportLine("raw_patch " + kind + "/" + name)
		}
		return starlark.None, nil
	})

	rawDelete := starlark.NewBuiltin("raw_delete", func(_ *starlark.Thread, _ *starlark.Builtin, sargs starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var apiVersion, kind, ns, name, resource string
		if err := starlark.UnpackArgs("raw_delete", sargs, kwargs, "api_version", &apiVersion, "kind", &kind, "namespace", &ns, "name", &name, "resource?", &resource); err != nil {
			return nil, err
		}
		if err := guardMutation(kind); err != nil {
			return nil, err
		}
		gvr, err := rawGVR(apiVersion, kind, resource)
		if err != nil {
			return nil, err
		}
		iface := resIface(gvr, ns)
		before, err := iface.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return starlark.None, nil
			}
			return nil, err
		}
		recreate := stripForSnapshot(before)
		if err := iface.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			return nil, err
		}
		undoPush(undoEntry{desc: "recreate " + kind + "/" + name, fn: func(uctx context.Context) error {
			_, err := iface.Create(uctx, &unstructured.Unstructured{Object: recreate}, metav1.CreateOptions{FieldManager: "rocketplane-agent"})
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			return err
		}})
		if reportLine != nil {
			reportLine("raw_delete " + kind + "/" + name)
		}
		return starlark.None, nil
	})

	return starlark.StringDict{
		"raw_get":    rawGet,
		"raw_list":   rawList,
		"raw_apply":  rawApply,
		"raw_patch":  rawPatch,
		"raw_delete": rawDelete,
	}
}
