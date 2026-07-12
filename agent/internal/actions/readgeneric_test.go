package actions

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
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

// runStdlib runs an embedded built-in .star against the fake clients and returns
// the concatenated report/step output — the real snapshot-surface execution path.
func runStdlib(t *testing.T, r *Runner, kind string, args map[string]string) (string, error) {
	t.Helper()
	src, ok := stdlibScripts[kind]
	if !ok {
		t.Fatalf("no embedded script for %q", kind)
	}
	var out strings.Builder
	_, err := r.runCapturedScript(context.Background(), src, args, func(s string) {
		out.WriteString(s)
		out.WriteByte('\n')
	})
	return out.String(), err
}

// get_secret surfaces only key names — values are redacted by the runtime before
// they ever cross into script logic.
func TestGetSecretNeverRevealsValues(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cred", Namespace: "prod"},
		Data:       map[string][]byte{"password": []byte("super-geheim"), "user": []byte("admin")},
	}
	r := testRunner(t, sec)
	out, err := runStdlib(t, r, "get_secret", map[string]string{"namespace": "prod", "kind": "Secret", "name": "db-cred"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-geheim") || strings.Contains(out, "c3VwZXItZ2VoZWlt") {
		t.Fatalf("secret values leaked: %q", out)
	}
	if !strings.Contains(out, "password") || !strings.Contains(out, "user") {
		t.Fatalf("expected key names, got %q", out)
	}
}

// get_resource on a Secret shows the redaction marker, never the value (or its base64).
func TestGetResourceRedactsSecrets(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cred", Namespace: "prod"},
		Data:       map[string][]byte{"password": []byte("super-geheim")},
	}
	r := testRunner(t, sec)
	out, err := runStdlib(t, r, "get_resource", map[string]string{"namespace": "prod", "kind": "Secret", "name": "db-cred", "apiVersion": "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-geheim") || strings.Contains(out, "c3VwZXItZ2VoZWlt") {
		t.Fatalf("secret leaked through get_resource: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected redaction marker, got %q", out)
	}
}

// restore_resource refuses a snapshot whose identity does not match the target —
// no object smuggling. The guard fails before any apply, so no cluster is needed.
func TestRestoreResourceIdentityGuard(t *testing.T) {
	r := testRunner(t)
	_, err := runStdlib(t, r, "restore_resource", map[string]string{
		"namespace": "ns", "kind": "ConfigMap", "name": "app-config",
		"snapshot": `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"other","namespace":"ns"}}`,
	})
	if err == nil || !strings.Contains(err.Error(), "match") {
		t.Fatalf("mismatched snapshot identity must be rejected, got %v", err)
	}
}

// redactSecretData replaces every value with a {len, sha256} marker in place.
func TestRedactSecretData(t *testing.T) {
	obj := map[string]any{"data": map[string]any{"token": "secret-value"}}
	redactSecretData(obj)
	got := obj["data"].(map[string]any)["token"].(string)
	if strings.Contains(got, "secret-value") || !strings.HasPrefix(got, "REDACTED len=12 sha256=") {
		t.Fatalf("value not redacted: %q", got)
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
