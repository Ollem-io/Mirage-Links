package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Token is a bearer credential. Its JSON representation is always redacted; use
// Reveal only at the one-time credential delivery boundary.
type Token string
type TokenHash [32]byte

func NewToken() (Token, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return Token("mir_" + base64.RawURLEncoding.EncodeToString(b)), nil
}
func ParseToken(s string) (Token, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "mir_") {
		return "", NewValidation("token", "invalid bearer token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(s[4:])
	// Exactly 32 random bytes is canonical: reject shortened, extended, padded,
	// or otherwise non-canonical encodings.
	if err != nil || len(payload) != 32 || base64.RawURLEncoding.EncodeToString(payload) != s[4:] {
		return "", NewValidation("token", "invalid bearer token")
	}
	return Token(s), nil
}

// Reveal returns the credential only for the one-time create response. Do not
// store its result or include it in ordinary DTOs.
func (t Token) Reveal() string  { return string(t) }
func (t Token) Hash() TokenHash { return sha256.Sum256([]byte(t)) }
func (h TokenHash) Verify(t Token) bool {
	got := t.Hash()
	return subtle.ConstantTimeCompare(h[:], got[:]) == 1
}
func (t Token) MarshalJSON() ([]byte, error)     { return json.Marshal("[redacted]") }
func (h TokenHash) MarshalJSON() ([]byte, error) { return json.Marshal("[redacted]") }
