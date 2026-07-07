// Package events ist der Event-Bus der Control-Plane: Agent-Pushes und
// Action-Statuswechsel werden als INVALIDATION-SIGNALE an SSE-Subscriber
// verteilt — Browser UND Agent hängen am selben Broker. Der Browser refetcht
// auf ein Signal hin gezielt seine Query, der Agent claimt auf `dispatch` hin
// pending Actions, statt zu pollen. Signale tragen bewusst KEINE Daten (klein,
// kein Auth-Risiko, kein Schema-Drift zwischen Stream und REST); einzige
// Ausnahme ist `cancel` mit der actionId — ohne sie wüsste der Agent nicht,
// welchen laufenden Ablauf er abbrechen soll.
//
// Multi-Replica: Publish geht über Postgres NOTIFY, die Zustellung an lokale
// Subscriber AUSSCHLIESSLICH über den LISTEN-Loop (Run) — so sieht jede
// CP-Replica jedes Signal genau einmal, egal wo es publiziert wurde. Fällt
// NOTIFY aus (kein Pool im Test, DB-Hänger), stellt Publish direkt lokal zu:
// eine einzelne Replica bleibt damit voll funktional, nur Cross-Replica-
// Zustellung entfällt — und der Agent hat ohnehin einen Fallback-Poll.
package events

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyChannel ist der eine Postgres-NOTIFY-Kanal aller Replicas; die
// Cluster-Zuordnung steckt im Payload (NOTIFY-Payload-Limit 8000 Bytes ist
// für Invalidation-Signale irrelevant).
const notifyChannel = "rp_events"

// Event ist ein Invalidation-Signal für einen Cluster-Scope.
type Event struct {
	Type string // topology | actions | logs | namespaces | dispatch | cancel
	Data string // optionales JSON-Payload (nur cancel: {"actionId":…}); "" → {}
}

// wireEvent ist die NOTIFY-Serialisierung (kompakte Keys — Payload bleibt klein).
type wireEvent struct {
	Cluster string `json:"c"`
	Type    string `json:"t"`
	Data    string `json:"d,omitempty"`
}

type subscriber struct {
	ch chan Event
}

// Broker verteilt Events je Cluster an alle Subscriber (über alle Replicas).
type Broker struct {
	pool *pgxpool.Pool // nil = reiner In-Memory-Modus (Tests)

	mu   sync.RWMutex
	subs map[uuid.UUID]map[*subscriber]struct{}
	// lastSent drosselt hochfrequente Topics (z.B. logs bei jedem Batch) —
	// pro Replica VOR dem NOTIFY, damit kein NOTIFY-Sturm entsteht.
	lastSent map[string]time.Time
}

func NewBroker(pool *pgxpool.Pool) *Broker {
	return &Broker{
		pool:     pool,
		subs:     map[uuid.UUID]map[*subscriber]struct{}{},
		lastSent: map[string]time.Time{},
	}
}

// Run hält die LISTEN-Verbindung und verteilt eingehende NOTIFYs an die
// lokalen Subscriber. Blockiert bis ctx endet; Verbindungsabbrüche werden mit
// Backoff neu aufgebaut (verpasste Signale sind verkraftbar — Clients
// refetchen Gesamtzustand, der Agent pollt als Fallback).
func (b *Broker) Run(ctx context.Context) {
	if b.pool == nil {
		return
	}
	for ctx.Err() == nil {
		if err := b.listenOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("events: listen loop: %v (reconnecting)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (b *Broker) listenOnce(ctx context.Context) error {
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// Die Conn wird exklusiv zum Lauschen benutzt und am Ende ZERSTÖRT (nicht
	// in den Pool zurückgelegt) — eine Conn mit aktivem LISTEN im Pool würde
	// spätere Queries mit Notifications verwirren.
	defer func() {
		_ = conn.Conn().Close(context.Background())
		conn.Release()
	}()

	if _, err := conn.Exec(ctx, "LISTEN "+notifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var w wireEvent
		if err := json.Unmarshal([]byte(n.Payload), &w); err != nil {
			continue
		}
		id, err := uuid.Parse(w.Cluster)
		if err != nil {
			continue
		}
		b.deliverLocal(id, Event{Type: w.Type, Data: w.Data})
	}
}

// Subscribe registriert einen Stream für clusterID. cancel MUSS aufgerufen
// werden (defer im Handler), sonst leakt der Subscriber.
func (b *Broker) Subscribe(clusterID uuid.UUID) (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, 16)}
	b.mu.Lock()
	if b.subs[clusterID] == nil {
		b.subs[clusterID] = map[*subscriber]struct{}{}
	}
	b.subs[clusterID][s] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if set, ok := b.subs[clusterID]; ok {
			delete(set, s)
			if len(set) == 0 {
				delete(b.subs, clusterID)
			}
		}
		b.mu.Unlock()
	}
	return s.ch, cancel
}

// Publish sendet ein Signal an alle Subscriber des Clusters — replicaweit via
// NOTIFY. minGap > 0 drosselt das Topic (weitere Signale im Fenster werden
// verworfen — der Client refetcht ohnehin den Gesamtzustand).
func (b *Broker) Publish(clusterID uuid.UUID, eventType string, minGap time.Duration) {
	b.publish(clusterID, Event{Type: eventType}, minGap)
}

// PublishData sendet ein Signal MIT Payload — ungedrosselt, weil Daten-Events
// (cancel) nie verworfen werden dürfen.
func (b *Broker) PublishData(clusterID uuid.UUID, eventType, data string) {
	b.publish(clusterID, Event{Type: eventType, Data: data}, 0)
}

func (b *Broker) publish(clusterID uuid.UUID, ev Event, minGap time.Duration) {
	key := clusterID.String() + "/" + ev.Type
	b.mu.Lock()
	if minGap > 0 {
		if t, ok := b.lastSent[key]; ok && time.Since(t) < minGap {
			b.mu.Unlock()
			return
		}
	}
	b.lastSent[key] = time.Now()
	b.mu.Unlock()

	if b.pool != nil {
		payload, _ := json.Marshal(wireEvent{Cluster: clusterID.String(), Type: ev.Type, Data: ev.Data})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := b.pool.Exec(ctx, "SELECT pg_notify($1, $2)", notifyChannel, string(payload))
		cancel()
		if err == nil {
			return // Zustellung (auch lokal) übernimmt der LISTEN-Loop
		}
		log.Printf("events: pg_notify failed (delivering locally only): %v", err)
	}
	// Fallback ohne Pool / bei NOTIFY-Fehler: direkt lokal zustellen.
	b.deliverLocal(clusterID, ev)
}

// deliverLocal stellt ein Event den Subscribern DIESER Replica zu.
func (b *Broker) deliverLocal(clusterID uuid.UUID, ev Event) {
	b.mu.RLock()
	targets := make([]*subscriber, 0, len(b.subs[clusterID]))
	for s := range b.subs[clusterID] {
		targets = append(targets, s)
	}
	b.mu.RUnlock()

	for _, s := range targets {
		select {
		case s.ch <- ev:
		default: // langsamer Client: Signal verwerfen — das nächste kommt
		}
	}
}
