// Package events ist der In-Process-Event-Bus der Control-Plane: Agent-Pushes
// und Action-Statuswechsel werden als INVALIDATION-SIGNALE an SSE-Subscriber
// (Browser) verteilt — der Client refetcht daraufhin gezielt seine Query,
// statt blind zu pollen. Signale tragen bewusst KEINE Daten (klein, kein
// Auth-Risiko, kein Schema-Drift zwischen Stream und REST).
//
// Skalierung: eine CP-Instanz = in-memory. Für Multi-Replica wird Publish
// durch Postgres LISTEN/NOTIFY ersetzt (gleiches Interface) — Subscriber
// hängen dann an der NOTIFY-Verteilung statt an der lokalen Map.
package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event ist ein Invalidation-Signal für einen Cluster-Scope.
type Event struct {
	Type string // topology | actions | logs | namespaces
}

type subscriber struct {
	ch chan Event
}

// Broker verteilt Events je Cluster an alle Subscriber.
type Broker struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[*subscriber]struct{}
	// lastSent drosselt hochfrequente Topics (z.B. logs bei jedem Batch).
	lastSent map[string]time.Time
}

func NewBroker() *Broker {
	return &Broker{
		subs:     map[uuid.UUID]map[*subscriber]struct{}{},
		lastSent: map[string]time.Time{},
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

// Publish sendet ein Signal an alle Subscriber des Clusters. minGap > 0
// drosselt das Topic (weitere Signale im Fenster werden verworfen — der
// Client refetcht ohnehin den Gesamtzustand).
func (b *Broker) Publish(clusterID uuid.UUID, eventType string, minGap time.Duration) {
	key := clusterID.String() + "/" + eventType
	b.mu.Lock()
	if minGap > 0 {
		if t, ok := b.lastSent[key]; ok && time.Since(t) < minGap {
			b.mu.Unlock()
			return
		}
	}
	b.lastSent[key] = time.Now()
	// Snapshot der Subscriber unter Lock, Senden non-blocking.
	targets := make([]*subscriber, 0, len(b.subs[clusterID]))
	for s := range b.subs[clusterID] {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	ev := Event{Type: eventType}
	for _, s := range targets {
		select {
		case s.ch <- ev:
		default: // langsamer Client: Signal verwerfen — das nächste kommt
		}
	}
}
