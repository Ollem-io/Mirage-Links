package domain

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

type SpaceID string
type LinkID string

func NewSpaceID() SpaceID { return SpaceID(randomID()) }
func NewLinkID() LinkID   { return LinkID(randomID()) }
func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

type Alias string

func ParseAlias(s string) (Alias, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if err := validateLabel(s, "alias"); err != nil {
		return "", err
	}
	return Alias(s), nil
}
func (a Alias) String() string { return string(a) }

type LinkName string

func ParseLinkName(s string) (LinkName, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if err := validateLabel(s, "name"); err != nil {
		return "", err
	}
	return LinkName(s), nil
}
func (n LinkName) String() string { return string(n) }
func validateLabel(s, field string) error {
	if len(s) < 1 || len(s) > 63 {
		return NewValidation(field, "must be a DNS label of 1 to 63 characters")
	}
	for i, c := range s {
		good := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
		if !good || c == '-' && (i == 0 || i == len(s)-1) {
			return NewValidation(field, "must match [a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?")
		}
	}
	return nil
}
