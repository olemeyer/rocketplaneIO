package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// dashboards_handlers.go — CRUD der org-weiten Dashboards-as-Code. Die spec ist
// roh Perses-YAML (offener Standard) — der Server hält sie 1:1 (portabel), die
// Struktur validiert der Client beim Parsen.

type dashboardReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Spec        string `json:"spec"` // Perses-YAML
}

func (s *Server) validateDashboardReq(w http.ResponseWriter, req *dashboardReq) bool {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeErr(w, http.StatusBadRequest, "dashboard name is required (max 200 chars)")
		return false
	}
	if strings.TrimSpace(req.Spec) == "" || len(req.Spec) > 256_000 {
		writeErr(w, http.StatusBadRequest, "dashboard spec (Perses YAML) is required (max 256k)")
		return false
	}
	if len(req.Description) > 2000 {
		req.Description = req.Description[:2000]
	}
	return true
}

func (s *Server) handleListDashboards(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	ds, err := s.store.ListDashboards(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list dashboards")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dashboards": ds})
}

func (s *Server) handleCreateDashboard(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	var req dashboardReq
	if !decode(w, r, &req) || !s.validateDashboardReq(w, &req) {
		return
	}
	user, _ := auth.UserFrom(r.Context())
	d, err := s.store.CreateDashboard(r.Context(), orgID, user.ID, req.Name, req.Description, req.Spec)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create dashboard (name taken?)")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleUpdateDashboard(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("dash"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid dashboard id")
		return
	}
	var req dashboardReq
	if !decode(w, r, &req) || !s.validateDashboardReq(w, &req) {
		return
	}
	d, err := s.store.UpdateDashboard(r.Context(), orgID, id, req.Name, req.Description, req.Spec)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "dashboard not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update dashboard")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("dash"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid dashboard id")
		return
	}
	if err := s.store.DeleteDashboard(r.Context(), orgID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "dashboard not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete dashboard")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
