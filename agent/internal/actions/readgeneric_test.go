package actions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func testRunner(t *testing.T, objs ...runtime.Object) *Runner {
	t.Helper()
	r := New("http://localhost:0", "tok", k8sfake.NewSimpleClientset(objs...), nil, nil)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	r.dyn = dynfake.NewSimpleDynamicClient(scheme, objs...)
	return r
}

func runSteps(t *testing.T, steps []step) (string, error) {
	t.Helper()
	var last string
	for _, s := range steps {
		out, err := s.run(context.Background(), func(string) {})
		if err != nil {
			return last, err
		}
		last = out
	}
	return last, nil
}

func TestGetSecretNeverRevealsValues(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cred", Namespace: "prod"},
		Data:       map[string][]byte{"password": []byte("super-geheim"), "user": []byte("admin")},
	}
	r := testRunner(t, sec)
	out, err := runSteps(t, r.planGetSecret(Action{TargetNamespace: "prod", TargetKind: "Secret", TargetName: "db-cred"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-geheim") || strings.Contains(out, "admin") {
		t.Fatalf("secret values leaked: %q", out)
	}
	if !strings.Contains(out, "password: len=12 sha256=") {
		t.Fatalf("expected redacted metadata, got %q", out)
	}
}

func TestGetResourceRedactsSecrets(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cred", Namespace: "prod"},
		Data:       map[string][]byte{"password": []byte("super-geheim")},
	}
	r := testRunner(t, sec)
	out, err := runSteps(t, r.planGetResource(Action{TargetNamespace: "prod", TargetKind: "Secret", TargetName: "db-cred"}))
	if err != nil {
		t.Fatal(err)
	}
	// base64 vom Klartext darf ebenfalls nicht auftauchen.
	if strings.Contains(out, "super-geheim") || strings.Contains(out, "c3VwZXItZ2VoZWlt") {
		t.Fatalf("secret leaked through get_resource: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected redaction marker, got %q", out)
	}
}

func TestGetResourceConfigMapFullData(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "prod"},
		Data:       map[string]string{"cluster.xml": "<yandex><remote_servers/></yandex>"},
	}
	r := testRunner(t, cm)
	out, err := runSteps(t, r.planGetResource(Action{TargetNamespace: "prod", TargetKind: "ConfigMap", TargetName: "app-config"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "remote_servers") {
		t.Fatalf("configmap data must be fully readable, got %q", out)
	}
}

func TestExecArgvValidation(t *testing.T) {
	bad := [][]string{
		{"rm", "-rf", "/"},
		{"cat", "/etc/passwd; rm -rf /"},
		{"sh", "-c", "id"},
		{"find", "/", "-delete"},
		{"cat", "$(whoami)"},
		{},
	}
	for _, argv := range bad {
		if err := validExecArgv(argv); err == nil {
			t.Errorf("argv %v must be rejected", argv)
		}
	}
	good := [][]string{
		{"cat", "/var/log/clickhouse-server/clickhouse-server.err.log"},
		{"tail", "-n", "200", "/var/log/app.log"},
		{"ls", "-la", "/etc/clickhouse-server/config.d"},
		{"df", "-h"},
		{"find", "/var/log", "-name", "*.err.log"},
	}
	for _, argv := range good {
		if err := validExecArgv(argv); err != nil {
			t.Errorf("argv %v must be allowed, got %v", argv, err)
		}
	}
}

func TestPatchSecretUndoRestoresValue(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("old-value")},
	}
	r := testRunner(t, sec)
	params, _ := json.Marshal(map[string]any{"key": "token", "value": "new-value"})
	a := Action{Kind: "patch_secret", TargetNamespace: "ns", TargetKind: "Secret", TargetName: "cred", Params: params}

	desc, undo := r.prepareUndo(context.Background(), a)
	if undo == nil || desc == "" {
		t.Fatal("patch_secret must be undoable")
	}
	if _, err := runSteps(t, r.planPatchSecret(a)); err != nil {
		t.Fatal(err)
	}
	got, _ := r.clientset.CoreV1().Secrets("ns").Get(context.Background(), "cred", metav1.GetOptions{})
	if string(got.Data["token"]) != "new-value" {
		t.Fatalf("patch did not apply: %q", got.Data["token"])
	}
	if err := undo(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = r.clientset.CoreV1().Secrets("ns").Get(context.Background(), "cred", metav1.GetOptions{})
	if string(got.Data["token"]) != "old-value" {
		t.Fatalf("undo did not restore: %q", got.Data["token"])
	}
}

func TestPVCExpandOnlyGrows(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "ns"},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			},
		},
	}
	r := testRunner(t, pvc)
	shrink, _ := json.Marshal(map[string]any{"size": "5Gi"})
	if _, err := runSteps(t, r.planPVCExpand(Action{TargetNamespace: "ns", TargetKind: "PersistentVolumeClaim", TargetName: "data", Params: shrink})); err == nil {
		t.Fatal("shrink must be rejected")
	}
	grow, _ := json.Marshal(map[string]any{"size": "20Gi"})
	if _, err := runSteps(t, r.planPVCExpand(Action{TargetNamespace: "ns", TargetKind: "PersistentVolumeClaim", TargetName: "data", Params: grow})); err != nil {
		t.Fatalf("grow must be allowed: %v", err)
	}
}

func TestRestoreResourceIdentityGuard(t *testing.T) {
	r := testRunner(t)
	snap := map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "other", "namespace": "ns"},
	}
	params, _ := json.Marshal(map[string]any{"snapshot": snap})
	_, err := runSteps(t, r.planRestoreResource(Action{Kind: "restore_resource", TargetNamespace: "ns", TargetKind: "ConfigMap", TargetName: "app-config", Params: params}))
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("mismatched snapshot identity must be rejected, got %v", err)
	}
}

func TestStripForSnapshot(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name": "x", "namespace": "ns", "uid": "123", "resourceVersion": "42",
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
			"annotations":   map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "{}"},
		},
		"status": map[string]any{"phase": "Active"},
		"data":   map[string]any{"k": "v"},
	}}
	obj := stripForSnapshot(u)
	md := obj["metadata"].(map[string]any)
	if _, ok := obj["status"]; ok {
		t.Fatal("status must be stripped")
	}
	for _, f := range []string{"uid", "resourceVersion", "managedFields", "annotations"} {
		if _, ok := md[f]; ok {
			t.Fatalf("%s must be stripped", f)
		}
	}
	if obj["data"].(map[string]any)["k"] != "v" {
		t.Fatal("data must survive")
	}
}
