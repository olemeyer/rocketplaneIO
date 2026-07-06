package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SessionCookieName is the HMAC-signed session cookie (see shared conventions).
const SessionCookieName = "rp_session"

// sessionTTL is the lifetime of a session (~30 days).
const sessionTTL = 30 * 24 * time.Hour

// sessionPayload is the JSON body carried inside the cookie.
type sessionPayload struct {
	UID string `json:"uid"`
	OID string `json:"oid"`
	Exp int64  `json:"exp"`
}

// Session is the decoded, verified session.
type Session struct {
	UserID uuid.UUID
	OrgID  uuid.UUID
}

// sign returns hex(HMAC-SHA256(msg, secret)).
func (a *Auth) sign(msg string) string {
	mac := hmac.New(sha256.New, []byte(a.cfg.SessionSecret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

// encodeSession builds the cookie value: base64url(json).hex(hmac(base64url)).
func (a *Auth) encodeSession(userID, orgID uuid.UUID) (string, error) {
	payload := sessionPayload{
		UID: userID.String(),
		OID: orgID.String(),
		Exp: time.Now().Add(sessionTTL).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal session: %w", err)
	}
	b64 := base64.RawURLEncoding.EncodeToString(raw)
	return b64 + "." + a.sign(b64), nil
}

// decodeSession verifies the signature and expiry and returns the session.
func (a *Auth) decodeSession(value string) (*Session, error) {
	b64, sig, ok := strings.Cut(value, ".")
	if !ok {
		return nil, fmt.Errorf("malformed session")
	}
	if !hmac.Equal([]byte(sig), []byte(a.sign(b64))) {
		return nil, fmt.Errorf("bad session signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	var p sessionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	if time.Now().Unix() >= p.Exp {
		return nil, fmt.Errorf("session expired")
	}
	uid, err := uuid.Parse(p.UID)
	if err != nil {
		return nil, fmt.Errorf("bad uid: %w", err)
	}
	oid, _ := uuid.Parse(p.OID) // org may be uuid.Nil
	return &Session{UserID: uid, OrgID: oid}, nil
}

// SetSession writes the signed session cookie.
func (a *Auth) SetSession(w http.ResponseWriter, userID, orgID uuid.UUID) error {
	value, err := a.encodeSession(userID, orgID)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
		MaxAge:   int(sessionTTL / time.Second),
	})
	return nil
}

// clearSession removes the session cookie.
func (a *Auth) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// ParseSession reads and verifies the session cookie from a request.
func (a *Auth) ParseSession(r *http.Request) (*Session, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("no session cookie")
	}
	return a.decodeSession(c.Value)
}
