package api

import "fmt"

// copilot_policy.go — Risk-Level je Action + Namespace-Scope-Enforcement.
// Der Level ist die EINE Wahrheit (Backend), aus der die UI Farbe + Approval-
// Modus ableitet. Scope-Enforcement ist ein HARTES Gate: eine Action kann nie
// ausserhalb des gewählten Namespaces laufen, egal was das LLM vorschlägt.

// actionLevel klassifiziert eine Action nach Blast-Radius. Für ein paar Kinds
// hängt der Level an den Params (scale-to-0, node_taint NoExecute) — so wie ein
// Admin es einschätzt.
func actionLevel(kind string, params map[string]any) string {
	switch kind {
	case "debug_bundle", "pod_events", "rollout_history", "drain_preview":
		return "read"
	case "scale":
		if n, ok := toIntVal(params["replicas"]); ok && n == 0 {
			return "destructive" // scale-to-0 = voller Ausfall des Workloads
		}
		return "reversible"
	case "node_taint":
		if e, _ := params["effect"].(string); e == "NoExecute" {
			return "destructive" // evictet laufende Pods
		}
		return "reversible"
	case "drain", "expand_pvc":
		return "destructive" // node leeren / PVC nicht schrumpfbar
	case "delete_pod", "evict_pod", "rollout_undo", "cleanup_pods", "cleanup_jobs", "cronjob_trigger":
		return "disruptive"
	}
	return "reversible"
}

func toIntVal(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
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
	if targetKind == "Node" || targetNamespace == "-" {
		return fmt.Sprintf("blocked by scope: a single namespace (%q) is selected — switch the scope to 'all namespaces' to act on nodes/cluster-scoped objects", scope)
	}
	ns := targetNamespace
	if targetKind == "Namespace" { // cleanup_pods / set_label auf Namespace: TargetName = ns
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
