// Package store definiert den Read-Port der Explore-API: EIN Interface, das
// heute vom deterministischen Seed erfüllt wird und später von einer
// ClickHouse-Implementierung (HTTP-Interface) als Drop-in.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
)

var (
	// ErrNotFound signalisiert einen unbekannten Trace (-> API 404 not_found).
	ErrNotFound = errors.New("store: not found")
	// ErrUnsupported markiert eine (noch) nicht implementierte Operation.
	ErrUnsupported = errors.New("store: unsupported operation")
)

// ServicesQuery bündelt Fenster + Filter für die RED-Liste.
type ServicesQuery struct {
	Start, End time.Time
	Step       time.Duration // 0 => Server wählt window/8
	Env        string        // Filter deployment.environment ("" = alle)
	Sort       string        // "rate"|"error"|"p95" (default "error")
}

// TracesQuery bündelt Filter + Cursor-Pagination für die Trace-Liste.
type TracesQuery struct {
	Service       string
	Start, End    time.Time
	Status        string  // ""|"ok"|"error"
	MinDurationMs float64 // 0 = unset
	MaxDurationMs float64 // 0 = unset
	Limit         int     // default 50, max 500
	Cursor        string  // opak, aus NextCursor
	Sort          string  // "start"|"duration" (default "start" desc)
}

// Store ist der einzige Read-Port der Explore-Domäne.
type Store interface {
	// Services liefert die RED-Health je Service über das Fenster.
	Services(ctx context.Context, q ServicesQuery) (model.ServicesResult, error)
	// Traces liefert kompakte Trace-Summaries, cursor-paginiert.
	Traces(ctx context.Context, q TracesQuery) (model.TraceList, error)
	// Trace liefert alle Spans eines Trace depth-first; unbekannt -> ErrNotFound.
	Trace(ctx context.Context, traceID string) (model.TraceDetail, error)

	// Ping prüft die Backend-Bereitschaft (Seed: nil; ClickHouse: HTTP-Ping).
	Ping(ctx context.Context) error
	// Close gibt Ressourcen frei; No-op beim Seed.
	Close() error
}
