package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// api_tokens.go — programmatischer Zugriff (Service-Accounts). Ein Token ist
// org-scoped, trägt eine Rolle (member|admin) und wird als `rp_<hex>` gesendet.
// Persistiert wird NUR sha256(secret) (via hashToken/newToken aus tokens.go);
// das Geheimnis ist genau einmal (bei Erstellung) sichtbar.

func apiTokenStatus(t *model.APIToken, now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return "revoked"
	case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
		return "expired"
	default:
		return "active"
	}
}

const apiTokenCols = `t.id, t.org_id, t.name, t.role, t.prefix, t.created_by,
	COALESCE(NULLIF(u.name,''), u.email, ''), t.created_at, t.last_used_at, t.expires_at, t.revoked_at`

func scanAPIToken(row pgx.Row, t *model.APIToken) error {
	return row.Scan(&t.ID, &t.OrgID, &t.Name, &t.Role, &t.Prefix, &t.CreatedBy,
		&t.CreatedByName, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt)
}

// ListAPITokens liefert die Tokens eines Orgs (ohne Geheimnis/Hash).
func (s *Store) ListAPITokens(ctx context.Context, orgID uuid.UUID) ([]model.APIToken, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+apiTokenCols+`
		FROM api_tokens t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.org_id=$1 ORDER BY t.created_at DESC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()
	now := time.Now().UTC()
	out := []model.APIToken{}
	for rows.Next() {
		var t model.APIToken
		if err := scanAPIToken(rows, &t); err != nil {
			return nil, err
		}
		t.Status = apiTokenStatus(&t, now)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateAPIToken legt ein Token an und gibt das Geheimnis EINMALIG zurück.
// role wird auf member|admin begrenzt (nie owner/platform-admin). expiresAt nil
// = kein Ablauf.
func (s *Store) CreateAPIToken(ctx context.Context, orgID uuid.UUID, name, role string, createdBy uuid.UUID, expiresAt *time.Time) (*model.APIToken, error) {
	if role != "admin" {
		role = "member"
	}
	secret, hash, err := newToken("rp_")
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	prefix := secret[:11] // "rp_" + 8 hex — reicht zur Wiedererkennung in der UI
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (org_id, name, role, token_hash, prefix, created_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		orgID, name, role, hash, prefix, createdBy, expiresAt).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("insert api token: %w", err)
	}
	t, err := s.getAPIToken(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	t.Secret = secret // NUR hier zurückgegeben
	return t, nil
}

func (s *Store) getAPIToken(ctx context.Context, orgID, id uuid.UUID) (*model.APIToken, error) {
	var t model.APIToken
	err := scanAPIToken(s.pool.QueryRow(ctx, `SELECT `+apiTokenCols+`
		FROM api_tokens t LEFT JOIN users u ON u.id = t.created_by
		WHERE t.id=$1 AND t.org_id=$2`, id, orgID), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Status = apiTokenStatus(&t, time.Now().UTC())
	return &t, nil
}

// RevokeAPIToken markiert ein Token als widerrufen (idempotent).
func (s *Store) RevokeAPIToken(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE api_tokens SET revoked_at=COALESCE(revoked_at, now())
		WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIToken entfernt ein Token endgültig.
func (s *Store) DeleteAPIToken(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// APITokenAuth ist das aufgelöste Principal hinter einem Bearer-Token.
type APITokenAuth struct {
	TokenID   uuid.UUID
	OrgID     uuid.UUID
	Role      string
	CreatedBy uuid.UUID
}

// ResolveAPIToken prüft ein Bearer-Geheimnis: Hash-Lookup, nicht widerrufen und
// nicht abgelaufen. Aktualisiert last_used_at gedrosselt (max. 1×/Minute).
func (s *Store) ResolveAPIToken(ctx context.Context, secret string) (*APITokenAuth, error) {
	var a APITokenAuth
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, role, created_by FROM api_tokens
		WHERE token_hash=$1 AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())`, hashToken(secret)).
		Scan(&a.TokenID, &a.OrgID, &a.Role, &a.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// last_used_at gedrosselt fortschreiben (best-effort).
	_, _ = s.pool.Exec(ctx, `
		UPDATE api_tokens SET last_used_at=now()
		WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, a.TokenID)
	return &a, nil
}
