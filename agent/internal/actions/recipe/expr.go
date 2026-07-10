package recipe

// expr.go is the non-Turing risk expression layer: a single bounded comparison
// over one param — `params.<name> <op> <int>` — used by risk.when. It is
// validated at parse and evaluated at plan time. It is deliberately not a
// language: no arithmetic, calls or loops. Anything needing real logic uses the
// rp.script escape hatch (which still declares a compensation).

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var whenPattern = regexp.MustCompile(`^\s*params\.([A-Za-z_]\w*)\s*(==|!=|<=|>=|<|>)\s*(-?\d+)\s*$`)

// validateWhen checks a when-expression parses and references a known param.
func validateWhen(when string, params map[string]bool) error {
	if strings.TrimSpace(when) == "" {
		return nil
	}
	m := whenPattern.FindStringSubmatch(when)
	if m == nil {
		return fmt.Errorf("unsupported when %q (only `params.<name> <op> <int>`)", when)
	}
	if !params[m[1]] {
		return fmt.Errorf("when %q references unknown param %q", when, m[1])
	}
	return nil
}

// evalWhen evaluates a when-expression against concrete params. An empty
// expression is always true.
func evalWhen(when string, params map[string]any) (bool, error) {
	if strings.TrimSpace(when) == "" {
		return true, nil
	}
	m := whenPattern.FindStringSubmatch(when)
	if m == nil {
		return false, fmt.Errorf("unsupported when %q", when)
	}
	name, op := m[1], m[2]
	rhs, _ := strconv.ParseInt(m[3], 10, 64)
	lhs, ok := toInt(params[name])
	if !ok {
		return false, fmt.Errorf("when %q: param %q is not an integer", when, name)
	}
	switch op {
	case "==":
		return lhs == rhs, nil
	case "!=":
		return lhs != rhs, nil
	case "<":
		return lhs < rhs, nil
	case ">":
		return lhs > rhs, nil
	case "<=":
		return lhs <= rhs, nil
	case ">=":
		return lhs >= rhs, nil
	}
	return false, fmt.Errorf("bad operator %q", op)
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	}
	return 0, false
}
