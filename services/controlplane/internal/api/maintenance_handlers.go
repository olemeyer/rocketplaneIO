package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// maintenance_handlers.go — maintenance windows. Reads require org membership,
// mutations require the admin role (like alert config). During an active window
// the alert evaluator suppresses notifications + auto-incident declaration for
// the matching scope.

// handleListMaintenance — GET …/maintenance
func (s *Server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	windows, err := s.store.ListMaintenanceWindows(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list maintenance windows")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"windows": windows})
}

// handleCreateMaintenance — POST …/maintenance {title, scopeNamespace, startsAt, endsAt}
func (s *Server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	var req struct {
		Title          string `json:"title"`
		ScopeNamespace string `json:"scopeNamespace"`
		StartsAt       string `json:"startsAt"`
		EndsAt         string `json:"endsAt"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	starts, err := time.Parse(time.RFC3339, req.StartsAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid startsAt (expected RFC3339)")
		return
	}
	ends, err := time.Parse(time.RFC3339, req.EndsAt)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid endsAt (expected RFC3339)")
		return
	}
	if !ends.After(starts) {
		writeErr(w, http.StatusBadRequest, "endsAt must be after startsAt")
		return
	}
	actorID, _ := s.actor(r)
	win, err := s.store.CreateMaintenanceWindow(r.Context(), orgID, clusterID, req.Title, req.ScopeNamespace, starts, ends, actorID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
	}
	s.audit(r, &orgID, "maintenance.create", "maintenance_window", win.ID.String(), win.Title, map[string]any{"namespace": win.ScopeNamespace})
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusCreated, win)
}

// handleDeleteMaintenance — DELETE …/maintenance/{window} (ends if active, else removes)
func (s *Server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("window"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid window id")
		return
	}
	if err := s.store.EndMaintenanceWindow(r.Context(), clusterID, id); err != nil {
		writeIncidentErr(w, err) // reuses ErrNotFound → 404 mapping
		return
	}
	s.audit(r, &orgID, "maintenance.end", "maintenance_window", id.String(), "", nil)
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
