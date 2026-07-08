package api

import (
	"fmt"
	"strconv"
	"strings"
)

// copilot_policy.go — Risk-Level je Action + Namespace-Scope-Enforcement.
// Der Level ist die EINE Wahrheit (Backend), aus der die UI Farbe + Approval-
// Modus ableitet. Scope-Enforcement ist ein HARTES Gate: eine Action kann nie
// ausserhalb des gewählten Namespaces laufen, egal was das LLM vorschlägt.

// actionLevel klassifiziert eine Action nach Blast-Radius. Für ein paar Kinds
// hängt der Level an den Params (scale-to-0, node_taint NoExecute) — so wie ein
// Admin es einschätzt.
func actionLevel(kind string, params map[string]any) string {
	switch kind {
	case "debug_bundle", "pod_events", "rollout_history", "drain_preview",
		"get_resource", "describe_resource", "get_secret", "helm_releases",
		"pod_logs", "list_events", "net_probe":
		return "read"
	case "exec_readonly":
		// liest nur (Whitelist-Kommandos), betritt aber einen laufenden Container —
		// das ist ein Eingriff in die Blackbox, kein stiller Read.
		return "disruptive"
	case "patch_secret", "pdb_set":
		return "reversible"
	case "delete_job":
		return "disruptive"
	case "patch_resource", "create_configmap", "delete_configmap", "pvc_expand", "restore_resource", "script", "run_debug_pod":
		return "destructive" // run_debug_pod: beliebiges Image+Kommando, wenn auch isoliert + auto-cleanup
	case "scale":
		// Fail-closed: replicas nicht als >0-Int parsebar (z.B. String "0",
		// fehlend) → destructive, damit ein scale-to-0 nie unter ein weicheres
		// Gate rutscht.
		n, ok := toIntVal(params["replicas"])
		if !ok || n == 0 {
			return "destructive"
		}
		return "reversible"
	case "node_taint":
		if e, _ := params["effect"].(string); e == "NoExecute" {
			return "destructive" // evictet laufende Pods
		}
		return "reversible"
	case "rollout_restart", "rollout_pause", "rollout_resume", "rollout_to_revision",
		"set_image", "set_env", "set_resources", "hpa_set", "hpa_toggle", "cordon", "uncordon",
		"cronjob_suspend", "cronjob_resume", "node_untaint", "statefulset_partition",
		"annotate", "set_label", "patch_configmap":
		return "reversible"
	case "drain", "expand_pvc":
		return "destructive" // node leeren / PVC nicht schrumpfbar
	case "delete_pod", "evict_pod", "rollout_undo", "cleanup_pods", "cleanup_jobs", "cronjob_trigger":
		return "disruptive"
	}
	// Fail-closed default: ein neuer, noch nicht klassifizierter Kind läuft nie
	// still unter dem schwächsten Gate — er verlangt die strengste Freigabe.
	return "destructive"
}

// actionCategory ordnet jeden Kind einer Katalog-Kategorie zu (UI-Gruppierung,
// Doku). Eine Quelle der Wahrheit — neue Kinds hier MIT eintragen.
func actionCategory(kind string) string {
	switch kind {
	case "debug_bundle", "pod_events", "rollout_history", "drain_preview",
		"get_resource", "describe_resource", "get_secret", "helm_releases", "exec_readonly",
		"pod_logs", "list_events", "run_debug_pod", "net_probe":
		return "diagnose"
	case "rollout_restart", "rollout_undo", "rollout_pause", "rollout_resume",
		"rollout_to_revision", "set_image", "statefulset_partition", "delete_pod", "evict_pod":
		return "workloads"
	case "scale", "hpa_set", "hpa_toggle":
		return "scaling"
	case "patch_configmap", "patch_secret", "create_configmap", "delete_configmap",
		"set_env", "set_resources", "annotate", "set_label":
		return "config"
	case "pdb_set", "patch_resource":
		return "network-policy"
	case "pvc_expand":
		return "storage"
	case "cordon", "uncordon", "drain", "node_taint", "node_untaint":
		return "nodes"
	case "cronjob_trigger", "cronjob_suspend", "cronjob_resume", "delete_job":
		return "batch"
	case "cleanup_pods", "cleanup_jobs":
		return "cleanup"
	case "script", "restore_resource":
		return "workflows"
	}
	return "other"
}

// toIntVal akzeptiert float64/int UND numerische Strings ("0") — sonst würde ein
// string-typisiertes replicas die scale-Klassifizierung unterlaufen.
func toIntVal(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}

// scopeViolation prüft, ob eine Action ausserhalb des aktiven Namespace-Scopes
// liegt. scope == "" heisst „alle Namespaces" (kein Gate). Rückgabe != "" = Grund
// der Ablehnung.
func scopeViolation(scope, targetNamespace, targetKind, targetName string) string {
	if scope == "" {
		return ""
	}
	// Cluster-scoped (Node) unter einem Namespace-Scope nicht erlaubt.
	// Case-insensitive: die Whitelist ist case-sensitive, aber das Scope-Gate
	// darf sich nicht darauf verlassen — "node" darf nicht durchrutschen.
	if strings.EqualFold(targetKind, "Node") || targetNamespace == "-" {
		return fmt.Sprintf("blocked by scope: a single namespace (%q) is selected — switch the scope to 'all namespaces' to act on nodes/cluster-scoped objects", scope)
	}
	ns := targetNamespace
	if strings.EqualFold(targetKind, "Namespace") { // cleanup_pods / set_label auf Namespace: TargetName = ns
		ns = targetName
	}
	if ns != scope {
		return fmt.Sprintf("blocked by scope: the active scope is namespace %q but this action targets %q", scope, ns)
	}
	return ""
}

// scopeSystemNote hängt dem System-Prompt den aktiven Namespace-Scope an, damit
// das LLM gar nicht erst ausserhalb investigiert/vorschlägt.
func scopeSystemNote(scope string) string {
	if scope == "" {
		return "\n\n# Active scope\nAll namespaces are in scope. get_service_map / get_infra give the cluster-wide overview."
	}
	return fmt.Sprintf("\n\n# Active scope — namespace %q ONLY\nThe human has scoped this session to the namespace %q. Investigate and act ONLY within it: pass namespace=%q to query_logs / query_traces, and NEVER propose an action on another namespace or on a cluster-scoped object (a Node) — those are hard-blocked and will fail. If a fix truly needs a node or another namespace, say so and tell the human to switch the scope to 'all namespaces'.", scope, scope, scope)
}
