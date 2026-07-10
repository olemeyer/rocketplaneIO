// Command genmanifests emits the stdlib Effect manifests for every built-in
// action kind. It is the single source of truth for each kind's declared
// reversibility, risk and target surface — derived from the agent's actual
// revert.go (which kinds have an inverse) and the control-plane whitelist (which
// target kinds each acts on). Run once to (re)generate:
//
//	go run ./cmd/genmanifests
//
// The manifests it writes are validated by recipe.Parse at load and by the
// per-kind test TestEveryKindHasConsistentManifest. scale.yaml is hand-written
// (with prose) and intentionally skipped.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/rocketplaneio/rocketplane/agent/internal/actions/recipe"
)

type param struct {
	name, typ string
	required  bool
}

type meta struct {
	kind    string
	title   string
	targets []string
	// category: "read" (no mutation), "reversible" (mutation + builtin inverse),
	// "irreversible" (mutation, no clean undo).
	category string
	riskBase string // reversible | destructive | external
	riskWhen string
	riskThen string
	params   []param
}

// table is authored from actionTargetKinds (control plane) + revert.go (agent).
// reversible == there is a prepareRevert arm; irreversible == mutates but has no
// clean inverse; read == observe-only.
var table = []meta{
	// ── reversible mutations (builtin inverse) ─────────────────────────────
	{"set_image", "Set container image", wl("Deployment", "StatefulSet", "DaemonSet"), "reversible", "reversible", "", "", []param{{"image", "string", true}, {"container", "string", false}}},
	{"rollout_pause", "Pause a rollout", wl("Deployment"), "reversible", "reversible", "", "", nil},
	{"rollout_resume", "Resume a rollout", wl("Deployment"), "reversible", "reversible", "", "", nil},
	{"rollout_undo", "Undo a rollout", wl("Deployment"), "reversible", "reversible", "", "", nil},
	{"rollout_to_revision", "Roll out a specific revision", wl("Deployment"), "reversible", "reversible", "", "", []param{{"revision", "int", true}}},
	{"hpa_set", "Set HPA bounds", wl("HorizontalPodAutoscaler"), "reversible", "reversible", "", "", []param{{"minReplicas", "int", false}, {"maxReplicas", "int", false}}},
	{"hpa_toggle", "Enable/disable an HPA", wl("HorizontalPodAutoscaler"), "reversible", "reversible", "", "", []param{{"enabled", "bool", true}}},
	{"cordon", "Cordon a node", wl("Node"), "reversible", "reversible", "", "", nil},
	{"uncordon", "Uncordon a node", wl("Node"), "reversible", "reversible", "", "", nil},
	{"drain", "Drain a node", wl("Node"), "reversible", "destructive", "", "", nil},
	{"cronjob_suspend", "Suspend a CronJob", wl("CronJob"), "reversible", "reversible", "", "", nil},
	{"cronjob_resume", "Resume a CronJob", wl("CronJob"), "reversible", "reversible", "", "", nil},
	{"node_taint", "Taint a node", wl("Node"), "reversible", "reversible", "", "", []param{{"key", "string", true}, {"value", "string", false}, {"effect", "string", false}}},
	{"node_untaint", "Remove a node taint", wl("Node"), "reversible", "reversible", "", "", []param{{"key", "string", true}}},
	{"annotate", "Set an annotation", wlAnnotate(), "reversible", "reversible", "", "", []param{{"key", "string", true}, {"value", "string", false}}},
	{"set_label", "Set a label", wl("Node", "Namespace"), "reversible", "reversible", "", "", []param{{"key", "string", true}, {"value", "string", false}}},
	{"patch_configmap", "Patch a ConfigMap key", wl("ConfigMap"), "reversible", "reversible", "", "", []param{{"key", "string", true}, {"value", "string", false}}},
	{"set_env", "Set a container env var", wl("Deployment", "StatefulSet", "DaemonSet"), "reversible", "reversible", "", "", []param{{"name", "string", true}, {"value", "string", false}, {"container", "string", false}}},
	{"set_resources", "Set container resources", wl("Deployment", "StatefulSet", "DaemonSet"), "reversible", "reversible", "", "", []param{{"container", "string", false}}},
	{"statefulset_partition", "Set a StatefulSet partition", wl("StatefulSet"), "reversible", "reversible", "", "", []param{{"partition", "int", true}}},
	{"patch_secret", "Patch a Secret key", wl("Secret"), "reversible", "destructive", "", "", []param{{"key", "string", true}}},
	{"create_configmap", "Create a ConfigMap", wl("ConfigMap"), "reversible", "reversible", "", "", nil},
	{"delete_configmap", "Delete a ConfigMap", wl("ConfigMap"), "reversible", "destructive", "", "", nil},
	{"pdb_set", "Set a PodDisruptionBudget", wl("PodDisruptionBudget"), "reversible", "reversible", "", "", nil},
	{"patch_resource", "Patch a resource", wl("Service", "Ingress", "NetworkPolicy", "PodDisruptionBudget", "ResourceQuota", "LimitRange"), "reversible", "reversible", "", "", nil},
	{"restore_resource", "Restore a resource from snapshot", wl("Service", "Ingress", "NetworkPolicy", "PodDisruptionBudget", "ConfigMap", "ResourceQuota", "LimitRange"), "reversible", "reversible", "", "", nil},

	// ── irreversible mutations (no clean inverse) ─────────────────────────
	{"rollout_restart", "Restart a rollout", wl("Deployment", "StatefulSet", "DaemonSet"), "irreversible", "reversible", "", "", nil},
	{"delete_pod", "Delete a pod", wl("Pod"), "irreversible", "destructive", "", "", nil},
	{"evict_pod", "Evict a pod", wl("Pod"), "irreversible", "destructive", "", "", nil},
	{"cleanup_pods", "Clean up terminal pods", wl("Namespace"), "irreversible", "destructive", "", "", nil},
	{"cleanup_jobs", "Clean up finished jobs", wl("Namespace"), "irreversible", "destructive", "", "", nil},
	{"cronjob_trigger", "Trigger a CronJob now", wl("CronJob"), "irreversible", "reversible", "", "", nil},
	{"delete_job", "Delete a Job", wl("Job"), "irreversible", "destructive", "", "", nil},
	{"run_debug_pod", "Run an ephemeral debug pod", wl("Namespace"), "irreversible", "reversible", "", "", nil},
	{"pvc_expand", "Expand a PVC", wl("PersistentVolumeClaim"), "irreversible", "destructive", "", "", []param{{"size", "string", true}}},

	// ── read-only investigation (no mutation) ─────────────────────────────
	{"pod_events", "Read pod events", wl("Deployment", "StatefulSet", "DaemonSet", "Pod"), "read", "reversible", "", "", nil},
	{"debug_bundle", "Collect a debug bundle", wl("Deployment", "StatefulSet", "DaemonSet", "Pod"), "read", "reversible", "", "", nil},
	{"rollout_history", "Read rollout history", wl("Deployment", "StatefulSet"), "read", "reversible", "", "", nil},
	{"drain_preview", "Preview a node drain", wl("Node"), "read", "reversible", "", "", nil},
	{"get_resource", "Read a resource as YAML", nil, "read", "reversible", "", "", nil},
	{"describe_resource", "Describe a resource", nil, "read", "reversible", "", "", nil},
	{"get_secret", "Read secret keys (hashed)", wl("Secret"), "read", "reversible", "", "", nil},
	{"helm_releases", "List Helm releases", wl("Namespace"), "read", "reversible", "", "", nil},
	{"exec_readonly", "Run a read-only exec", wl("Pod"), "read", "reversible", "", "", nil},
	{"pod_logs", "Read pod logs", wl("Pod"), "read", "reversible", "", "", nil},
	{"list_events", "List namespace events", wl("Namespace"), "read", "reversible", "", "", nil},
	{"net_probe", "Probe network reachability", wl("Namespace"), "read", "external", "", "", nil},
}

func wl(t ...string) []string { return t }
func wlAnnotate() []string {
	return wl("Deployment", "StatefulSet", "DaemonSet", "Pod", "Node", "Namespace", "ConfigMap", "Service", "PersistentVolumeClaim", "CronJob", "HorizontalPodAutoscaler")
}

func stepsFor(category string) []recipe.Step {
	switch category {
	case "read":
		return []recipe.Step{{Name: "gather", Kind: "read"}}
	case "reversible":
		return []recipe.Step{{Name: "trigger", Kind: "mutate"}, {Name: "observe", Kind: "observe"}, {Name: "verify", Kind: "verify"}}
	default: // irreversible
		return []recipe.Step{{Name: "trigger", Kind: "mutate"}, {Name: "verify", Kind: "verify"}}
	}
}

func main() {
	dir := filepath.Join("internal", "actions", "recipe", "stdlib")
	if _, err := os.Stat(dir); err != nil {
		fmt.Fprintf(os.Stderr, "run from the agent/ module root: %v\n", err)
		os.Exit(1)
	}
	n := 0
	for _, m := range table {
		rev, comp := "none", "none"
		if m.category == "reversible" {
			rev, comp = "full", "builtin"
		}
		man := recipe.Manifest{
			Recipe: m.kind, Title: m.title, Targets: m.targets,
			Reversibility: rev, Compensation: comp,
			Risk:  &recipe.RiskRule{Base: m.riskBase, When: m.riskWhen, Then: m.riskThen},
			Steps: stepsFor(m.category),
		}
		for _, p := range m.params {
			man.Params = append(man.Params, recipe.ParamSpec{Name: p.name, Type: p.typ, Required: p.required})
		}
		if err := man.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "%s: invalid: %v\n", m.kind, err)
			os.Exit(1)
		}
		b, err := yaml.Marshal(man)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: marshal: %v\n", m.kind, err)
			os.Exit(1)
		}
		header := "# Generated by cmd/genmanifests — do not edit by hand.\n" +
			"# Declares " + m.kind + "'s reversibility, risk and target surface; the agent runs\n" +
			"# the audited pipeline and inverse under this contract.\n"
		out := filepath.Join(dir, m.kind+".yaml")
		if err := os.WriteFile(out, []byte(header+string(b)), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: write: %v\n", m.kind, err)
			os.Exit(1)
		}
		n++
	}
	names := make([]string, 0, len(table))
	for _, m := range table {
		names = append(names, m.kind)
	}
	sort.Strings(names)
	fmt.Printf("wrote %d manifests: %s\n", n, strings.Join(names, " "))
}
