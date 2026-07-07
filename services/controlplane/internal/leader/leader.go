// Package leader gated Singleton-Hintergrundarbeit (Alert-Evaluator,
// Action-Reaper) auf genau EINE Control-Plane-Replica — über einen Postgres-
// Session-Advisory-Lock. Kein zusätzliches Infrastruktur-Teil: die DB, die
// ohnehin die Wahrheit hält, ist auch der Schiedsrichter. Verliert der Leader
// seine DB-Verbindung, endet die Session, der Lock fällt, eine andere Replica
// übernimmt; die verwaiste Arbeit wird über den abgebrochenen Context beendet.
package leader

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	retryInterval = 15 * time.Second // Nicht-Leader: so oft wird der Lock probiert
	pingInterval  = 10 * time.Second // Leader: Health-Check der Lock-Verbindung
)

// Run versucht dauerhaft, den Advisory-Lock `key` zu halten, und führt `lead`
// aus, solange die Führung besteht. Blockiert bis ctx endet. `name` ist nur
// fürs Log (z.B. "alerts+reaper").
func Run(ctx context.Context, pool *pgxpool.Pool, name string, key int64, lead func(ctx context.Context)) {
	for ctx.Err() == nil {
		if !attempt(ctx, pool, name, key, lead) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryInterval):
			}
		}
	}
}

// attempt holt eine dedizierte Verbindung, versucht den Lock und führt bei
// Erfolg lead aus, bis die Verbindung stirbt oder ctx endet. Rückgabe true =
// wir WAREN Leader (sofort erneut versuchen), false = Lock war belegt.
func attempt(ctx context.Context, pool *pgxpool.Pool, name string, key int64, lead func(ctx context.Context)) bool {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false
	}
	// Die Lock-Conn wird am Ende ZERSTÖRT statt in den Pool zurückgelegt:
	// der Advisory-Lock ist session-gebunden — eine zurückgelegte Conn würde
	// den Lock unsichtbar weiterhalten und niemand könnte je wieder führen.
	defer func() {
		_ = conn.Conn().Close(context.Background())
		conn.Release()
	}()

	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&got); err != nil || !got {
		return false
	}
	log.Printf("leader: acquired %s", name)

	leadCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		lead(leadCtx)
		close(done)
	}()

	// Führung hält, solange die Lock-Verbindung lebt.
	tick := time.NewTicker(pingInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return true
		case <-done: // lead beendete sich selbst (sollte nur bei ctx-Ende passieren)
			cancel()
			return true
		case <-tick.C:
			pingCtx, pcancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			pcancel()
			if err != nil {
				log.Printf("leader: lost %s (lock connection: %v)", name, err)
				cancel()
				<-done
				return true
			}
		}
	}
}
