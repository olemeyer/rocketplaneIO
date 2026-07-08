package api

// admin_handlers.go — the platform-admin (super admin) console: instance-wide
// user & org directory, suspend/reactivate users, grant/revoke platform-admin.
// Every endpoint gates on requirePlatformAdmin. This is distinct from org roles:
// a platform admin operates the whole instance (self-hosted operator / SaaS staff).

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// handleAdminStats — GET /api/admin/stats
func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	stats, err := s.store.PlatformStats(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load stats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
}

// handleAdminListUsers — GET /api/admin/users?search=&limit=
func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	users, err := s.store.ListAllUsers(r.Context(), r.URL.Query().Get("search"), atoiOr(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleAdminListOrgs — GET /api/admin/orgs?search=&limit=
func (s *Server) handleAdminListOrgs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePlatformAdmin(w, r); !ok {
		return
	}
	orgs, err := s.store.ListAllOrgs(r.Context(), r.URL.Query().Get("search"), atoiOr(r.URL.Query().Get("limit"), 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list orgs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"orgs": orgs})
}

// handleAdminSetUserFlags — PATCH /api/admin/users/{user}
// Body: {suspended?: bool, platformAdmin?: bool}. Guards against self-lockout.
func (s *Server) handleAdminSetUserFlags(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		Suspended     *bool `json:"suspended"`
		PlatformAdmin *bool `json:"platformAdmin"`
	}
	if !decode(w, r, &body) {
		return
	}
	// A platform admin cannot suspend or demote themselves (avoid lockout).
	if targetID == caller.ID {
		if (body.Suspended != nil && *body.Suspended) || (body.PlatformAdmin != nil && !*body.PlatformAdmin) {
			writeErr(w, http.StatusConflict, "you cannot suspend or demote your own admin account")
			return
		}
	}
	if body.Suspended != nil {
		if err := s.store.SetUserSuspended(r.Context(), targetID, *body.Suspended); err != nil {
			writeAdminErr(w, err)
			return
		}
		action := "user.reactivated"
		if *body.Suspended {
			action = "user.suspended"
		}
		s.audit(r, nil, action, "user", targetID.String(), "", nil)
	}
	if body.PlatformAdmin != nil {
		if err := s.store.SetPlatformAdmin(r.Context(), targetID, *body.PlatformAdmin); err != nil {
			writeAdminErr(w, err)
			return
		}
		s.audit(r, nil, "user.platform_admin_changed", "user", targetID.String(), "", map[string]any{"admin": *body.PlatformAdmin})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeAdminErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "user not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, "operation failed")
}

// ensurePlatformAdmins grants super-admin to configured emails on boot
// (RP_PLATFORM_ADMINS). Idempotent; only touches users that already exist.
func (s *Server) ensurePlatformAdmins(ctx context.Context) {
	if len(s.cfg.PlatformAdmins) == 0 {
		return
	}
	_ = s.store.EnsurePlatformAdminByEmail(ctx, s.cfg.PlatformAdmins)
}
