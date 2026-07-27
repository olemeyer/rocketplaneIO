package actions

// script_raw.go — shared Starlark ↔ Go conversion + GVR resolution used by the
// snapshot surface (script_snapshot.go). The former raw_* mutation builtins
// (rawBuiltins) were removed with the legacy custom-script engine; only the pure
// helpers remain.

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"go.starlark.net/starlark"
)

// dynamicResource: shared interface for namespaced/cluster-scoped access.
type dynamicResource = dynamic.ResourceInterface

// rawGVR resolves apiVersion+kind: the static map first, else the explicit
// resource plural (CRDs etc.).
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
		// conservative plural heuristic as a convenience — set resource= for edge cases.
		res = strings.ToLower(kind) + "s"
	}
	if gv.Version == "" {
		// An empty apiVersion yields an empty GroupVersion, and the dynamic client
		// then builds /api//namespaces/<ns>/<res> — the API server reads the path
		// shifted and rejects it with a confusing "cannot get resource <namespace>".
		// Core v1 is the only sane default for a kind we could not map.
		gv.Version = "v1"
	}
	return gv.WithResource(res), nil
}

// starToGo converts Starlark values to JSON-able Go values.
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

// goToStar converts JSON-like Go values to Starlark.
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
