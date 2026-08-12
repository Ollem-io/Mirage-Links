package domain

import (
	"strings"
	"time"
)

const (
	DefaultTTL   = 6 * time.Hour
	MinTTL       = time.Minute
	MaxTTL       = 12 * time.Hour
	DefaultGrace = 15 * time.Second
	MinGrace     = time.Second
	MaxGrace     = 15 * time.Minute
)

type Clock interface{ Now() time.Time }
type SystemClock struct{}

func (SystemClock) Now() time.Time             { return time.Now().UTC() }
func ParseTTL(s string) (time.Duration, error) { return parseBoundedDuration("ttl", s, MinTTL, MaxTTL) }
func ParseGrace(s string) (time.Duration, error) {
	return parseBoundedDuration("grace", s, MinGrace, MaxGrace)
}
func parseBoundedDuration(field, s string, min, max time.Duration) (time.Duration, error) {
	d, e := time.ParseDuration(strings.TrimSpace(s))
	if e != nil {
		return 0, NewValidation(field, "must be a duration")
	}
	if d < min || d > max {
		return 0, NewValidation(field, "out of allowed range")
	}
	return d, nil
}
func Expired(at, expires time.Time) bool { return !at.Before(expires) }
