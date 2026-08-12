package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDurationBoundaries(t *testing.T) {
	for _, x := range []struct {
		s  string
		ok bool
	}{{"1m", true}, {"12h", true}, {"59s", false}, {"12h1s", false}, {"junk", false}} {
		_, e := ParseTTL(x.s)
		if (e == nil) != x.ok {
			t.Errorf("ttl %s", x.s)
		}
	}
	for _, x := range []struct {
		s  string
		ok bool
	}{{"1s", true}, {"15m", true}, {"0s", false}, {"15m1s", false}} {
		_, e := ParseGrace(x.s)
		if (e == nil) != x.ok {
			t.Errorf("grace %s", x.s)
		}
	}
}
func TestLabels(t *testing.T) {
	for _, s := range []string{"a", "api-calm", "a1"} {
		if _, e := ParseLinkName(s); e != nil {
			t.Error(e)
		}
	}
	for _, s := range []string{"", "-a", "a-", "A_", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl"} {
		if _, e := ParseLinkName(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestHostAndURL(t *testing.T) {
	a, _ := ParseAlias("calm-fox")
	n, _ := ParseLinkName("api")
	b, _ := ParseBaseHost("mirage.example.com")
	if got := b.Host(n, a); got != "api-calm-fox.mirage.example.com" {
		t.Error(got)
	}
	b, _ = ParseBaseHost("-mirage.example.com")
	if got := b.Host(n, a); got != "api-calm-fox-mirage.example.com" {
		t.Error(got)
	}
	for _, p := range []int{80, 443, 9955} {
		if _, e := PublicURL(b, n, a, p); e != nil {
			t.Error(e)
		}
	}
	if _, e := PublicURL(b, n, a, 0); e == nil {
		t.Error("port")
	}
}
func TestHealth(t *testing.T) {
	for _, s := range []string{"GET http://127.0.0.1:99/", "HEAD http://[::1]/", "POST http://localhost/"} {
		if _, e := ParseHealthCheck(s); e != nil {
			t.Errorf("%s: %v", s, e)
		}
	}
	for _, s := range []string{"PUT http://localhost/", "GET http://example.com/", "GET /x", "GET http://127.0.0.1/ x"} {
		if _, e := ParseHealthCheck(s); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
func TestTransitionAndExpiry(t *testing.T) {
	l := Link{Status: StatusCreating}
	if e := l.Transition(StatusActive); e == nil {
		t.Error("illegal")
	}
	for _, s := range []LinkStatus{StatusStarting, StatusHealthy, StatusActive, StatusStopping, StatusDeleted} {
		if e := l.Transition(s); e != nil {
			t.Error(e)
		}
	}
	if e := l.Transition(StatusDeleted); e != nil {
		t.Error("idempotent")
	}
	if e := l.Transition(StatusActive); e == nil {
		t.Error("terminal")
	}
	now := time.Now()
	if !Expired(now, now) || Expired(now, now.Add(time.Nanosecond)) {
		t.Error("boundary")
	}
}
func TestTokenDoesNotLeak(t *testing.T) {
	tok, e := NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if !tok.Hash().Verify(tok) || tok.Hash().Verify(Token("mir_wrong")) {
		t.Error("verification")
	}
	s := Space{TokenHash: tok.Hash()}
	b, _ := json.Marshal(s)
	if string(b) == "" || string(b) == string(tok) || contains(string(b), "hash") {
		t.Errorf("leak %s", b)
	}
}
func contains(s, x string) bool {
	for i := 0; i+len(x) <= len(s); i++ {
		if s[i:i+len(x)] == x {
			return true
		}
	}
	return false
}
func FuzzParseHealthCheck(f *testing.F) {
	f.Add("GET http://localhost/")
	f.Fuzz(func(t *testing.T, s string) { ParseHealthCheck(s) })
}
func FuzzParseLinkName(f *testing.F) {
	f.Add("api")
	f.Fuzz(func(t *testing.T, s string) { ParseLinkName(s) })
}

func TestErrorsAndIDs(t *testing.T) {
	for _, e := range []error{NewValidation("x", "bad"), NewConflict("conflict"), NewNotFound("missing"), NewUnauthorized("no")} {
		if e.Error() == "" {
			t.Fatal("empty")
		}
	}
	if !IsKind(NewUnauthorized("no"), Unauthorized) || IsKind(NewUnauthorized("no"), NotFound) {
		t.Fatal("kind")
	}
	if NewSpaceID() == "" || NewLinkID() == "" || NewSpaceID() == NewSpaceID() {
		t.Fatal("ids")
	}
	if _, e := ParseAlias("BAD_"); e == nil {
		t.Fatal("alias")
	}
	a, _ := ParseAlias(" CALM ")
	if a.String() != "calm" {
		t.Fatal(a)
	}
}
func TestTokenParsingAndMarshaling(t *testing.T) {
	tok, _ := NewToken()
	got, e := ParseToken(" " + string(tok) + " ")
	if e != nil || got != tok {
		t.Fatal(e)
	}
	for _, s := range []string{"", "x", "mir_%%%"} {
		if _, e := ParseToken(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	b, e := json.Marshal(tok.Hash())
	if e != nil || string(b) != `"[redacted]"` {
		t.Fatalf("%s %v", b, e)
	}
}
func TestBaseHostAndEntityExpiry(t *testing.T) {
	for _, s := range []string{"", "-", "bad/path", "a..b"} {
		if _, e := ParseBaseHost(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
	now := time.Now()
	sp := Space{ExpiresAt: now}
	li := Link{ExpiresAt: now}
	if !sp.Expired(now) || !li.Expired(now) {
		t.Fatal("entity expiry")
	}
	if (SystemClock{}).Now().IsZero() {
		t.Fatal("clock")
	}
}
