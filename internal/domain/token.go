package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
)

// Token is a bearer credential. It intentionally has no JSON representation.
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
	if !strings.HasPrefix(s, "mir_") || len(s) <= 4 {
		return "", NewValidation("token", "invalid bearer token")
	}
	if _, err := base64.RawURLEncoding.DecodeString(s[4:]); err != nil {
		return "", NewValidation("token", "invalid bearer token")
	}
	return Token(s), nil
}
func (t Token) Hash() TokenHash { return sha256.Sum256([]byte(t)) }
func (h TokenHash) Verify(t Token) bool {
	got := t.Hash()
	return subtle.ConstantTimeCompare(h[:], got[:]) == 1
}
func (h TokenHash) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
