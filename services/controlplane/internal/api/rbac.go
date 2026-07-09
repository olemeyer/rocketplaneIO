package api

// rbac.go — role enforcement for org-scoped endpoints. Roles form a hierarchy:
//   owner (3) > admin (2) > member (1).
// Membership alone (resolveOrg) grants READ access to an org's data. Mutations
// gate on a minimum role via requireRole; org-lifecycle + member management gate
// on owner/admin; the platform-admin console gates on requirePlatformAdmin.

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

func roleRank(role string) int {
	switch role {
	case "owner":
		return 3
	case "admin":
		return 2
	case "member":
		return 1
	}
	return 0
}

// resolveOrgRole resolves {org} (uuid or slug), confirms membership, and returns
// the org id together with the caller's role. Replaces resolveOrg where the role
// is needed; resolveOrg still exists for pure read endpoints.
func (s *Server) resolveOrgRole(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	user, _ := auth.UserFrom(r.Context())
	param := r.PathValue("org")

	var (
		orgID uuid.UUID
		err   error
	)
	if id, perr := uuid.Parse(param); perr == nil {
		orgID = id
		_, err = s.store.GetOrgByID(r.Context(), id)
	} else {
		o, gerr := s.store.GetOrgBySlug(r.Context(), param)
		if gerr == nil {
			orgID = o.ID
		}
		err = gerr
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "org not found")
		} else {
			writeErr(w, http.StatusInternalServerError, "failed to resolve org")
		}
		return uuid.Nil, "", false
	}
	// API token: strictly scoped to the token's org, role comes from the token (never
	// platform-admin/owner). Must take effect BEFORE the user/platform-admin path.
	if tp, ok := auth.TokenFrom(r.Context()); ok {
		if tp.OrgID != orgID {
			writeErr(w, http.StatusForbidden, "token is not scoped to this org")
			return uuid.Nil, "", false
		}
		return orgID, tp.Role, true
	}
	// Platform admins can act on any org (support / operations).
	if user != nil && user.IsPlatformAdmin {
		role, rerr := s.store.RoleInOrg(r.Context(), user.ID, orgID)
		if errors.Is(rerr, store.ErrNotFound) {
			return orgID, "owner", true // treat as owner for admin override
		}
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "failed to check role")
			return uuid.Nil, "", false
		}
		if roleRank(role) < roleRank("owner") {
			role = "owner"
		}
		return orgID, role, true
	}
	role, rerr := s.store.RoleInOrg(r.Context(), user.ID, orgID)
	if errors.Is(rerr, store.ErrNotFound) {
		writeErr(w, http.StatusForbidden, "not a member of this org")
		return uuid.Nil, "", false
	}
	if rerr != nil {
		writeErr(w, http.StatusInternalServerError, "failed to check role")
		return uuid.Nil, "", false
	}
	return orgID, role, true
}

// requireRole resolves the org and enforces role >= minRole, writing 403 otherwise.
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, minRole string) (uuid.UUID, string, bool) {
	orgID, role, ok := s.resolveOrgRole(w, r)
	if !ok {
		return uuid.Nil, "", false
	}
	if roleRank(role) < roleRank(minRole) {
		writeErr(w, http.StatusForbidden, "requires "+minRole+" role in this org")
		return uuid.Nil, "", false
	}
	return orgID, role, true
}

// requireClusterRole resolves {org}+{cluster}, verifies the cluster belongs to
// the org, and enforces role >= minRole. The cluster-scoped analogue of
// requireRole (used by alert rules, actions, metric/dashboard mutations).
func (s *Server) requireClusterRole(w http.ResponseWriter, r *http.Request, minRole string) (uuid.UUID, uuid.UUID, bool) {
	orgID, role, ok := s.resolveOrgRole(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	if roleRank(role) < roleRank(minRole) {
		writeErr(w, http.StatusForbidden, "requires "+minRole+" role in this org")
		return uuid.Nil, uuid.Nil, false
	}
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	if _, _, err := s.store.GetClusterWithNamespaces(r.Context(), orgID, clusterID); err != nil {
		writeErr(w, http.StatusNotFound, "cluster not found")
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, clusterID, true
}

// requirePlatformAdmin gates the /api/admin/* console. API tokens are refused
// outright — the platform-admin console is interactive-session-only, even if the
// token's creating user happens to be a platform admin.
func (s *Server) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	if _, isToken := auth.TokenFrom(r.Context()); isToken {
		writeErr(w, http.StatusForbidden, "the platform-admin console requires an interactive session")
		return nil, false
	}
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil || !user.IsPlatformAdmin {
		writeErr(w, http.StatusForbidden, "platform admin only")
		return nil, false
	}
	return user, true
}

// audit records a mutating action best-effort (never fails the request).
func (s *Server) audit(r *http.Request, orgID *uuid.UUID, action, targetType, targetID, targetName string, metadata map[string]any) {
	user, _ := auth.UserFrom(r.Context())
	var actorID *uuid.UUID
	email := ""
	if user != nil {
		id := user.ID
		actorID = &id
		email = user.Email
	}
	_ = s.store.WriteAudit(r.Context(), orgID, actorID, email, action, targetType, targetID, targetName, metadata, clientIP(r))
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
