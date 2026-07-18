package api

import (
	"encoding/json"
	"strconv"
	"strings"
)

// actions_policy.go — risk classification for the GENERIC operation set. The
// action catalog is gone: agents (via MCP) and the system (alert remediation)
// express arbitrary operations through a handful of kubectl-shaped primitives,
// and rocketplaneIO classifies each one. The level is THE single source of
// truth from which RBAC, the approval gate and the UI derive behavior.
//
// Operation kinds:
//   k8s_get, k8s_list           — reads (any resource incl. CRDs)
//   k8s_patch, k8s_apply        — mutations with snapshot capture (reversible)
//   k8s_delete                  — delete with whole-object capture
//   k8s_exec                    — arbitrary command in a container (NOT reversible)
//   script                      — ad-hoc/defined Starlark workflow
//   snapshot_restore            — the generic rollback run (system-created)

// operationKinds is the closed set of kinds the control plane accepts.
var operationKinds = map[string]bool{
	"k8s_get":    true,
	"k8s_list":   true,
	"k8s_patch":  true,
	"k8s_apply":  true,
	"k8s_delete": true,
	"k8s_exec":   true,
	"script":     true,
}

// actionLevel classifies an operation by blast radius, the way an admin would
// judge it. Fail-closed: anything unknown or unparseable is destructive.
func actionLevel(kind string, targetKind string, params map[string]any) string {
	switch kind {
	case "k8s_get", "k8s_list":
		return "read"
	case "k8s_patch", "k8s_apply":
		// Snapshot capture makes these restorable — reversible by construction.
		// Exception, fail-closed: anything that sets spec.replicas to 0 takes a
		// workload offline and never slips under a softer gate.
		if payloadScalesToZero(params) {
			return "destructive"
		}
		return "reversible"
	case "k8s_delete":
		// Pods/Jobs are routinely recreated by their controllers — disruptive.
		// Deleting anything else (workloads, namespaces, PVCs, CRs, …) is
		// destructive even though the capture can recreate the object: identity,
		// volumes and child resources may not survive the round trip.
		switch targetKind {
		case "Pod", "Job":
			return "disruptive"
		}
		return "destructive"
	case "k8s_exec":
		// Arbitrary command in a running container — not snapshotable, not
		// reversible. Always behind the strictest gate.
		return "destructive"
	case "script":
		return "destructive"
	}
	// Fail-closed default: a new, not-yet-classified kind never runs silently
	// under the weakest gate.
	return "destructive"
}

// payloadScalesToZero detects spec.replicas == 0 in a patch or manifest params
// payload (both carry the JSON document under "patch" resp. "manifest").
func payloadScalesToZero(params map[string]any) bool {
	for _, key := range []string{"patch", "manifest"} {
		raw, ok := params[key]
		if !ok {
			continue
		}
		var doc map[string]any
		switch v := raw.(type) {
		case string:
			if json.Unmarshal([]byte(v), &doc) != nil {
				return true // unparseable mutation payload → fail closed
			}
		case map[string]any:
			doc = v
		default:
			return true
		}
		if spec, ok := doc["spec"].(map[string]any); ok {
			if reps, present := spec["replicas"]; present {
				if n, ok := toIntVal(reps); !ok || n == 0 {
					return true
				}
			}
		}
	}
	return false
}

// toIntVal accepts float64/int AND numeric strings ("0") — otherwise a
// string-typed replicas value would undermine the classification.
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
