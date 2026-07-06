package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// ErrEmailTaken is returned when a local signup collides with an existing email.
var ErrEmailTaken = errors.New("email already registered")

// CountUsers returns the number of users. Zero means the instance is uninitialised
// and the first-user setup screen should be shown (n8n-style onboarding).
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateLocalUser inserts a user with a bcrypt password hash. Returns ErrEmailTaken
// on a duplicate email.
func (s *Store) CreateLocalUser(ctx context.Context, email, name, passwordHash string) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, email, name, avatar_url, created_at`,
		email, name, passwordHash).
		Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("insert local user: %w", err)
	}
	return &u, nil
}

// GetUserForLogin loads a user by email together with the stored password hash.
// An empty hash means the account has no local password (SSO-only).
func (s *Store) GetUserForLogin(ctx context.Context, email string) (*model.User, string, error) {
	var (
		u    model.User
		hash *string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, avatar_url, created_at, password_hash
		FROM users WHERE email=$1`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("get user for login: %w", err)
	}
	if hash == nil {
		return &u, "", nil
	}
	return &u, *hash, nil
}
