package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// copilot_chats_handlers.go — gespeicherte Copilot-Vorgänge (Chats): auflisten,
// laden, upserten (Client-seitige id), löschen. Scoped auf (Cluster, User).

func (s *Server) copilotChatScope(w http.ResponseWriter, r *http.Request) (clusterID, userID uuid.UUID, ok bool) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	clusterID, ok = parseClusterID(w, r)
	if !ok {
		return
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return uuid.Nil, uuid.Nil, false
	}
	user, uok := auth.UserFrom(r.Context())
	if !uok {
		writeErr(w, http.StatusUnauthorized, "no user")
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, user.ID, true
}

func (s *Server) handleListCopilotChats(w http.ResponseWriter, r *http.Request) {
	clusterID, userID, ok := s.copilotChatScope(w, r)
	if !ok {
		return
	}
	chats, err := s.store.ListCopilotChats(r.Context(), clusterID, userID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list chats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": chats})
}

func (s *Server) handleGetCopilotChat(w http.ResponseWriter, r *http.Request) {
	clusterID, userID, ok := s.copilotChatScope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("chat"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chat id")
		return
	}
	chat, err := s.store.GetCopilotChat(r.Context(), id, clusterID, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "chat not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load chat")
		return
	}
	writeJSON(w, http.StatusOK, chat)
}

func (s *Server) handleUpsertCopilotChat(w http.ResponseWriter, r *http.Request) {
	clusterID, userID, ok := s.copilotChatScope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("chat"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chat id")
		return
	}
	var req struct {
		Title   string          `json:"title"`
		Summary string          `json:"summary"`
		Data    json.RawMessage `json:"data"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Title) > 200 {
		req.Title = req.Title[:200]
	}
	if len(req.Summary) > 400 {
		req.Summary = req.Summary[:400]
	}
	chat, err := s.store.UpsertCopilotChat(r.Context(), id, clusterID, userID, req.Title, req.Summary, req.Data)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusForbidden, "not your chat")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to save chat")
		return
	}
	writeJSON(w, http.StatusOK, chat)
}

func (s *Server) handleDeleteCopilotChat(w http.ResponseWriter, r *http.Request) {
	clusterID, userID, ok := s.copilotChatScope(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("chat"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid chat id")
		return
	}
	if err := s.store.DeleteCopilotChat(r.Context(), id, clusterID, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "chat not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete chat")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
