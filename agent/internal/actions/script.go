package actions

// script.go — shared Starlark language options + interpreter budget for the
// snapshot execution surface (script_snapshot.go), which is the SINGLE script
// execution path: built-in kinds, their forks, and custom workflows all run there.
// The former raw custom-script engine (executeScript + rawBuiltins) was removed
// once the snapshot substrate became the default and only path.

import (
	"time"

	"go.starlark.net/syntax"
)

const (
	scriptTimeout  = 10 * time.Minute // scripts orchestrate several rollouts
	maxScriptSteps = 40               // timeline upper bound (UI + sanity)
	maxSleep       = 30 * time.Second
	// Starlark interpreter budget: prevents hot loops (for over large ranges) —
	// cluster waits run in Go builtins and cost almost no budget.
	maxExecutionSteps = 5_000_000
)

// scriptFileOptions: hermetic language options — sets yes, while/recursion NO
// (termination guarantee), top-level control flow for ergonomic workflows. Shared
// by the agent's execution surface and the control-plane's save-time compile.
func scriptFileOptions() *syntax.FileOptions {
	return &syntax.FileOptions{
		Set:             true,
		GlobalReassign:  true,
		TopLevelControl: true,
		While:           false,
		Recursion:       false,
	}
}
