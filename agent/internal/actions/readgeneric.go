package actions

// readgeneric.go — helpers shared by the snapshot surface's read/exec primitives:
// secret redaction (values never cross into script logic or the audit trail), the
// read-only exec argv whitelist, and the output cap. The read/exec ACTIONS
// themselves (get_resource, describe_resource, get_secret, helm_releases,
// exec_readonly) now run as embedded .star scripts over these primitives.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	execTimeout = 15 * time.Second
	// Output-Caps: die Ergebnisse reisen im steps-JSON des Action-Reports
	// (CP-Limit 64 KiB gesamt) — deutlich darunter bleiben.
	execOutputCap = 40 * 1024
	readOutputCap = 40 * 1024
)

// redactSecretData ersetzt Secret-Werte durch {len, sha256} — auf dem
// unstructured-Objekt (Snapshot/get_resource-Pfad).
func redactSecretData(obj map[string]any) {
	for _, field := range []string{"data", "stringData"} {
		if data, ok := obj[field].(map[string]any); ok {
			for k, v := range data {
				s, _ := v.(string)
				sum := sha256.Sum256([]byte(s))
				data[k] = fmt.Sprintf("REDACTED len=%d sha256=%s", len(s), hex.EncodeToString(sum[:8]))
			}
		}
	}
}

// execAllowed spiegelt die CP-Whitelist (Defense-in-Depth: der Agent verlässt
// sich nie allein auf die Validierung der Control-Plane).
var execAllowed = map[string]bool{
	"cat": true, "ls": true, "head": true, "tail": true, "df": true, "du": true,
	"stat": true, "wc": true, "env": true, "id": true, "uname": true, "find": true,
}

func validExecArgv(argv []string) error {
	if len(argv) == 0 || !execAllowed[argv[0]] {
		return fmt.Errorf("command not in read-only whitelist")
	}
	for _, arg := range argv {
		if strings.ContainsAny(arg, "|;&$><`\n\r") {
			return fmt.Errorf("argument contains shell metacharacters")
		}
	}
	if argv[0] == "find" {
		for _, arg := range argv[1:] {
			switch arg {
			case "-exec", "-execdir", "-delete", "-ok", "-okdir":
				return fmt.Errorf("find must not use -exec/-delete")
			}
		}
	}
	return nil
}

// limitWriter deckelt den gesammelten Output hart (Rest wird verworfen).
type limitWriter struct {
	buf *bytes.Buffer
	max int
}

func (w limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len() >= w.max {
		return len(p), nil // weiter konsumieren, nichts mehr speichern
	}
	if w.buf.Len()+len(p) > w.max {
		p = p[:w.max-w.buf.Len()]
	}
	w.buf.Write(p)
	return len(p), nil
}
