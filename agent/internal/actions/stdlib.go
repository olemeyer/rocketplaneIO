package actions

// stdlib.go — the embedded built-in action scripts (snapshot substrate). Each
// built-in kind is a real .star file that snapshots what it touches + mutates
// over the generic primitives; a fork is a byte-copy of it. This is the honest
// "shown == executed, built-in == custom" surface. Dispatch is behind a flag
// (RP_ACTIONS_SNAPSHOT=1) so the released plan()/revert.go path is untouched
// until each kind's parity is proven and flipped.

import (
	"embed"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// actionsSnapshotDispatch routes built-in kinds to their embedded .star (the
// snapshot substrate) instead of the native plan(). ON by default — the
// substrate IS the execution path now, so what the UI shows is what runs. The
// escape hatch RP_ACTIONS_SNAPSHOT=0 falls back to native plan() for a rollback.
var actionsSnapshotDispatch = os.Getenv("RP_ACTIONS_SNAPSHOT") != "0"

//go:embed stdlib/*.star
var stdlibFS embed.FS

var stdlibScripts = map[string]string{}

func init() {
	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return
	}
	for _, e := range entries {
		data, err := stdlibFS.ReadFile("stdlib/" + e.Name())
		if err == nil {
			stdlibScripts[strings.TrimSuffix(e.Name(), ".star")] = string(data)
		}
	}
}

// builtinSnapshotScript returns the embedded .star for a built-in kind.
func builtinSnapshotScript(kind string) (string, bool) {
	s, ok := stdlibScripts[kind]
	return s, ok
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
	return args
}
