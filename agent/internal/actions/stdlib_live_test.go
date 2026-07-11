//go:build live

package actions

// stdlib_live_test.go — runs the REAL embedded built-in scripts against a REAL
// kube-apiserver (minikube) to prove what the fake dynamic client cannot:
// strategic-merge (set_image/set_env), json-merge patches, node taint read-
// modify-write, delete->recreate and create->delete, and the field-scoped vs
// whole-object restore round-trips. Each check runs a script through the same
// capture log the production path uses, then replays the ONE generic Restore and
// asserts the resource is back to its before-state.
//
//	KUBECONTEXT=minikube go test -tags live ./internal/actions/ -run TestStdlibLive -v -timeout 600s

import (
	"context"
	"os"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const liveNS = "rp-live"

func liveRunner(t *testing.T) *Runner {
	t.Helper()
	ctxName := os.Getenv("KUBECONTEXT")
	if ctxName == "" {
		ctxName = "minikube"
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{CurrentContext: ctxName}).ClientConfig()
	if err != nil {
		t.Skipf("no kubeconfig for context %q: %v", ctxName, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	return New("http://127.0.0.1:0", "tok", cs, cfg, nil) // New builds the dynamic client from cfg
}

func applyU(t *testing.T, r *Runner, kind, ns string, obj map[string]any) {
	t.Helper()
	gvr := kindGVR[kind]
	u := &unstructured.Unstructured{Object: obj}
	iface := r.dyn.Resource(gvr).Namespace(ns)
	if ns == "" || ns == "-" {
		iface = r.dyn.Resource(gvr)
	}
	_, err := iface.Create(context.Background(), u, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return
	}
	if err != nil {
		t.Fatalf("create %s/%s: %v", kind, u.GetName(), err)
	}
}

func liveGet(t *testing.T, r *Runner, kind, ns, name string) map[string]any {
	t.Helper()
	gvr := kindGVR[kind]
	iface := r.dyn.Resource(gvr).Namespace(ns)
	if ns == "" || ns == "-" {
		iface = r.dyn.Resource(gvr)
	}
	u, err := iface.Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s/%s: %v", kind, name, err)
	}
	return u.Object
}

func liveGone(t *testing.T, r *Runner, kind, ns, name string) bool {
	t.Helper()
	gvr := kindGVR[kind]
	iface := r.dyn.Resource(gvr).Namespace(ns)
	if ns == "" || ns == "-" {
		iface = r.dyn.Resource(gvr)
	}
	_, err := iface.Get(context.Background(), name, metav1.GetOptions{})
	return apierrors.IsNotFound(err)
}

// roundtrip runs a real embedded script, checks the mutation, replays Restore,
// then checks the before-state is back. after/back may be nil (reads).
func roundtrip(t *testing.T, r *Runner, kind string, args map[string]string, after, back func(t *testing.T)) {
	t.Helper()
	src, ok := stdlibScripts[kind]
	if !ok {
		t.Fatalf("no embedded script for %q", kind)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log, err := r.runCapturedScript(ctx, src, args, func(s string) { t.Logf("  %s: %s", kind, s) })
	if err != nil {
		t.Fatalf("%s run: %v", kind, err)
	}
	if after != nil {
		after(t)
	}
	for _, x := range r.Restore(ctx, log.List()) {
		if !x.OK {
			t.Fatalf("%s restore %s/%s: %s", kind, x.Kind, x.Name, x.Detail)
		}
	}
	if back != nil {
		back(t)
	}
}

func TestStdlibLive(t *testing.T) {
	r := liveRunner(t)
	ctx := context.Background()
	ns := liveNS

	// fresh namespace
	_ = r.clientset.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{})
	for i := 0; i < 60; i++ {
		if _, err := r.clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			break
		}
		time.Sleep(time.Second)
	}
	applyU(t, r, "Namespace", "", map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns},
	})
	t.Cleanup(func() { _ = r.clientset.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	// ── fixtures ──
	applyU(t, r, "Deployment", ns, map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": ns, "labels": map[string]any{"app": "web"}},
		"spec": map[string]any{
			"replicas": int64(1),
			"selector": map[string]any{"matchLabels": map[string]any{"app": "web"}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": "web"}},
				"spec": map[string]any{"containers": []any{map[string]any{
					"name": "web", "image": "nginx:1.25-alpine",
					"ports": []any{map[string]any{"containerPort": int64(80)}},
				}}},
			},
		},
	})
	applyU(t, r, "ConfigMap", ns, map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "cfg", "namespace": ns},
		"data":     map[string]any{"mode": "prod", "keep": "x"},
	})
	applyU(t, r, "Service", ns, map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": "svc", "namespace": ns},
		"spec": map[string]any{
			"selector": map[string]any{"app": "web"},
			"ports":    []any{map[string]any{"port": int64(80), "targetPort": int64(80)}},
		},
	})
	applyU(t, r, "HorizontalPodAutoscaler", ns, map[string]any{
		"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler",
		"metadata": map[string]any{"name": "hpa", "namespace": ns},
		"spec": map[string]any{
			"minReplicas": int64(1), "maxReplicas": int64(3),
			"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"},
		},
	})
	applyU(t, r, "PodDisruptionBudget", ns, map[string]any{
		"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
		"metadata": map[string]any{"name": "pdb", "namespace": ns},
		"spec": map[string]any{
			"minAvailable": int64(1),
			"selector":     map[string]any{"matchLabels": map[string]any{"app": "web"}},
		},
	})

	get := func(kind, name string) map[string]any { return liveGet(t, r, kind, ns, name) }
	getCluster := func(kind, name string) map[string]any { return liveGet(t, r, kind, "", name) }
	nget := func(path ...string) func(obj map[string]any) any {
		return func(obj map[string]any) any { v, _ := nestedGet(obj, path); return v }
	}
	_ = nget

	// ── field-scoped: annotate add -> restore removes, sibling intact ──
	t.Run("annotate", func(t *testing.T) {
		roundtrip(t, r, "annotate",
			map[string]string{"namespace": ns, "kind": "Deployment", "name": "web", "key": "note", "value": "maint"},
			func(t *testing.T) {
				anns, _ := nestedGet(get("Deployment", "web"), []string{"metadata", "annotations"})
				if m, _ := anns.(map[string]any); m["note"] != "maint" {
					t.Fatalf("annotation not set: %v", anns)
				}
			},
			func(t *testing.T) {
				anns, _ := nestedGet(get("Deployment", "web"), []string{"metadata", "annotations"})
				if m, _ := anns.(map[string]any); m != nil {
					if _, ok := m["note"]; ok {
						t.Fatalf("annotation not removed on restore: %v", m)
					}
				}
			})
	})

	// ── configmap key add + remove ──
	t.Run("patch_configmap_add", func(t *testing.T) {
		roundtrip(t, r, "patch_configmap",
			map[string]string{"namespace": ns, "name": "cfg", "key": "feature", "value": "on"},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfg"), []string{"data"})
				m, _ := d.(map[string]any)
				if m["feature"] != "on" || m["mode"] != "prod" || m["keep"] != "x" {
					t.Fatalf("configmap add wrong: %v", m)
				}
			},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfg"), []string{"data"})
				m, _ := d.(map[string]any)
				if _, ok := m["feature"]; ok {
					t.Fatalf("feature not removed on restore: %v", m)
				}
				if m["keep"] != "x" {
					t.Fatalf("sibling clobbered: %v", m)
				}
			})
	})
	t.Run("patch_configmap_remove", func(t *testing.T) {
		roundtrip(t, r, "patch_configmap",
			map[string]string{"namespace": ns, "name": "cfg", "key": "mode", "remove": "true"},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfg"), []string{"data"})
				if m, _ := d.(map[string]any); m["mode"] != nil {
					t.Fatalf("mode not removed: %v", m)
				}
			},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfg"), []string{"data"})
				if m, _ := d.(map[string]any); m["mode"] != "prod" {
					t.Fatalf("mode not restored: %v", m)
				}
			})
	})

	// ── hpa bounds ──
	t.Run("hpa_set", func(t *testing.T) {
		roundtrip(t, r, "hpa_set",
			map[string]string{"namespace": ns, "name": "hpa", "minReplicas": "2", "maxReplicas": "6"},
			func(t *testing.T) {
				o := get("HorizontalPodAutoscaler", "hpa")
				if v, _ := nestedGet(o, []string{"spec", "minReplicas"}); toI(v) != 2 {
					t.Fatalf("min not 2: %v", v)
				}
				if v, _ := nestedGet(o, []string{"spec", "maxReplicas"}); toI(v) != 6 {
					t.Fatalf("max not 6: %v", v)
				}
			},
			func(t *testing.T) {
				o := get("HorizontalPodAutoscaler", "hpa")
				if v, _ := nestedGet(o, []string{"spec", "minReplicas"}); toI(v) != 1 {
					t.Fatalf("min not restored: %v", v)
				}
				if v, _ := nestedGet(o, []string{"spec", "maxReplicas"}); toI(v) != 3 {
					t.Fatalf("max not restored: %v", v)
				}
			})
	})

	// ── pdb: switch minAvailable -> maxUnavailable, restore back ──
	t.Run("pdb_set", func(t *testing.T) {
		roundtrip(t, r, "pdb_set",
			map[string]string{"namespace": ns, "name": "pdb", "maxUnavailable": "1"},
			func(t *testing.T) {
				o := get("PodDisruptionBudget", "pdb")
				if v, _ := nestedGet(o, []string{"spec", "maxUnavailable"}); toI(v) != 1 {
					t.Fatalf("maxUnavailable not 1: %v", v)
				}
			},
			func(t *testing.T) {
				o := get("PodDisruptionBudget", "pdb")
				if v, _ := nestedGet(o, []string{"spec", "minAvailable"}); toI(v) != 1 {
					t.Fatalf("minAvailable not restored: %v", v)
				}
			})
	})

	// ── generic json patch on a Service ──
	t.Run("patch_resource", func(t *testing.T) {
		roundtrip(t, r, "patch_resource",
			map[string]string{"kind": "Service", "namespace": ns, "name": "svc", "patch": `{"spec":{"sessionAffinity":"ClientIP"}}`},
			func(t *testing.T) {
				if v, _ := nestedGet(get("Service", "svc"), []string{"spec", "sessionAffinity"}); v != "ClientIP" {
					t.Fatalf("sessionAffinity not set: %v", v)
				}
			},
			func(t *testing.T) {
				// restored to before (None is the default)
				if v, _ := nestedGet(get("Service", "svc"), []string{"spec", "sessionAffinity"}); v == "ClientIP" {
					t.Fatalf("sessionAffinity not restored: %v", v)
				}
			})
	})

	// ── node taint read-modify-write ──
	t.Run("node_taint", func(t *testing.T) {
		node := nodeName(t, r)
		roundtrip(t, r, "node_taint",
			map[string]string{"namespace": "-", "kind": "Node", "name": node, "key": "rp-live", "value": "x", "effect": "PreferNoSchedule"},
			func(t *testing.T) {
				if !hasTaint(getCluster("Node", node), "rp-live") {
					t.Fatalf("taint not added")
				}
			},
			func(t *testing.T) {
				if hasTaint(getCluster("Node", node), "rp-live") {
					t.Fatalf("taint not removed on restore")
				}
			})
	})

	// ── delete_configmap: delete -> restore recreates with data ──
	t.Run("delete_configmap", func(t *testing.T) {
		applyU(t, r, "ConfigMap", ns, map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cfgdel", "namespace": ns},
			"data":     map[string]any{"a": "1"},
		})
		roundtrip(t, r, "delete_configmap",
			map[string]string{"namespace": ns, "name": "cfgdel"},
			func(t *testing.T) {
				if !liveGone(t, r, "ConfigMap", ns, "cfgdel") {
					t.Fatalf("configmap not deleted")
				}
			},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfgdel"), []string{"data"})
				if m, _ := d.(map[string]any); m["a"] != "1" {
					t.Fatalf("configmap not recreated with data: %v", m)
				}
			})
	})

	// ── create_configmap: create -> restore deletes ──
	t.Run("create_configmap", func(t *testing.T) {
		roundtrip(t, r, "create_configmap",
			map[string]string{"namespace": ns, "name": "cfgnew", "data": `{"a":"1","b":"2"}`},
			func(t *testing.T) {
				d, _ := nestedGet(get("ConfigMap", "cfgnew"), []string{"data"})
				if m, _ := d.(map[string]any); m["a"] != "1" || m["b"] != "2" {
					t.Fatalf("configmap not created: %v", m)
				}
			},
			func(t *testing.T) {
				if !liveGone(t, r, "ConfigMap", ns, "cfgnew") {
					t.Fatalf("created configmap not deleted on restore")
				}
			})
	})

	// ── cordon -> restore uncordons ──
	t.Run("cordon", func(t *testing.T) {
		node := nodeName(t, r)
		roundtrip(t, r, "cordon",
			map[string]string{"namespace": "-", "kind": "Node", "name": node},
			func(t *testing.T) {
				if v, _ := nestedGet(getCluster("Node", node), []string{"spec", "unschedulable"}); v != true {
					t.Fatalf("node not cordoned: %v", v)
				}
			},
			func(t *testing.T) {
				v, _ := nestedGet(getCluster("Node", node), []string{"spec", "unschedulable"})
				if v == true {
					t.Fatalf("node not uncordoned on restore")
				}
			})
	})

	// ── waiting mutations (real rollout) ──
	t.Run("scale", func(t *testing.T) {
		roundtrip(t, r, "scale",
			map[string]string{"namespace": ns, "kind": "Deployment", "name": "web", "replicas": "2"},
			func(t *testing.T) {
				if v, _ := nestedGet(get("Deployment", "web"), []string{"spec", "replicas"}); toI(v) != 2 {
					t.Fatalf("replicas not 2: %v", v)
				}
			},
			func(t *testing.T) {
				if v, _ := nestedGet(get("Deployment", "web"), []string{"spec", "replicas"}); toI(v) != 1 {
					t.Fatalf("replicas not restored to 1: %v", v)
				}
			})
	})
	t.Run("set_image", func(t *testing.T) {
		roundtrip(t, r, "set_image",
			map[string]string{"namespace": ns, "kind": "Deployment", "name": "web", "container": "web", "image": "nginx:1.27-alpine"},
			func(t *testing.T) {
				if img := containerImage(get("Deployment", "web"), "web"); img != "nginx:1.27-alpine" {
					t.Fatalf("image not updated: %v", img)
				}
			},
			func(t *testing.T) {
				if img := containerImage(get("Deployment", "web"), "web"); img != "nginx:1.25-alpine" {
					t.Fatalf("image not restored: %v", img)
				}
			})
	})
	t.Run("set_env", func(t *testing.T) {
		roundtrip(t, r, "set_env",
			map[string]string{"namespace": ns, "kind": "Deployment", "name": "web", "container": "web", "envName": "LOG_LEVEL", "value": "debug"},
			func(t *testing.T) {
				if !hasEnv(get("Deployment", "web"), "web", "LOG_LEVEL") {
					t.Fatalf("env not set")
				}
			},
			func(t *testing.T) {
				if hasEnv(get("Deployment", "web"), "web", "LOG_LEVEL") {
					t.Fatalf("env not removed on restore")
				}
			})
	})

	// ── reads (no mutation) ──
	t.Run("get_resource", func(t *testing.T) {
		roundtrip(t, r, "get_resource",
			map[string]string{"kind": "ConfigMap", "namespace": ns, "name": "cfg"}, nil, nil)
	})
	t.Run("debug_bundle", func(t *testing.T) {
		roundtrip(t, r, "debug_bundle",
			map[string]string{"namespace": ns, "kind": "Deployment", "name": "web"}, nil, nil)
	})
}

func nodeName(t *testing.T, r *Runner) string {
	t.Helper()
	nl, err := r.dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).List(context.Background(), metav1.ListOptions{Limit: 1})
	if err != nil || len(nl.Items) == 0 {
		t.Fatalf("list nodes: %v", err)
	}
	return nl.Items[0].GetName()
}

func hasTaint(node map[string]any, key string) bool {
	ts, _ := nestedGet(node, []string{"spec", "taints"})
	arr, _ := ts.([]any)
	for _, e := range arr {
		if m, _ := e.(map[string]any); m["key"] == key {
			return true
		}
	}
	return false
}

func containerImage(dep map[string]any, name string) string {
	cs, _ := nestedGet(dep, []string{"spec", "template", "spec", "containers"})
	arr, _ := cs.([]any)
	for _, c := range arr {
		if m, _ := c.(map[string]any); m["name"] == name {
			s, _ := m["image"].(string)
			return s
		}
	}
	return ""
}

func hasEnv(dep map[string]any, container, env string) bool {
	cs, _ := nestedGet(dep, []string{"spec", "template", "spec", "containers"})
	arr, _ := cs.([]any)
	for _, c := range arr {
		m, _ := c.(map[string]any)
		if m["name"] != container {
			continue
		}
		envs, _ := m["env"].([]any)
		for _, e := range envs {
			if em, _ := e.(map[string]any); em["name"] == env {
				return true
			}
		}
	}
	return false
}
