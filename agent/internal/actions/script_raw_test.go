package actions

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.starlark.net/starlark"
)

func callBuiltin(t *testing.T, dict starlark.StringDict, name string, kwargs map[string]starlark.Value) (starlark.Value, error) {
	t.Helper()
	fn := dict[name].(*starlark.Builtin)
	kw := make([]starlark.Tuple, 0, len(kwargs))
	for k, v := range kwargs {
		kw = append(kw, starlark.Tuple{starlark.String(k), v})
	}
	th := &starlark.Thread{Name: "test"}
	return fn.CallInternal(th, nil, kw)
}

func dictOf(t *testing.T, m map[string]starlark.Value) *starlark.Dict {
	t.Helper()
	d := starlark.NewDict(len(m))
	for k, v := range m {
		if err := d.SetKey(starlark.String(k), v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestRawApplyPatchDeleteWithUndo(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "ns"},
		Data:       map[string]string{"a": "1"},
	}
	r := testRunner(t, cm)
	var undo []undoEntry
	dict := r.rawBuiltins(context.Background(), func(e undoEntry) { undo = append(undo, e) }, nil)

	// raw_get liefert das Objekt
	v, err := callBuiltin(t, dict, "raw_get", map[string]starlark.Value{
		"api_version": starlark.String("v1"), "kind": starlark.String("ConfigMap"),
		"namespace": starlark.String("ns"), "name": starlark.String("cfg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v == starlark.None {
		t.Fatal("raw_get should find the configmap")
	}

	// raw_patch ändert + registriert Undo
	patch := dictOf(t, map[string]starlark.Value{
		"data": dictOf(t, map[string]starlark.Value{"a": starlark.String("2")}),
	})
	if _, err := callBuiltin(t, dict, "raw_patch", map[string]starlark.Value{
		"api_version": starlark.String("v1"), "kind": starlark.String("ConfigMap"),
		"namespace": starlark.String("ns"), "name": starlark.String("cfg"), "patch": patch,
	}); err != nil {
		t.Fatal(err)
	}
	if len(undo) != 1 || !strings.Contains(undo[0].desc, "restore ConfigMap/cfg") {
		t.Fatalf("undo = %+v", undo)
	}

	// raw_delete löscht + registriert Recreate
	if _, err := callBuiltin(t, dict, "raw_delete", map[string]starlark.Value{
		"api_version": starlark.String("v1"), "kind": starlark.String("ConfigMap"),
		"namespace": starlark.String("ns"), "name": starlark.String("cfg"),
	}); err != nil {
		t.Fatal(err)
	}
	if len(undo) != 2 || !strings.Contains(undo[1].desc, "recreate ConfigMap/cfg") {
		t.Fatalf("undo = %+v", undo)
	}
	// Recreate-Undo wirklich ausführen (LIFO: zuletzt registrierter zuerst)
	if err := undo[1].fn(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err = callBuiltin(t, dict, "raw_get", map[string]starlark.Value{
		"api_version": starlark.String("v1"), "kind": starlark.String("ConfigMap"),
		"namespace": starlark.String("ns"), "name": starlark.String("cfg"),
	})
	if err != nil || v == starlark.None {
		t.Fatalf("configmap should be recreated (err=%v)", err)
	}
}

func TestRawMutationDenylist(t *testing.T) {
	r := testRunner(t)
	dict := r.rawBuiltins(context.Background(), func(undoEntry) {}, nil)
	for _, kind := range []string{"ClusterRoleBinding", "Node", "Namespace", "MutatingWebhookConfiguration", "CustomResourceDefinition"} {
		_, err := callBuiltin(t, dict, "raw_delete", map[string]starlark.Value{
			"api_version": starlark.String("v1"), "kind": starlark.String(kind),
			"namespace": starlark.String("-"), "name": starlark.String("x"),
		})
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%s mutation must be denied, got %v", kind, err)
		}
	}
}

func TestRawMutationBudget(t *testing.T) {
	r := testRunner(t)
	dict := r.rawBuiltins(context.Background(), func(undoEntry) {}, nil)
	var lastErr error
	for i := 0; i <= maxRawMutations; i++ {
		manifest := dictOf(t, map[string]starlark.Value{
			"apiVersion": starlark.String("v1"), "kind": starlark.String("ConfigMap"),
			"metadata": dictOf(t, map[string]starlark.Value{"name": starlark.String("cm"), "namespace": starlark.String("ns")}),
			"data":     dictOf(t, map[string]starlark.Value{"i": starlark.String("x")}),
		})
		_, lastErr = callBuiltin(t, dict, "raw_apply", map[string]starlark.Value{"manifest": manifest})
	}
	if lastErr == nil || !strings.Contains(lastErr.Error(), "budget") {
		t.Fatalf("mutation %d should exhaust the budget, got %v", maxRawMutations+1, lastErr)
	}
}
