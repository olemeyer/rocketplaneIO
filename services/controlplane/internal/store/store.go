// Package store contains all persistence logic. It uses jackc/pgx/v5 with
// parameterised SQL only (no ORM). Every method takes a context and wraps
// errors for callers.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalidToken is returned when an enroll/agent token is missing, expired,
// used or revoked.
var ErrInvalidToken = errors.New("invalid or expired token")

// Store wraps the pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a Store over the given pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ── Users ──────────────────────────────────────────────────────────────────

// UpsertUserFromOIDC creates or updates a user. With a non-empty OIDC subject it
// matches on google_sub (linking an existing email row if present); with an
// empty subject (dev-login) it matches on email.
func (s *Store) UpsertUserFromOIDC(ctx context.Context, sub, email, name, avatar string) (*model.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var u model.User
	scan := func(row pgx.Row) error {
		return row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.CreatedAt)
	}
	const cols = `id, email, name, avatar_url, created_at`

	if sub != "" {
		// 1. existing google user → refresh profile.
		err := scan(tx.QueryRow(ctx, `
			UPDATE users SET email=$2, name=$3, avatar_url=$4
			WHERE google_sub=$1 RETURNING `+cols, sub, email, name, avatar))
		if err == nil {
			return &u, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("update user by sub: %w", err)
		}
		// 2. existing email row (e.g. dev-created) → link the subject.
		err = scan(tx.QueryRow(ctx, `
			UPDATE users SET google_sub=$1, name=$3, avatar_url=$4
			WHERE email=$2 RETURNING `+cols, sub, email, name, avatar))
		if err == nil {
			return &u, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("link user by email: %w", err)
		}
		// 3. brand new user.
		if err := scan(tx.QueryRow(ctx, `
			INSERT INTO users (email, name, avatar_url, google_sub)
			VALUES ($2, $3, $4, $1) RETURNING `+cols, sub, email, name, avatar)); err != nil {
			return nil, fmt.Errorf("insert user: %w", err)
		}
		return &u, tx.Commit(ctx)
	}

	// Dev-login path: match on email only.
	err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM users WHERE email=$1`, email))
	if err == nil {
		return &u, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("select user by email: %w", err)
	}
	if err := scan(tx.QueryRow(ctx, `
		INSERT INTO users (email, name, avatar_url)
		VALUES ($1, $2, $3) RETURNING `+cols, email, name, avatar)); err != nil {
		return nil, fmt.Errorf("insert dev user: %w", err)
	}
	return &u, tx.Commit(ctx)
}

// GetUser loads a user by id (incl. platform-admin + suspension state, which the
// session middleware and admin console rely on).
func (s *Store) GetUser(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, avatar_url, is_platform_admin, suspended_at, created_at
		FROM users WHERE id=$1`, userID).
		Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.IsPlatformAdmin, &u.SuspendedAt, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

// ── Orgs & memberships ─────────────────────────────────────────────────────

// EnsurePersonalOrg returns the user's personal org, creating it (plus an owner
// membership) on first login.
func (s *Store) EnsurePersonalOrg(ctx context.Context, userID uuid.UUID, email string) (*model.Org, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var o model.Org
	err = tx.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, o.is_personal, o.created_at
		FROM orgs o
		JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id=$1 AND o.is_personal=true
		ORDER BY o.created_at
		LIMIT 1`, userID).Scan(&o.ID, &o.Name, &o.Slug, &o.IsPersonal, &o.CreatedAt)
	if err == nil {
		o.Role = "owner"
		return &o, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lookup personal org: %w", err)
	}

	local := email
	if i := strings.IndexByte(email, '@'); i > 0 {
		local = email[:i]
	}
	name := local
	if name == "" {
		name = "Personal"
	}
	created, err := createOrgTx(ctx, tx, name, slugify(local), true, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit personal org: %w", err)
	}
	return created, nil
}

// CreateOrg creates a new (non-personal) org owned by userID.
func (s *Store) CreateOrg(ctx context.Context, name string, userID uuid.UUID) (*model.Org, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("org name required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	o, err := createOrgTx(ctx, tx, name, slugify(name), false, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit org: %w", err)
	}
	return o, nil
}

// createOrgTx inserts an org (retrying with a random suffix on slug collision)
// and an owner membership, inside the given transaction. Each insert attempt
// runs inside its own savepoint (pgx nested tx) so a unique violation does not
// abort the surrounding transaction.
func createOrgTx(ctx context.Context, tx pgx.Tx, name, baseSlug string, isPersonal bool, userID uuid.UUID) (*model.Org, error) {
	var o model.Org
	slug := baseSlug
	for attempt := 0; attempt < 6; attempt++ {
		sp, err := tx.Begin(ctx) // savepoint
		if err != nil {
			return nil, fmt.Errorf("begin savepoint: %w", err)
		}
		err = sp.QueryRow(ctx, `
			INSERT INTO orgs (name, slug, is_personal, created_by)
			VALUES ($1, $2, $3, $4)
			RETURNING id, name, slug, is_personal, created_at`,
			name, slug, isPersonal, userID).
			Scan(&o.ID, &o.Name, &o.Slug, &o.IsPersonal, &o.CreatedAt)
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return nil, fmt.Errorf("release savepoint: %w", err)
			}
			break
		}
		_ = sp.Rollback(ctx)
		if isUniqueViolation(err) {
			suffix, rerr := randHex(3)
			if rerr != nil {
				return nil, rerr
			}
			slug = baseSlug + "-" + suffix
			continue
		}
		return nil, fmt.Errorf("insert org: %w", err)
	}
	if o.ID == uuid.Nil {
		return nil, fmt.Errorf("could not allocate unique slug for %q", baseSlug)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')`,
		o.ID, userID); err != nil {
		return nil, fmt.Errorf("insert owner membership: %w", err)
	}
	o.Role = "owner"
	return &o, nil
}

// ListOrgsForUser returns every org the user belongs to, with the user's role.
func (s *Store) ListOrgsForUser(ctx context.Context, userID uuid.UUID) ([]model.Org, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.slug, o.is_personal, m.role, o.created_at
		FROM orgs o
		JOIN memberships m ON m.org_id = o.id
		WHERE m.user_id=$1
		ORDER BY o.is_personal DESC, o.created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("list orgs: %w", err)
	}
	defer rows.Close()

	orgs := []model.Org{}
	for rows.Next() {
		var o model.Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.IsPersonal, &o.Role, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan org: %w", err)
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

// GetOrgByID loads an org by id.
func (s *Store) GetOrgByID(ctx context.Context, orgID uuid.UUID) (*model.Org, error) {
	return s.getOrg(ctx, `WHERE id=$1`, orgID)
}

// GetOrgBySlug loads an org by slug.
func (s *Store) GetOrgBySlug(ctx context.Context, slug string) (*model.Org, error) {
	return s.getOrg(ctx, `WHERE slug=$1`, slug)
}

func (s *Store) getOrg(ctx context.Context, where string, arg any) (*model.Org, error) {
	var o model.Org
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, slug, is_personal, created_at FROM orgs `+where, arg).
		Scan(&o.ID, &o.Name, &o.Slug, &o.IsPersonal, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get org: %w", err)
	}
	return &o, nil
}

// IsMember reports whether the user belongs to the org.
func (s *Store) IsMember(ctx context.Context, userID, orgID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM memberships WHERE user_id=$1 AND org_id=$2)`,
		userID, orgID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	return exists, nil
}
