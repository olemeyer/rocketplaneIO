package store

// members.go — org membership & role management, invitations, org lifecycle,
// platform-admin operations and the audit log. Roles: owner > admin > member.
// The API layer (internal/api/rbac.go) enforces them; this file is the storage.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

var ErrLastOwner = errors.New("cannot remove or demote the last owner of an org")

// RoleInOrg returns the caller's role in the org ("" + ErrNotFound if not a member).
func (s *Store) RoleInOrg(ctx context.Context, userID, orgID uuid.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM memberships WHERE user_id=$1 AND org_id=$2`, userID, orgID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("role in org: %w", err)
	}
	return role, nil
}

// ListMembers returns every member of an org with their user profile + role.
func (s *Store) ListMembers(ctx context.Context, orgID uuid.UUID) ([]model.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_url, m.role, m.created_at
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.org_id=$1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, u.email`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	out := []model.Member{}
	for rows.Next() {
		var m model.Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) countOwnersTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships WHERE org_id=$1 AND role='owner'`, orgID).Scan(&n)
	return n, err
}

// UpdateMemberRole changes a member's role, refusing to demote the last owner.
func (s *Store) UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var cur string
	if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if cur == "owner" && role != "owner" {
		n, err := s.countOwnersTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastOwner
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET role=$3 WHERE org_id=$1 AND user_id=$2`, orgID, userID, role); err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	return tx.Commit(ctx)
}

// RemoveMember removes a user from an org, refusing to remove the last owner.
func (s *Store) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var cur string
	if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if cur == "owner" {
		n, err := s.countOwnersTx(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastOwner
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE org_id=$1 AND user_id=$2`, orgID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return tx.Commit(ctx)
}

// AddMember inserts a membership (used by invite-accept); idempotent on conflict.
func (s *Store) AddMember(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role=EXCLUDED.role`, orgID, userID, role)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

// RenameOrg updates an org's display name (slug is stable).
func (s *Store) RenameOrg(ctx context.Context, orgID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("org name required")
	}
	ct, err := s.pool.Exec(ctx, `UPDATE orgs SET name=$2 WHERE id=$1 AND is_personal=false`, orgID, name)
	if err != nil {
		return fmt.Errorf("rename org: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("cannot rename this org")
	}
	return nil
}

// DeleteOrg removes a non-personal org (cascades clusters, members, everything).
func (s *Store) DeleteOrg(ctx context.Context, orgID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM orgs WHERE id=$1 AND is_personal=false`, orgID)
	if err != nil {
		return fmt.Errorf("delete org: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("cannot delete a personal org")
	}
	return nil
}

// TransferOwnership promotes newOwner to owner and demotes the current caller to
// admin — the target must already be a member.
func (s *Store) TransferOwnership(ctx context.Context, orgID, fromUser, toUser uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=$1 AND user_id=$2)`, orgID, toUser).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET role='owner' WHERE org_id=$1 AND user_id=$2`, orgID, toUser); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET role='admin' WHERE org_id=$1 AND user_id=$2 AND role='owner'`, orgID, fromUser); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ── Invitations ──────────────────────────────────────────────────────────────

func hashInviteToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// CreateInvitation upserts a pending invite and returns it WITH the one-time
// plaintext token (never stored). Replaces any existing pending invite for the
// same (org,email).
func (s *Store) CreateInvitation(ctx context.Context, orgID uuid.UUID, email, role string, invitedBy uuid.UUID, ttl time.Duration) (*model.Invitation, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("valid email required")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(ttl)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Drop a prior pending invite so the partial unique index doesn't collide.
	if _, err := tx.Exec(ctx, `DELETE FROM invitations WHERE org_id=$1 AND lower(email)=lower($2) AND accepted_at IS NULL`, orgID, email); err != nil {
		return nil, err
	}
	var inv model.Invitation
	err = tx.QueryRow(ctx, `
		INSERT INTO invitations (org_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, org_id, email, role, created_at, expires_at`,
		orgID, email, role, hashInviteToken(token), invitedBy, expires).
		Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("create invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	inv.Token = token
	return &inv, nil
}

// ListInvitations returns pending (unaccepted, unexpired) invites of an org.
func (s *Store) ListInvitations(ctx context.Context, orgID uuid.UUID) ([]model.Invitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.org_id, i.email, i.role, COALESCE(u.email,''), i.created_at, i.expires_at
		FROM invitations i LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.org_id=$1 AND i.accepted_at IS NULL AND i.expires_at > now()
		ORDER BY i.created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()
	out := []model.Invitation{}
	for rows.Next() {
		var inv model.Invitation
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.InvitedBy, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvitation deletes a pending invite (scoped to its org).
func (s *Store) RevokeInvitation(ctx context.Context, orgID, invID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM invitations WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL`, invID, orgID)
	if err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetInvitationByToken returns a pending invite by its plaintext token, with the
// org name (for the public accept-preview page).
func (s *Store) GetInvitationByToken(ctx context.Context, token string) (*model.Invitation, error) {
	var inv model.Invitation
	err := s.pool.QueryRow(ctx, `
		SELECT i.id, i.org_id, i.email, i.role, i.created_at, i.expires_at, o.name
		FROM invitations i JOIN orgs o ON o.id = i.org_id
		WHERE i.token_hash=$1 AND i.accepted_at IS NULL AND i.expires_at > now()`,
		hashInviteToken(token)).
		Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.CreatedAt, &inv.ExpiresAt, &inv.OrgName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get invitation: %w", err)
	}
	return &inv, nil
}

// AcceptInvitation consumes a token: creates/updates the membership for userID
// and marks the invite accepted. Returns the org id joined.
func (s *Store) AcceptInvitation(ctx context.Context, token string, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var (
		invID uuid.UUID
		orgID uuid.UUID
		role  string
	)
	err = tx.QueryRow(ctx, `
		SELECT id, org_id, role FROM invitations
		WHERE token_hash=$1 AND accepted_at IS NULL AND expires_at > now() FOR UPDATE`,
		hashInviteToken(token)).Scan(&invID, &orgID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1,$2,$3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role=EXCLUDED.role`, orgID, userID, role); err != nil {
		return uuid.Nil, fmt.Errorf("join org: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET accepted_at=now(), accepted_by=$2 WHERE id=$1`, invID, userID); err != nil {
		return uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return orgID, nil
}

// ── Platform admin (super admin) ─────────────────────────────────────────────

// SetPlatformAdmin grants/revokes the global platform-admin flag on a user.
func (s *Store) SetPlatformAdmin(ctx context.Context, userID uuid.UUID, admin bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET is_platform_admin=$2 WHERE id=$1`, userID, admin)
	if err != nil {
		return fmt.Errorf("set platform admin: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserSuspended suspends/reactivates a user (suspended users cannot log in).
func (s *Store) SetUserSuspended(ctx context.Context, userID uuid.UUID, suspended bool) error {
	var q string
	if suspended {
		q = `UPDATE users SET suspended_at=now() WHERE id=$1`
	} else {
		q = `UPDATE users SET suspended_at=NULL WHERE id=$1`
	}
	ct, err := s.pool.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("suspend user: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// EnsurePlatformAdminByEmail grants platform-admin to the given emails on boot
// (RP_PLATFORM_ADMINS) — idempotent, only touches existing users.
func (s *Store) EnsurePlatformAdminByEmail(ctx context.Context, emails []string) error {
	for _, e := range emails {
		e = strings.TrimSpace(strings.ToLower(e))
		if e == "" {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET is_platform_admin=true WHERE lower(email)=$1`, e); err != nil {
			return err
		}
	}
	return nil
}

// ListAllUsers is the platform-admin user directory (org counts included).
func (s *Store) ListAllUsers(ctx context.Context, search string, limit int) ([]model.AdminUser, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.avatar_url, u.is_platform_admin, u.suspended_at, u.created_at,
		       (SELECT count(*) FROM memberships m WHERE m.user_id = u.id)
		FROM users u
		WHERE ($1 = '' OR u.email ILIKE '%'||$1||'%' OR u.name ILIKE '%'||$1||'%')
		ORDER BY u.created_at DESC LIMIT $2`, search, limit)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}
	defer rows.Close()
	out := []model.AdminUser{}
	for rows.Next() {
		var a model.AdminUser
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.AvatarURL, &a.IsPlatformAdmin, &a.SuspendedAt, &a.CreatedAt, &a.OrgCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAllOrgs is the platform-admin org directory (member/cluster counts).
func (s *Store) ListAllOrgs(ctx context.Context, search string, limit int) ([]model.AdminOrg, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.slug, o.is_personal, o.created_at,
		       (SELECT count(*) FROM memberships m WHERE m.org_id = o.id),
		       (SELECT count(*) FROM clusters c WHERE c.org_id = o.id)
		FROM orgs o
		WHERE ($1 = '' OR o.name ILIKE '%'||$1||'%' OR o.slug ILIKE '%'||$1||'%')
		ORDER BY o.created_at DESC LIMIT $2`, search, limit)
	if err != nil {
		return nil, fmt.Errorf("list all orgs: %w", err)
	}
	defer rows.Close()
	out := []model.AdminOrg{}
	for rows.Next() {
		var a model.AdminOrg
		if err := rows.Scan(&a.ID, &a.Name, &a.Slug, &a.IsPersonal, &a.CreatedAt, &a.MemberCount, &a.ClusterCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PlatformStats returns instance-wide counts for the admin dashboard.
func (s *Store) PlatformStats(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for key, q := range map[string]string{
		"users":     `SELECT count(*) FROM users`,
		"suspended": `SELECT count(*) FROM users WHERE suspended_at IS NOT NULL`,
		"admins":    `SELECT count(*) FROM users WHERE is_platform_admin`,
		"orgs":      `SELECT count(*) FROM orgs WHERE is_personal=false`,
		"clusters":  `SELECT count(*) FROM clusters`,
	} {
		var n int
		if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}

// ── Audit log ────────────────────────────────────────────────────────────────

// WriteAudit records one mutating action. Best-effort: never blocks the request.
func (s *Store) WriteAudit(ctx context.Context, orgID *uuid.UUID, actorID *uuid.UUID, actorEmail, action, targetType, targetID, targetName string, metadata map[string]any, ip string) error {
	md, _ := json.Marshal(metadata)
	if len(md) == 0 {
		md = []byte("{}")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (org_id, actor_id, actor_email, action, target_type, target_id, target_name, metadata, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		orgID, actorID, actorEmail, action, targetType, targetID, targetName, md, ip)
	if err != nil {
		return fmt.Errorf("write audit: %w", err)
	}
	return nil
}

// ListAudit returns an org's audit entries, newest first.
func (s *Store) ListAudit(ctx context.Context, orgID uuid.UUID, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, actor_email, action, target_type, target_id, target_name, metadata, created_at
		FROM audit_log WHERE org_id=$1 ORDER BY created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var a model.AuditEntry
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ActorEmail, &a.Action, &a.TargetType, &a.TargetID, &a.TargetName, &a.Metadata, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FindUserByEmail returns a user id by email (for invite/transfer flows), or
// ErrNotFound.
func (s *Store) FindUserByEmail(ctx context.Context, email string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email)=lower($1)`, strings.TrimSpace(email)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
