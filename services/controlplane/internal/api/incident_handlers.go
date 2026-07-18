package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// incident_handlers.go — REST for the incident lifecycle. Reads require org
// membership (resolveClusterScope), mutations require at least the member
// role (requireClusterRole "member"). Every mutating endpoint writes to the
// timeline and the org audit log.

// actor returns the ID + email of the logged-in user (for timeline/audit).
func (s *Server) actor(r *http.Request) (*uuid.UUID, string) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		return nil, ""
	}
	id := user.ID
	return &id, user.Email
}

// handleListIncidents — GET …/incidents?status=&limit=
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	incs, err := s.store.ListIncidents(r.Context(), clusterID, status, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": incs})
}

// handleIncidentStats — GET …/incidents/stats
func (s *Server) handleIncidentStats(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	stats, err := s.store.IncidentStats(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load stats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

// handleGetIncident — GET …/incidents/{id} (incident + timeline).
func (s *Server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	_, clusterID, ok := s.resolveClusterScope(w, r)
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	inc, err := s.store.GetIncident(r.Context(), clusterID, id)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}
	events, err := s.store.ListIncidentEvents(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load timeline")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incident": inc, "timeline": events})
}

// handleCreateIncident — POST …/incidents (manual declaration).
func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	var req struct {
		Title    string `json:"title"`
		Summary  string `json:"summary"`
		Severity string `json:"severity"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	actorID, email := s.actor(r)
	inc, err := s.store.CreateIncident(r.Context(), orgID, clusterID, req.Title, req.Summary, req.Severity, actorID, email)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create incident")
		return
	}
	s.audit(r, &orgID, "incident.declare", "incident", inc.ID.String(), inc.Title, map[string]any{"severity": inc.Severity})
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusCreated, inc)
}

// handleUpdateIncident — PATCH …/incidents/{id} {title?, summary?, severity?}
func (s *Server) handleUpdateIncident(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	var req struct {
		Title    *string `json:"title"`
		Summary  *string `json:"summary"`
		Severity *string `json:"severity"`
	}
	if !decode(w, r, &req) {
		return
	}
	actorID, email := s.actor(r)
	inc, err := s.store.UpdateIncidentFields(r.Context(), clusterID, id, req.Title, req.Summary, req.Severity, actorID, email)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}
	s.audit(r, &orgID, "incident.update", "incident", id.String(), inc.Title, nil)
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, inc)
}

// handleIncidentStatus — POST …/incidents/{id}/status {status}
func (s *Server) handleIncidentStatus(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &req) {
		return
	}
	// Validate the status in the handler (don't leak a raw store/DB error to the client).
	switch req.Status {
	case "open", "acknowledged", "mitigated", "resolved":
	default:
		writeErr(w, http.StatusBadRequest, "invalid status")
		return
	}
	actorID, email := s.actor(r)
	inc, err := s.store.UpdateIncidentStatus(r.Context(), clusterID, id, req.Status, actorID, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "incident not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update status")
		return
	}
	s.audit(r, &orgID, "incident.status", "incident", id.String(), inc.Title, map[string]any{"status": inc.Status})
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, inc)
}

// handleAssignIncident — POST …/incidents/{id}/assign {userId|null}
func (s *Server) handleAssignIncident(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	var req struct {
		UserID *string `json:"userId"`
	}
	if !decode(w, r, &req) {
		return
	}
	var assignee *uuid.UUID
	if req.UserID != nil && *req.UserID != "" {
		uid, err := uuid.Parse(*req.UserID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid userId")
			return
		}
		// Only org members may be assigned.
		if _, rerr := s.store.RoleInOrg(r.Context(), uid, orgID); rerr != nil {
			writeErr(w, http.StatusBadRequest, "assignee is not a member of this org")
			return
		}
		assignee = &uid
	}
	actorID, email := s.actor(r)
	inc, err := s.store.AssignIncident(r.Context(), clusterID, id, assignee, actorID, email)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}
	s.audit(r, &orgID, "incident.assign", "incident", id.String(), inc.Title, nil)
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, inc)
}

// handleIncidentNote — POST …/incidents/{id}/notes {note}
func (s *Server) handleIncidentNote(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Note == "" {
		writeErr(w, http.StatusBadRequest, "note is required")
		return
	}
	actorID, email := s.actor(r)
	if err := s.store.AddIncidentNote(r.Context(), clusterID, id, req.Note, actorID, email); err != nil {
		writeIncidentErr(w, err)
		return
	}
	s.audit(r, &orgID, "incident.note", "incident", id.String(), "", nil)
	s.broker.Publish(clusterID, "incidents", 0)
	events, _ := s.store.ListIncidentEvents(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"timeline": events})
}

// handleIncidentPostmortem — PUT …/incidents/{id}/postmortem {text}
func (s *Server) handleIncidentPostmortem(w http.ResponseWriter, r *http.Request) {
	orgID, clusterID, ok := s.requireClusterRole(w, r, "member")
	if !ok {
		return
	}
	id, ok := parseIncidentID(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &req) {
		return
	}
	actorID, email := s.actor(r)
	inc, err := s.store.SetIncidentPostmortem(r.Context(), clusterID, id, req.Text, actorID, email)
	if err != nil {
		writeIncidentErr(w, err)
		return
	}
	s.audit(r, &orgID, "incident.postmortem", "incident", id.String(), inc.Title, nil)
	s.broker.Publish(clusterID, "incidents", 0)
	writeJSON(w, http.StatusOK, inc)
}

func parseIncidentID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("incident"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid incident id")
		return uuid.Nil, false
	}
	return id, true
}

func writeIncidentErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "incident not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, "incident operation failed")
}
