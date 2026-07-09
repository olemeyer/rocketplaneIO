package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// apitoken_handlers.go — management of API tokens (service accounts). DELIBERATELY
// restricted to an interactive session AND the admin role: a token may not create
// or revoke further tokens (otherwise self-renewal / privilege persistence).

// requireSessionAdmin requires a cookie session (not an API token) with the
// admin role in the org. Returns the orgID.
func (s *Server) requireSessionAdmin(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if _, isToken := auth.TokenFrom(r.Context()); isToken {
		writeErr(w, http.StatusForbidden, "managing API tokens requires an interactive session")
		return uuid.Nil, false
	}
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return uuid.Nil, false
	}
	return orgID, true
}

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireSessionAdmin(w, r)
	if !ok {
		return
	}
	tokens, err := s.store.ListAPITokens(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list tokens")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireSessionAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Role != "admin" {
		req.Role = "member"
	}
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		if req.ExpiresInDays > 3650 {
			req.ExpiresInDays = 3650
		}
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	user, _ := auth.UserFrom(r.Context())
	tok, err := s.store.CreateAPIToken(r.Context(), orgID, req.Name, req.Role, user.ID, expiresAt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create token")
		return
	}
	s.audit(r, &orgID, "apitoken.create", "api_token", tok.ID.String(), tok.Name, map[string]any{"role": tok.Role})
	writeJSON(w, http.StatusCreated, tok) // contains the secret ONE TIME ONLY
}

func (s *Server) handleRevokeAPIToken(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireSessionAdmin(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), orgID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "token not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to revoke token")
		return
	}
	s.audit(r, &orgID, "apitoken.revoke", "api_token", id.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireSessionAdmin(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("token"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if err := s.store.DeleteAPIToken(r.Context(), orgID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "token not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete token")
		return
	}
	s.audit(r, &orgID, "apitoken.delete", "api_token", id.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
