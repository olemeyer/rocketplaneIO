package api

// tenancy_handlers.go — org member management, invitations, org lifecycle and
// the org audit log. Read endpoints require membership; mutations gate on role
// via requireRole (admin manages members/clusters/alerts; owner manages the org
// itself). Every mutation writes an audit entry.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/auth"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

const inviteTTL = 7 * 24 * time.Hour

func validRole(role string) bool {
	return role == "owner" || role == "admin" || role == "member"
}

// ── Members ──────────────────────────────────────────────────────────────────

// handleListMembers — GET /api/orgs/{org}/members  (any member)
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.resolveOrg(w, r)
	if !ok {
		return
	}
	members, err := s.store.ListMembers(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	me, _ := auth.UserFrom(r.Context())
	for i := range members {
		if me != nil && members[i].UserID == me.ID {
			members[i].IsYou = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// handleUpdateMemberRole — PATCH /api/orgs/{org}/members/{user}  (admin+, owner to touch owners)
func (s *Server) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID, callerRole, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	targetID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !validRole(body.Role) {
		writeErr(w, http.StatusBadRequest, "role must be owner, admin or member")
		return
	}
	// Only an owner may grant or modify the owner role.
	if body.Role == "owner" && callerRole != "owner" {
		writeErr(w, http.StatusForbidden, "only an owner can grant the owner role")
		return
	}
	if err := s.store.UpdateMemberRole(r.Context(), orgID, targetID, body.Role); err != nil {
		if errors.Is(err, store.ErrLastOwner) {
			writeErr(w, http.StatusConflict, "cannot demote the last owner")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "member not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update role")
		return
	}
	s.audit(r, &orgID, "member.role_changed", "member", targetID.String(), "", map[string]any{"role": body.Role})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRemoveMember — DELETE /api/orgs/{org}/members/{user}
// admin+ to remove others; any member may remove themselves (leave).
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	orgID, callerRole, ok := s.resolveOrgRole(w, r)
	if !ok {
		return
	}
	targetID, err := uuid.Parse(r.PathValue("user"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return
	}
	me, _ := auth.UserFrom(r.Context())
	leaving := me != nil && me.ID == targetID
	if !leaving && roleRank(callerRole) < roleRank("admin") {
		writeErr(w, http.StatusForbidden, "requires admin role to remove other members")
		return
	}
	if err := s.store.RemoveMember(r.Context(), orgID, targetID); err != nil {
		if errors.Is(err, store.ErrLastOwner) {
			writeErr(w, http.StatusConflict, "cannot remove the last owner — transfer ownership first")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "member not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to remove member")
		return
	}
	action := "member.removed"
	if leaving {
		action = "member.left"
	}
	s.audit(r, &orgID, action, "member", targetID.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── Invitations ──────────────────────────────────────────────────────────────

// handleListInvitations — GET /api/orgs/{org}/invitations  (admin+)
func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	invs, err := s.store.ListInvitations(r.Context(), orgID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list invitations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invs})
}

// handleCreateInvitation — POST /api/orgs/{org}/invitations  (admin+; owner to invite owner)
func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	orgID, callerRole, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	if !validRole(body.Role) {
		writeErr(w, http.StatusBadRequest, "role must be owner, admin or member")
		return
	}
	if body.Role == "owner" && callerRole != "owner" {
		writeErr(w, http.StatusForbidden, "only an owner can invite an owner")
		return
	}
	inv, err := s.store.CreateInvitation(r.Context(), orgID, body.Email, body.Role, mustUserID(r), inviteTTL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	inv.AcceptURL = s.cfg.PublicURL + "/invite/" + inv.Token
	s.audit(r, &orgID, "invitation.created", "invitation", inv.ID.String(), inv.Email, map[string]any{"role": inv.Role})
	// The token is returned ONCE here so the inviter can share the accept link.
	// (Email delivery is out of scope for the self-hosted default; the link is
	// the deliverable. A configured SMTP provider can be wired in later.)
	writeJSON(w, http.StatusOK, map[string]any{"invitation": inv})
}

// handleRevokeInvitation — DELETE /api/orgs/{org}/invitations/{invite}  (admin+)
func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	invID, err := uuid.Parse(r.PathValue("invite"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid invitation id")
		return
	}
	if err := s.store.RevokeInvitation(r.Context(), orgID, invID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "invitation not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to revoke invitation")
		return
	}
	s.audit(r, &orgID, "invitation.revoked", "invitation", invID.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleInvitationPreview — GET /api/invitations/{token}  (authenticated: shows
// which org the token joins, so the accept page can render before committing).
func (s *Server) handleInvitationPreview(w http.ResponseWriter, r *http.Request) {
	inv, err := s.store.GetInvitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "invitation not found or expired")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load invitation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"orgName": inv.OrgName, "email": inv.Email, "role": inv.Role, "expiresAt": inv.ExpiresAt,
	})
}

// handleAcceptInvitation — POST /api/invitations/{token}/accept  (authenticated).
// Joins the caller's account to the org and switches their session to it.
func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "sign in to accept")
		return
	}
	orgID, err := s.store.AcceptInvitation(r.Context(), r.PathValue("token"), user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "invitation not found or expired")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to accept invitation")
		return
	}
	_ = s.auth.SetSession(w, user.ID, orgID)
	s.audit(r, &orgID, "invitation.accepted", "member", user.ID.String(), user.Email, nil)
	writeJSON(w, http.StatusOK, map[string]any{"orgId": orgID})
}

// ── Org lifecycle ────────────────────────────────────────────────────────────

// handleUpdateOrg — PATCH /api/orgs/{org}  (owner)  — rename.
func (s *Server) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "owner")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.store.RenameOrg(r.Context(), orgID, body.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, &orgID, "org.renamed", "org", orgID.String(), body.Name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteOrg — DELETE /api/orgs/{org}  (owner)  — destroys the org + all data.
func (s *Server) handleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "owner")
	if !ok {
		return
	}
	if err := s.store.DeleteOrg(r.Context(), orgID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, nil, "org.deleted", "org", orgID.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTransferOwnership — POST /api/orgs/{org}/transfer  (owner).
func (s *Server) handleTransferOwnership(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "owner")
	if !ok {
		return
	}
	var body struct {
		UserID string `json:"userId"`
	}
	if !decode(w, r, &body) {
		return
	}
	toUser, err := uuid.Parse(strings.TrimSpace(body.UserID))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid userId")
		return
	}
	if err := s.store.TransferOwnership(r.Context(), orgID, mustUserID(r), toUser); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "the new owner must already be a member")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}
	s.audit(r, &orgID, "org.ownership_transferred", "member", toUser.String(), "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── Audit log ────────────────────────────────────────────────────────────────

// handleListAudit — GET /api/orgs/{org}/audit  (admin+)
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := s.requireRole(w, r, "admin")
	if !ok {
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n := atoiOr(v, 100); n > 0 {
			limit = n
		}
	}
	entries, err := s.store.ListAudit(r.Context(), orgID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list audit log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func mustUserID(r *http.Request) uuid.UUID {
	if u, ok := auth.UserFrom(r.Context()); ok {
		return u.ID
	}
	return uuid.Nil
}
