package actions

import (
	"context"
	"encoding/json"
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

// k8s_get on a Secret shows the redaction marker, never the value.
func TestK8sGetRedactsSecrets(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cred", Namespace: "prod"},
		Data:       map[string][]byte{"password": []byte("super-geheim")},
	}
	r := testRunner(t, sec)
	out, err := runStdlib(t, r, "k8s_get", map[string]string{
		"namespace": "prod", "kind": "Secret", "name": "db-cred",
		"apiVersion": "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-geheim") || strings.Contains(out, "c3VwZXItZ2VoZWlt") {
		t.Fatalf("secret leaked through k8s_get: %q", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected redaction marker, got %q", out)
	}
}

// k8s_get on a ConfigMap returns its data.
func TestK8sGetConfigMap(t *testing.T) {
	r := snapRunner(t, cm("ns", "mycfg", map[string]any{"key": "val"}))
	out, err := runStdlib(t, r, "k8s_get", map[string]string{
		"namespace": "ns", "kind": "ConfigMap", "name": "mycfg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "val") {
		t.Fatalf("expected configmap data in output, got %q", out)
	}
}

// k8s_get fails gracefully when the object is not found.
func TestK8sGetNotFound(t *testing.T) {
	r := snapRunner(t)
	_, err := runStdlib(t, r, "k8s_get", map[string]string{
		"namespace": "ns", "kind": "ConfigMap", "name": "missing",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// k8s_list reports a JSON header line plus one JSON document per item — and
// every line survives, which is the point: report() used to overwrite, so a
// list of N objects arrived as one.
func TestK8sListConfigMaps(t *testing.T) {
	r := snapRunner(t,
		cm("ns", "cm1", map[string]any{"a": "1"}),
		cm("ns", "cm2", map[string]any{"b": "2"}),
	)
	out, err := runStdlib(t, r, "k8s_list", map[string]string{
		"namespace": "ns", "kind": "ConfigMap",
	})
	if err != nil {
		t.Fatal(err)
	}
	var header struct {
		Kind      string `json:"kind"`
		Namespace string `json:"namespace"`
		Count     int    `json:"count"`
	}
	lines := jsonLines(t, out)
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 items, got %d lines: %q", len(lines), out)
	}
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header not JSON: %v (%q)", err, lines[0])
	}
	if header.Count != 2 || header.Kind != "ConfigMap" || header.Namespace != "ns" {
		t.Fatalf("unexpected header: %+v", header)
	}
	for _, name := range []string{"cm1", "cm2"} {
		if !strings.Contains(out, `"name":"`+name+`"`) {
			t.Fatalf("item %s missing from output: %q", name, out)
		}
	}
}

// k8s_get reports the object as JSON, not as a Starlark repr — an MCP client
// has to be able to parse it.
func TestK8sGetReportsJSON(t *testing.T) {
	r := snapRunner(t, cm("ns", "cm1", map[string]any{"a": "1"}))
	out, err := runStdlib(t, r, "k8s_get", map[string]string{
		"namespace": "ns", "kind": "ConfigMap", "name": "cm1",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := jsonLines(t, out)
	if len(lines) != 1 {
		t.Fatalf("expected one JSON line, got %d: %q", len(lines), out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("object not JSON: %v (%q)", err, lines[0])
	}
	data, _ := obj["data"].(map[string]any)
	if obj["kind"] != "ConfigMap" || data["a"] != "1" {
		t.Fatalf("unexpected payload: %+v", obj)
	}
}

// jsonLines strips the "step: …" prefix line the harness emits and returns the
// remaining report lines.
func jsonLines(t *testing.T, out string) []string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "step:") {
			continue
		}
		lines = append(lines, l)
	}
	return lines
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
