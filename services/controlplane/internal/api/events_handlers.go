package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// events_handlers.go — der SSE-Stream je Cluster: ersetzt das Browser-Polling
// durch Invalidation-Signale (topology/actions/logs/namespaces). Der Client
// refetcht auf ein Signal hin gezielt seine Query; ein Heartbeat-Kommentar
// alle 25s hält Proxies/LBs auf der Leitung.

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
		case <-heartbeat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: {}\n\n", ev.Type)
			flusher.Flush()
		}
	}
}
