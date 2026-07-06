package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// action_definitions_handlers.go — CRUD der org-weiten Starlark-Workflows.

var defNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

type defReq struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Params         json.RawMessage `json:"params"`
	Source         string          `json:"source"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
}

func (s *Server) validateDefReq(w http.ResponseWriter, req *defReq) bool {
	if !defNamePattern.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "name must be kebab-case (a-z, 0-9, -)")
		return false
	}
	if req.Source == "" || len(req.Source) > 64_000 {
		writeErr(w, http.StatusBadRequest, "source is required (max 64k)")
		return false
	}
	if len(req.Params) > 8_000 {
		writeErr(w, http.StatusBadRequest, "params too large")
		return false
	}
	if _, err := parseParamSpecs(req.Params); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	// Save-Zeit-Verifikation: kaputte Workflows kann man nicht anlegen.
	if err := checkScriptSource(req.Name, req.Source); err != nil {
		writeErr(w, http.StatusBadRequest, "script does not compile: "+err.Error())
		return false
	}
	if req.TimeoutSeconds == 0 {
		req.TimeoutSeconds = 600
	}
	if req.TimeoutSeconds < 30 || req.TimeoutSeconds > 1800 {
		writeErr(w, http.StatusBadRequest, "timeoutSeconds must be 30..1800")
		return false
	}
	return true
}

func (s *Server) handleListActionDefs(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	defs, err := s.store.ListActionDefinitions(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list definitions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"definitions": defs})
}

func (s *Server) handleCreateActionDef(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	var req defReq
	if !decode(w, r, &req) || !s.validateDefReq(w, &req) {
		return
	}
	user, _ := auth.UserFrom(r.Context())
	d, err := s.store.CreateActionDefinition(r.Context(), orgID, user.ID, req.Name, req.Description, req.Params, req.Source, req.TimeoutSeconds)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create definition (name taken?)")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleUpdateActionDef(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("def"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid definition id")
		return
	}
	var req defReq
	if !decode(w, r, &req) || !s.validateDefReq(w, &req) {
		return
	}
	d, err := s.store.UpdateActionDefinition(r.Context(), orgID, id, req.Name, req.Description, req.Params, req.Source, req.TimeoutSeconds)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "definition not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update definition")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteActionDef(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("def"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid definition id")
		return
	}
	if err := s.store.DeleteActionDefinition(r.Context(), orgID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "definition not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete definition")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
