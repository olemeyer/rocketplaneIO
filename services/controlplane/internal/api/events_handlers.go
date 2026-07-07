package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/events"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// events_handlers.go — die SSE-Streams je Cluster: ersetzen Polling durch
// Invalidation-Signale. Zwei Consumer, ein Broker:
//   Browser (Session): topology/actions/logs/namespaces/alerts → Query-Refetch.
//   Agent   (Bearer):  dispatch (pending claimen) + cancel (laufenden Ablauf
//                      sofort abbrechen) — der outbound-only-Rückkanal in
//                      Push-Latenz statt Poll-Intervall.
// Ein Heartbeat-Kommentar alle 25s hält Proxies/LBs auf der Leitung.

func (s *Server) handleClusterEvents(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "cluster not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load cluster")
		return
	}

	s.streamEvents(w, r, clusterID, nil)
}

// handleAgentEvents — GET /api/agent/events (Bearer): der Live-Kanal zum
// Agenten. Er bekommt NUR agent-relevante Signale (dispatch/cancel) — die
// UI-Topics würden bei jedem eigenen Push/Report sinnlos zurückschallen.
func (s *Server) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := auth.ClusterIDFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no cluster in context")
		return
	}
	s.streamEvents(w, r, clusterID, map[string]bool{"dispatch": true, "cancel": true})
}

// streamEvents fährt einen SSE-Stream über den Broker; only != nil filtert
// auf die genannten Event-Typen.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, only map[string]bool) {
	flusher, okF := w.(http.Flusher)
	if !okF {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: nicht puffern
	w.WriteHeader(http.StatusOK)

	// Hello — der Client weiß, dass der Stream lebt (schaltet Poll-Fallback ab).
	fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	ch, cancel := s.broker.Subscribe(clusterID)
	defer cancel()

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.shutdownCh:
			// Graceful Shutdown: Streams sofort beenden, sonst hängt
			// http.Server.Shutdown bis zum Timeout (Clients reconnecten).
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-ch:
			if only != nil && !only[ev.Type] {
				continue
			}
			writeEvent(w, ev)
			flusher.Flush()
		}
	}
}

func writeEvent(w http.ResponseWriter, ev events.Event) {
	data := ev.Data
	if data == "" {
		data = "{}"
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
}
