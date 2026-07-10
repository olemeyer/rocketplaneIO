package recipe

// stdlib.go embeds the built-in recipes and parses them once. A built-in and a
// user fork resolve through the same Parse/Validate path, so the built-ins are
// themselves a proof the guarantees hold: if scale.yaml ever violated the
// required-compensation or additive-verify rules, this package would fail to
// load (and its test would go red).

import (
	"embed"
	"fmt"
	"sync"
)

//go:embed stdlib/*.yaml
var stdlibFS embed.FS

var (
	stdlibOnce sync.Once
	stdlib     map[string]*Manifest
	stdlibErr  error
)

func loadStdlib() {
	stdlib = map[string]*Manifest{}
	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		stdlibErr = err
		return
	}
	for _, e := range entries {
		data, err := stdlibFS.ReadFile("stdlib/" + e.Name())
		if err != nil {
			stdlibErr = err
			return
		}
		m, err := Parse(data)
		if err != nil {
			stdlibErr = fmt.Errorf("stdlib/%s: %w", e.Name(), err)
			return
		}
		if _, dup := stdlib[m.Recipe]; dup {
			stdlibErr = fmt.Errorf("stdlib: duplicate recipe %q", m.Recipe)
			return
		}
		stdlib[m.Recipe] = m
	}
}

// Builtin returns the parsed built-in manifest for a recipe key. ok=false means
// no built-in of that kind (the caller falls back to the native plan()).
func Builtin(recipe string) (m *Manifest, ok bool, err error) {
	stdlibOnce.Do(loadStdlib)
	if stdlibErr != nil {
		return nil, false, stdlibErr
	}
	m, ok = stdlib[recipe]
	return m, ok, nil
}
