// Package promql ist der Platzhalter für rocketplanes PromQL-über-alle-Signale-Engine.
//
// Ziel: EINE Query-Sprache (PromQL) über Metrics, Logs, Spans und Web Events,
// ausgewertet gegen ClickHouse. Aktuell nur die Typ-Oberfläche + Stubs.
package promql

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented signalisiert eine noch nicht gebaute Auswertung.
var ErrNotImplemented = errors.New("promql engine: not implemented")

// Engine wertet PromQL-Ausdrücke aus.
type Engine struct{}

// NewEngine erstellt eine Engine.
func NewEngine() *Engine { return &Engine{} }

// Result ist das (künftige) Ergebnis einer Auswertung.
type Result struct {
	Query string    `json:"query"`
	At    time.Time `json:"at"`
}

// Instant wertet einen PromQL-Ausdruck zu einem Zeitpunkt aus (Stub).
func (e *Engine) Instant(_ context.Context, _ string, _ time.Time) (*Result, error) {
	return nil, ErrNotImplemented
}

// Range wertet einen PromQL-Ausdruck über ein Zeitfenster aus (Stub).
func (e *Engine) Range(_ context.Context, _ string, _, _ time.Time, _ time.Duration) (*Result, error) {
	return nil, ErrNotImplemented
}
