// Package auth implements Google OIDC login, the dev-login bypass, HMAC session
// cookies and the request middlewares (session + agent bearer token).
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/config"
	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/store"
)

const oauthStateCookie = "rp_oauth_state"

// Auth bundles configuration, the store and (optionally) the configured Google
// OIDC provider.
type Auth struct {
	cfg           *config.Config
	store         *store.Store
	oauth         *oauth2.Config
	verifier      *oidc.IDTokenVerifier
	secureCookies bool
}

// New builds the Auth service. When Google credentials are configured it sets up
// the OIDC provider; otherwise the dev-login bypass is used.
func New(ctx context.Context, cfg *config.Config, st *store.Store) (*Auth, error) {
	a := &Auth{
		cfg:           cfg,
		store:         st,
		secureCookies: strings.HasPrefix(cfg.PublicURL, "https"),
	}
	if cfg.GoogleConfigured() {
		provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
		if err != nil {
			return nil, fmt.Errorf("init google oidc provider: %w", err)
		}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID})
		a.oauth = &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.PublicURL + "/auth/google/callback",
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		}
	}
	return a, nil
}

// ── Google OIDC ────────────────────────────────────────────────────────────

// StartGoogle redirects the browser to Google's consent screen.
func (a *Auth) StartGoogle(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		writeErr(w, http.StatusNotImplemented, "google oidc not configured")
		return
	}
	state, err := randToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((10 * time.Minute) / time.Second),
	})
	http.Redirect(w, r, a.oauth.AuthCodeURL(state), http.StatusFound)
}

// CallbackGoogle handles the OIDC redirect: exchanges the code, verifies the id
// token, upserts the user, ensures a personal org and sets the session cookie.
func (a *Auth) CallbackGoogle(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		writeErr(w, http.StatusNotImplemented, "google oidc not configured")
		return
	}
	ctx := r.Context()

	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		writeErr(w, http.StatusBadRequest, "invalid oauth state")
		return
	}
	// State consumed — clear it.
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "missing code")
		return
	}
	token, err := a.oauth.Exchange(ctx, code)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "token exchange failed")
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		writeErr(w, http.StatusBadGateway, "no id_token in response")
		return
	}
	idToken, err := a.verifier.Verify(ctx, rawID)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "id token verification failed")
		return
	}
	var claims struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		writeErr(w, http.StatusBadGateway, "invalid id token claims")
		return
	}
	if claims.Email == "" {
		writeErr(w, http.StatusBadGateway, "id token missing email")
		return
	}

	if err := a.loginAndRedirect(ctx, w, r, claims.Sub, claims.Email, claims.Name, claims.Picture, true); err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
	}
}

// ── Dev-login bypass ───────────────────────────────────────────────────────

// DevLogin is a local login shortcut, enabled only when RP_ENV=dev and Google
// is not configured.
func (a *Auth) DevLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.IsDev() || a.cfg.GoogleConfigured() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeErr(w, http.StatusBadRequest, "valid email required")
		return
	}
	name := email
	if i := strings.IndexByte(email, '@'); i > 0 {
		name = email[:i]
	}
	if err := a.loginAndRedirect(r.Context(), w, r, "", email, name, "", false); err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
	}
}

// loginAndRedirect upserts the user, ensures the personal org, sets the session
// and either redirects (browser flow) or returns JSON (dev-login).
func (a *Auth) loginAndRedirect(ctx context.Context, w http.ResponseWriter, r *http.Request, sub, email, name, avatar string, redirect bool) error {
	user, err := a.store.UpsertUserFromOIDC(ctx, sub, email, name, avatar)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	org, err := a.store.EnsurePersonalOrg(ctx, user.ID, user.Email)
	if err != nil {
		return fmt.Errorf("ensure personal org: %w", err)
	}
	if err := a.SetSession(w, user.ID, org.ID); err != nil {
		return err
	}
	if redirect {
		http.Redirect(w, r, "/", http.StatusFound)
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "currentOrgId": org.ID})
	return nil
}

// Logout clears the session cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	a.clearSession(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── Middlewares ────────────────────────────────────────────────────────────

// WithSession requires a valid session cookie and loads the user + current org
// into the request context.
func (a *Auth) WithSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := a.ParseSession(r)
		if err != nil {
			a.unauthorized(w, r)
			return
		}
		user, err := a.store.GetUser(r.Context(), sess.UserID)
		if err != nil {
			// Session signed OK but the user no longer exists (e.g. DB reset).
			a.unauthorized(w, r)
			return
		}
		if user.SuspendedAt != nil {
			// Suspended by a platform admin — invalidate the live session.
			a.unauthorized(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxOrgID, sess.OrgID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithSessionOrToken accepts EITHER a browser session cookie OR an API bearer
// token (`rp_…`). For a token it loads the token's creating user as the actor
// (created_by is NOT NULL / ON DELETE CASCADE, so it always exists) and attaches
// a TokenPrincipal scoped to the token's org + role. Authz code (resolveOrg,
// resolveOrgRole, requirePlatformAdmin) inspects that principal to scope strictly
// to the token's org and to refuse platform-admin / owner / token-management.
func (a *Auth) WithSessionOrToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1) Session cookie (interactive).
		if sess, err := a.ParseSession(r); err == nil {
			user, err := a.store.GetUser(r.Context(), sess.UserID)
			if err != nil || user.SuspendedAt != nil {
				a.unauthorized(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUser, user)
			ctx = context.WithValue(ctx, ctxOrgID, sess.OrgID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// 2) API bearer token.
		if tok := bearerToken(r); strings.HasPrefix(tok, "rp_") {
			princ, err := a.store.ResolveAPIToken(r.Context(), tok)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "invalid or expired api token")
				return
			}
			user, err := a.store.GetUser(r.Context(), princ.CreatedBy)
			if err != nil || user.SuspendedAt != nil {
				writeErr(w, http.StatusUnauthorized, "api token owner is not active")
				return
			}
			ctx := context.WithValue(r.Context(), ctxUser, user)
			ctx = context.WithValue(ctx, ctxOrgID, princ.OrgID)
			ctx = WithTokenPrincipal(ctx, &TokenPrincipal{TokenID: princ.TokenID, OrgID: princ.OrgID, Role: princ.Role})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		a.unauthorized(w, r)
	})
}

// WithAPIToken accepts ONLY an API bearer token (`rp_…`) — no session-cookie
// fallback. The MCP endpoint uses this: every MCP caller is a token, so a
// transaction always carries a meaningful token identity, and the endpoint can
// be exposed without any cookie/CSRF surface.
func (a *Auth) WithAPIToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if !strings.HasPrefix(tok, "rp_") {
			writeErr(w, http.StatusUnauthorized, "an api token (Authorization: Bearer rp_…) is required")
			return
		}
		princ, err := a.store.ResolveAPIToken(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid or expired api token")
			return
		}
		user, err := a.store.GetUser(r.Context(), princ.CreatedBy)
		if err != nil || user.SuspendedAt != nil {
			writeErr(w, http.StatusUnauthorized, "api token owner is not active")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxOrgID, princ.OrgID)
		ctx = WithTokenPrincipal(ctx, &TokenPrincipal{TokenID: princ.TokenID, OrgID: princ.OrgID, Role: princ.Role})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// unauthorized clears any stale session cookie and returns 401. Clearing the
// cookie is what breaks the /login ⇄ / redirect loop the browser hits when a
// signed-but-invalid cookie is present (expired session, or user/DB gone): once
// the cookie is gone the middleware stops bouncing /login back to /.
func (a *Auth) unauthorized(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(SessionCookieName); err == nil {
		a.clearSession(w)
	}
	writeErr(w, http.StatusUnauthorized, "unauthorized")
}

// WithAgent requires a valid agent bearer token and loads the cluster id into
// the request context.
func (a *Auth) WithAgent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		clusterID, err := a.store.AuthAgentToken(r.Context(), token)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxClusterID, clusterID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}
