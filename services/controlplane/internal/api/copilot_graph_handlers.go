package api

// copilot_graph_handlers.go — Read-API des Investigation-Graphs fürs UI
// (Reload/Resume): alle Knoten eines Chats in seq-Reihenfolge.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// handleGetInvestigationGraph — GET …/copilot/chats/{chat}/graph
func (s *Server) handleGetInvestigationGraph(w http.ResponseWriter, r *http.Request) {
	clusterID, _, ok := s.copilotChatScope(w, r)
	if !ok {
		return
	}
	chatID, err := uuid.Parse(r.PathValue("chat"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chat id")
		return
	}
	invID, err := s.store.InvestigationForChat(r.Context(), chatID, clusterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"nodes": []any{}})
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load investigation")
		return
	}
	nodes, err := s.store.ListNodes(r.Context(), invID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load nodes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"investigationId": invID, "nodes": nodes})
}
