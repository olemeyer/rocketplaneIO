package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

// local.go implements the "easy way to start" onboarding (n8n-style): a local
// email+password owner account, no SSO configuration required. Google stays
// additive (only offered when configured).

// SetupStatus reports whether the instance still needs its first user, and which
// login methods are available. Public (no session).
func (a *Auth) SetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"needsSetup":    n == 0,
		"googleEnabled": a.cfg.GoogleConfigured(),
		"localEnabled":  true,
	})
}

// Setup creates the very first user (instance owner) + their personal org, then
// signs them in. Only works while the instance has zero users.
func (a *Auth) Setup(w http.ResponseWriter, r *http.Request) {
	n, err := a.store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "already set up")
		return
	}

	var body struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	email, name, pw, verr := validateSignup(body.Email, body.Name, body.Password)
	if verr != "" {
		writeErr(w, http.StatusBadRequest, verr)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	user, err := a.store.CreateLocalUser(r.Context(), email, name, string(hash))
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			writeErr(w, http.StatusConflict, "email already registered")
			return
		}
		writeErr(w, http.StatusInternalServerError, "could not create user")
		return
	}
	if err := a.finishLogin(r.Context(), w, user.ID, user.Email); err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// LoginLocal authenticates an existing local account (email + password). SSO-only.
func (a *Auth) LoginLocal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || body.Password == "" {
		writeErr(w, http.StatusBadRequest, "email and password required")
		return
	}

	user, hash, err := a.store.GetUserForLogin(r.Context(), email)
	// Uniform error to avoid leaking which emails exist.
	if err != nil || hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err := a.finishLogin(r.Context(), w, user.ID, user.Email); err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// finishLogin ensures the personal org exists and sets the session cookie.
func (a *Auth) finishLogin(ctx context.Context, w http.ResponseWriter, userID uuid.UUID, email string) error {
	org, err := a.store.EnsurePersonalOrg(ctx, userID, email)
	if err != nil {
		return fmt.Errorf("ensure personal org: %w", err)
	}
	return a.SetSession(w, userID, org.ID)
}

// validateSignup normalises + checks the signup fields, returning a user-facing
// error string (empty when valid).
func validateSignup(rawEmail, rawName, pw string) (email, name, password, errMsg string) {
	email = strings.TrimSpace(strings.ToLower(rawEmail))
	name = strings.TrimSpace(rawName)
	if email == "" || !strings.Contains(email, "@") {
		return "", "", "", "a valid email is required"
	}
	if len(pw) < 8 {
		return "", "", "", "password must be at least 8 characters"
	}
	if name == "" {
		if i := strings.IndexByte(email, '@'); i > 0 {
			name = email[:i]
		} else {
			name = email
		}
	}
	return email, name, pw, ""
}
