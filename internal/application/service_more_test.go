package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/domain"
)

func TestQueriesValidationAndOneTimeCredential(t *testing.T) {
	s, f, tok := setup(t)
	ctx := context.Background()
	spaces, err := s.ListSpaces(ctx)
	if err != nil || len(spaces) != 1 {
		t.Fatal(err, spaces)
	}
	if _, err = s.Authorize(ctx, "BAD_", tok); !domain.IsKind(err, domain.Validation) {
		t.Fatal(err)
	}
	if _, err = s.Authorize(ctx, "calm-fox", "wrong"); !domain.IsKind(err, domain.Unauthorized) {
		t.Fatal(err)
	}
	if _, err = s.CreateLink(ctx, input(tok)); err != nil {
		t.Fatal(err)
	}
	links, err := s.ListLinks(ctx, "calm-fox", tok)
	if err != nil || len(links) != 1 {
		t.Fatal(err, links)
	}
	logs, err := s.LogsFor(ctx, "calm-fox", tok, "api", 10)
	if err != nil || len(logs) != 1 || logs[0].Text != "redacted" {
		t.Fatal(err, logs)
	}
	s.Logs = nil
	if _, err = s.LogsFor(ctx, "calm-fox", tok, "api", 1); err == nil {
		t.Fatal("missing logs")
	}
	// persisted representation carries only a hash; issuing another space never reveals old token.
	sp := spaces[0]
	if sp.TokenHash.Verify("wrong") || !sp.TokenHash.Verify(tok) {
		t.Fatal("hash contract")
	}
	_ = f
}

func TestInputTablesAndMissingPorts(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*CreateLinkInput)
	}{
		{"command", func(x *CreateLinkInput) { x.Command = " " }}, {"folder", func(x *CreateLinkInput) { x.Folder = "" }},
		{"name", func(x *CreateLinkInput) { x.Name = "BAD_" }}, {"health-empty", func(x *CreateLinkInput) { x.HealthCheck = domain.HealthCheck{} }},
		{"health-remote", func(x *CreateLinkInput) { x.HealthCheck.URL = "http://example.com/" }},
		{"ttl-low", func(x *CreateLinkInput) { x.TTL = time.Second }}, {"ttl-high", func(x *CreateLinkInput) { x.TTL = 13 * time.Hour }},
		{"grace-low", func(x *CreateLinkInput) { x.Grace = time.Millisecond }}, {"grace-high", func(x *CreateLinkInput) { x.Grace = 16 * time.Minute }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, tok := setup(t)
			x := input(tok)
			tc.mutate(&x)
			if _, e := s.CreateLink(ctx, x); !domain.IsKind(e, domain.Validation) {
				t.Fatalf("%v", e)
			}
		})
	}
	s, _, tok := setup(t)
	s.Ports = nil
	if _, e := s.CreateLink(ctx, input(tok)); e == nil {
		t.Fatal("missing lifecycle ports")
	}
	s, _, _ = setup(t)
	if e := s.DeleteSpace(ctx, DeleteSpaceInput{Alias: "BAD_"}); !domain.IsKind(e, domain.Validation) {
		t.Fatal(e)
	}
}

func TestSpaceCreationBranches(t *testing.T) {
	ctx := context.Background()
	m := newMem()
	f := &fake{mem: m, now: time.Now()}
	makeService := func() *Service { return &Service{Repo: m, Clock: f, IDs: f, Aliases: f, Tokens: f, Hashes: f} }
	for _, ttl := range []time.Duration{time.Second, 13 * time.Hour} {
		s := makeService()
		if _, e := s.CreateSpace(ctx, CreateSpaceInput{TTL: ttl}); !domain.IsKind(e, domain.Validation) {
			t.Fatal(e)
		}
	}
	s := makeService()
	if _, e := s.CreateSpace(ctx, CreateSpaceInput{Alias: "BAD_"}); !domain.IsKind(e, domain.Validation) {
		t.Fatal(e)
	}
	s = makeService()
	s.Aliases = nil
	if _, e := s.CreateSpace(ctx, CreateSpaceInput{}); e == nil {
		t.Fatal("alias port")
	}
	s = makeService()
	s.IDs = nil
	if _, e := s.CreateSpace(ctx, CreateSpaceInput{Alias: "ok"}); e == nil {
		t.Fatal("identity ports")
	}
	s = makeService()
	r, e := s.CreateSpace(ctx, CreateSpaceInput{Alias: "named", TTL: time.Minute})
	if e != nil || r.Token == "" {
		t.Fatal(e, r)
	}
}

type repoFault struct {
	*mem
	op    string
	count int
}

func (r *repoFault) hit(op string) error {
	if r.op == op {
		r.count++
		return errors.New("repo " + op)
	}
	return nil
}
func (r *repoFault) CreateLink(c context.Context, l domain.Link) error {
	if e := r.hit("create"); e != nil {
		return e
	}
	return r.mem.CreateLink(c, l)
}
func (r *repoFault) SaveLink(c context.Context, l domain.Link) error {
	if e := r.hit(fmt.Sprintf("save%d", r.count+1)); e != nil {
		return e
	}
	r.count++
	return r.mem.SaveLink(c, l)
}
func (r *repoFault) ListLinks(c context.Context, id domain.SpaceID) ([]domain.Link, error) {
	if e := r.hit("list"); e != nil {
		return nil, e
	}
	return r.mem.ListLinks(c, id)
}
func (r *repoFault) DeleteSpace(c context.Context, id domain.SpaceID) error {
	if e := r.hit("delete-space"); e != nil {
		return e
	}
	return r.mem.DeleteSpace(c, id)
}

func TestConcurrentDuplicateMutations(t *testing.T) {
	s, _, tok := setup(t)
	ctx := context.Background()
	x := input(tok)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.CreateLink(ctx, x); errs <- e }()
	}
	wg.Wait()
	close(errs)
	ok, conf := 0, 0
	for e := range errs {
		if e == nil {
			ok++
		} else if domain.IsKind(e, domain.Conflict) {
			conf++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conf != 1 {
		t.Fatal(ok, conf)
	}
	errs = make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.DeleteLink(ctx, LinkMutationInput{Alias: "calm-fox", Token: tok, Name: "api"})
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := s.DeleteLink(ctx, LinkMutationInput{Alias: "calm-fox", Token: tok, Name: "unknown"}); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
}

func TestAutomaticRestartPolicyAndCap(t *testing.T) {
	s, f, tok := setup(t)
	ctx := context.Background()
	x := input(tok)
	x.Restarts = false
	if _, e := s.CreateLink(ctx, x); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ScheduleAutomaticRestart(ctx, "calm-fox", tok, "api"); !domain.IsKind(e, domain.Conflict) {
		t.Fatal(e)
	}
	if e := s.DeleteLink(ctx, LinkMutationInput{Alias: "calm-fox", Token: tok, Name: "api"}); e != nil {
		t.Fatal(e)
	}
	x.Restarts = true
	if _, e := s.CreateLink(ctx, x); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 8; i++ {
		at, e := s.ScheduleAutomaticRestart(ctx, "calm-fox", tok, "api")
		if e != nil {
			t.Fatal(e)
		}
		want := time.Second * time.Duration(1<<min(i, 6))
		if want > time.Minute {
			want = time.Minute
		}
		if !at.Equal(f.now.Add(want)) {
			t.Fatalf("%d %v", i, at)
		}
	}
	l, _ := s.Repo.FindLink(ctx, "s", "api")
	l.ExpiresAt = f.now.Add(time.Second)
	_ = s.Repo.SaveLink(ctx, l)
	if _, e := s.ScheduleAutomaticRestart(ctx, "calm-fox", tok, "api"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
}

func TestSpaceTTLBoundsLinkAndForceAudit(t *testing.T) {
	s, f, tok := setup(t)
	ctx := context.Background()
	sp, _ := s.Repo.FindSpaceByAlias(ctx, "calm-fox")
	sp.ExpiresAt = f.now.Add(2 * time.Minute)
	s.Repo.(*mem).spaces[sp.Alias] = sp
	x := input(tok)
	x.TTL = time.Hour
	r, e := s.CreateLink(ctx, x)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Link.ExpiresAt.Equal(sp.ExpiresAt) {
		t.Fatal("link outlives space")
	}
	s.Audit = nil
	if e = s.DeleteSpace(ctx, DeleteSpaceInput{Alias: "calm-fox", Force: true, Reason: "ops"}); e == nil {
		t.Fatal("audit unavailable")
	}
}
