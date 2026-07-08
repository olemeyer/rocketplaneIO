package api

// copilot_secrets.go — In-Memory-Vault für Secret-Werte eines Copilot-Runs.
// Quellen: ask_user (kind=secret) und get_secret (reveal). Das LLM sieht
// ausschließlich Platzhalter {{secret:<token>}}; der Klartext wird NUR
// serverseitig aufgelöst (Action-Params nach Approval, base64-Utility) und
// weder geloggt noch in Chat-Historie oder SSE-Events geschrieben.

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sync"
	"time"
)

const vaultTTL = 45 * time.Minute

type vaultEntry struct {
	values  map[string]string // token → Klartext
	touched time.Time
}

var runSecretsVault = struct {
	mu sync.Mutex
	m  map[string]*vaultEntry // runID → Einträge
}{m: map[string]*vaultEntry{}}

var secretPlaceholderRe = regexp.MustCompile(`\{\{secret:([a-f0-9]{24})\}\}`)

// vaultPut legt einen Klartext-Wert ab und liefert den Platzhalter fürs LLM.
func vaultPut(runID, value string) string {
	tok := make([]byte, 12)
	_, _ = rand.Read(tok)
	token := hex.EncodeToString(tok)

	runSecretsVault.mu.Lock()
	defer runSecretsVault.mu.Unlock()
	vaultGC()
	e := runSecretsVault.m[runID]
	if e == nil {
		e = &vaultEntry{values: map[string]string{}}
		runSecretsVault.m[runID] = e
	}
	e.values[token] = value
	e.touched = time.Now()
	return "{{secret:" + token + "}}"
}

// vaultResolve ersetzt alle Platzhalter dieses Runs durch Klartext. Unbekannte
// Tokens bleiben stehen (fail-closed: es entsteht nie fremder Klartext).
func vaultResolve(runID, s string) string {
	runSecretsVault.mu.Lock()
	e := runSecretsVault.m[runID]
	if e != nil {
		e.touched = time.Now()
	}
	runSecretsVault.mu.Unlock()
	if e == nil {
		return s
	}
	return secretPlaceholderRe.ReplaceAllStringFunc(s, func(m string) string {
		token := secretPlaceholderRe.FindStringSubmatch(m)[1]
		runSecretsVault.mu.Lock()
		v, ok := e.values[token]
		runSecretsVault.mu.Unlock()
		if !ok {
			return m
		}
		return v
	})
}

// vaultHasPlaceholder meldet, ob s (mindestens) einen Platzhalter enthält —
// fürs UI-Flag „enthält Secret".
func vaultHasPlaceholder(s string) bool { return secretPlaceholderRe.MatchString(s) }

// vaultGC räumt abgelaufene Runs auf (Aufrufer hält den Lock).
func vaultGC() {
	cut := time.Now().Add(-vaultTTL)
	for id, e := range runSecretsVault.m {
		if e.touched.Before(cut) {
			for k := range e.values {
				delete(e.values, k) // Klartext aktiv überschreiben lassen (GC)
			}
			delete(runSecretsVault.m, id)
		}
	}
}
