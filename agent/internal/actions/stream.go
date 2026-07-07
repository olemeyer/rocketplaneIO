// stream.go — der Live-Kanal des Runners: EIN outbound SSE-Stream zur
// Control-Plane liefert dispatch- und cancel-Signale in Push-Latenz. Der Agent
// bleibt outbound-only (er ÖFFNET die Verbindung), aber statt alle 3s zu
// pollen hält er eine idle Leitung — die Grundlage, auf der viele Agenten an
// einer Control-Plane skalieren: Claim-Requests entstehen nur noch, wenn es
// tatsächlich Arbeit gibt.
//
// Robustheit: Reconnect mit exponentiellem Backoff + Jitter (kein Thundering
// Herd nach einem Control-Plane-Neustart), Watchdog gegen tote Verbindungen
// (der Server pingt alle 25s — bleibt es länger still, ist die Leitung tot),
// und ein seltener Fallback-Poll in actions.go fängt verlorene Signale ab.
package actions

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	// streamWatchdog: Server-Pings kommen alle 25s — dreimal verpasst heißt tot.
	streamWatchdog = 80 * time.Second
	// Reconnect-Backoff: 1s → 2s → … → max 30s, plus Jitter.
	streamBackoffMin = 1 * time.Second
	streamBackoffMax = 30 * time.Second
)

// streamLoop hält den SSE-Stream bis ctx abbricht: verbinden, Events
// verarbeiten, bei Abriss mit Backoff neu verbinden.
func (r *Runner) streamLoop(ctx context.Context) {
	backoff := streamBackoffMin
	for ctx.Err() == nil {
		gotHello, err := r.streamOnce(ctx)
		r.streamLive.Store(false)
		if ctx.Err() != nil {
			return
		}
		if gotHello {
			backoff = streamBackoffMin // die Verbindung stand — frisch starten
		}
		if err != nil {
			log.Printf("actions: event stream disconnected: %v (reconnect in ~%s)", err, backoff)
		}
		// Jitter: viele Agenten dürfen nach einem CP-Neustart nicht im
		// Gleichtakt wiederkommen.
		delay := backoff/2 + time.Duration(rand.Int63n(int64(backoff)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if backoff *= 2; backoff > streamBackoffMax {
			backoff = streamBackoffMax
		}
	}
}

// streamOnce fährt EINE Stream-Verbindung bis zum Abriss. Rückgabe: ob die
// Verbindung je stand (hello empfangen) — steuert den Backoff-Reset.
func (r *Runner) streamOnce(ctx context.Context) (bool, error) {
	// Eigener Kontext, damit der Watchdog die Verbindung kappen kann.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Watchdog: jede empfangene Zeile (auch Ping-Kommentare) armiert ihn neu.
	watchdog := time.AfterFunc(streamWatchdog, cancel)
	defer watchdog.Stop()

	req, err := http.NewRequestWithContext(sctx, http.MethodGet, r.baseURL+"/api/agent/events", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := r.streamHTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<14))
		return false, fmt.Errorf("control-plane returned %d", resp.StatusCode)
	}

	gotHello := false
	eventType, data := "", ""
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<16)
	for scanner.Scan() {
		watchdog.Reset(streamWatchdog)
		line := scanner.Text()
		switch {
		case line == "":
			// Leerzeile = Event komplett.
			if eventType != "" {
				r.handleStreamEvent(eventType, data, &gotHello)
			}
			eventType, data = "", ""
		case strings.HasPrefix(line, ":"):
			// Kommentar (Server-Ping) — hält nur den Watchdog frisch.
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return gotHello, err
	}
	return gotHello, fmt.Errorf("stream closed")
}

// handleStreamEvent verarbeitet ein komplettes SSE-Event.
func (r *Runner) handleStreamEvent(eventType, data string, gotHello *bool) {
	switch eventType {
	case "hello":
		*gotHello = true
		r.streamLive.Store(true)
		log.Printf("actions: event stream connected (poll fallback stretched to %s)", streamFallbackPoll)
		// Catch-up: Actions, die während des Disconnects angelegt wurden.
		r.triggerPoll()
	case "dispatch":
		r.triggerPoll()
	case "cancel":
		var p struct {
			ActionID string `json:"actionId"`
		}
		if err := json.Unmarshal([]byte(data), &p); err != nil || p.ActionID == "" {
			return
		}
		if r.cancelAction(p.ActionID) {
			log.Printf("actions: cancel signal for %s — aborting now", p.ActionID)
		}
	}
}

// triggerPoll stößt einen sofortigen Claim-Fetch an (coalesced).
func (r *Runner) triggerPoll() {
	select {
	case r.pollNow <- struct{}{}:
	default:
	}
}

/* ── Cancel-Registry: SSE-Cancel trifft laufende Abläufe sofort ─────────── */

// registerCancel meldet einen laufenden Ablauf an; fn bricht ihn ab (idempotent).
func (r *Runner) registerCancel(actionID string, fn func()) {
	r.cancelMu.Lock()
	r.cancels[actionID] = fn
	r.cancelMu.Unlock()
}

func (r *Runner) unregisterCancel(actionID string) {
	r.cancelMu.Lock()
	delete(r.cancels, actionID)
	r.cancelMu.Unlock()
}

// cancelAction feuert den registrierten Abbruch; false = Action läuft hier nicht
// (schon fertig oder nie geclaimt — dann greift der DB-seitige cancel_requested).
func (r *Runner) cancelAction(actionID string) bool {
	r.cancelMu.Lock()
	fn, ok := r.cancels[actionID]
	r.cancelMu.Unlock()
	if ok {
		fn()
	}
	return ok
}
