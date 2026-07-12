package actions

// stdlib.go — the embedded built-in action scripts (snapshot substrate). Each
// built-in kind is a real .star file that snapshots what it touches + mutates
// over the generic primitives; a fork is a byte-copy of it. This is the honest
// "shown == executed, built-in == custom" surface, and the ONLY script execution
// path — built-in kinds and custom workflows both run here. The 6 host-capability
// kinds (drain/evict_pod/exec_readonly/net_probe/pod_logs/run_debug_pod) have no
// script and fall through to the native plan().

import (
	"embed"
	"encoding/json"
	"strconv"
	"strings"
)

//go:embed stdlib/*.star
var stdlibFS embed.FS

var stdlibScripts = map[string]string{}

// stdlibReversible[kind] is the script's `# @reversible` front-matter
// (snapshot|readonly|none). "none" kinds do not durably report snapshots and are
// not auto-rolled-back — so the UI never offers a revert that would fail or add
// garbage (a cleanup recreating terminal pods, a PVC that cannot shrink).
var stdlibReversible = map[string]string{}

func init() {
	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return
	}
	for _, e := range entries {
		data, err := stdlibFS.ReadFile("stdlib/" + e.Name())
		if err != nil {
			continue
		}
		kind := strings.TrimSuffix(e.Name(), ".star")
		src := string(data)
		stdlibScripts[kind] = src
		stdlibReversible[kind] = parseReversible(src)
	}
}

// parseReversible extracts the `# @reversible <value>` front-matter line.
func parseReversible(src string) string {
	for _, ln := range strings.Split(src, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "# @reversible ") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "# @reversible "))
		}
	}
	return "snapshot" // default: capture + rollback + revertible
}

// builtinSnapshotScript returns the embedded .star for a built-in kind.
func builtinSnapshotScript(kind string) (string, bool) {
	s, ok := stdlibScripts[kind]
	return s, ok
}

// builtinReversible reports whether a built-in kind's captures should be durably
// reported + auto-rolled-back (true) or discarded (false, for @reversible none).
func builtinReversible(kind string) bool {
	return stdlibReversible[kind] != "none"
}

// snapshotArgs flattens an action's target + typed params into the string `args`
// dict a stdlib script reads (all values are strings — scripts cast with int()).
func snapshotArgs(a Action) map[string]string {
	args := map[string]string{}
	var p map[string]any
	if len(a.Params) > 0 {
		_ = json.Unmarshal(a.Params, &p)
	}
	for k, v := range p {
		switch x := v.(type) {
		case string:
			args[k] = x
		case float64:
			args[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			args[k] = strconv.FormatBool(x)
		case nil:
			// skip
		default:
			b, _ := json.Marshal(v)
			args[k] = string(b)
		}
	}
	// Target coordinates win over any param of the same name — a param must never
	// clobber the resource the action targets (e.g. set_env's env-var name).
	args["namespace"] = a.TargetNamespace
	args["kind"] = a.TargetKind
	args["name"] = a.TargetName
	// The action ID lets a script build a deterministic per-run resource name
	// (run_debug_pod names its probe pod rp-debug-<id> so cleanup finds it again).
	args["id"] = a.ID
	return args
}
