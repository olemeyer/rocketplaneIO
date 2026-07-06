package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Token prefixes (see shared conventions): enroll tokens vs. long-lived agent
// tokens. Only sha256(token) is ever persisted; the plaintext is returned once.
const (
	enrollTokenPrefix = "rpe_"
	agentTokenPrefix  = "rpa_"
	tokenBytes        = 32
)

// newToken returns a fresh plaintext token (prefix + 32 random bytes hex) and
// its hex-encoded sha256 hash for storage.
func newToken(prefix string) (plain, hash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("read random: %w", err)
	}
	plain = prefix + hex.EncodeToString(b)
	return plain, hashToken(plain), nil
}

// hashToken returns the hex-encoded sha256 of a plaintext token.
func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// randHex returns n random bytes as a hex string.
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns an arbitrary string into a URL-safe slug base.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugInvalid.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	return s
}
