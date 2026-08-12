package libsql

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func space(id, alias string, expiry time.Time) domain.Space {
	a, e := domain.ParseAlias(alias)
	if e != nil {
		panic(e)
	}
	tok, _ := domain.NewToken()
	return domain.Space{ID: domain.SpaceID(id), Alias: a, TokenHash: tok.Hash(), ExpiresAt: expiry}
}
func link(id, sid, name string, expiry time.Time) domain.Link {
	n, e := domain.ParseLinkName(name)
	if e != nil {
		panic(e)
	}
	return domain.Link{ID: domain.LinkID(id), SpaceID: domain.SpaceID(sid), Name: n, Status: domain.StatusCreating, ExpiresAt: expiry}
}
func TestOpenMigratesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var n int
	if e = s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&n); e != nil || n != currentSchemaVersion {
		t.Fatalf("version %d %v", n, e)
	}
}
func TestTokenHashOnlyAndSpaceQueries(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()
	tok, _ := domain.NewToken()
	a, _ := domain.ParseAlias("alpha")
	in := domain.Space{ID: "s1", Alias: a, TokenHash: tok.Hash(), ExpiresAt: now.Add(time.Hour)}
	if e := s.CreateSpace(context.Background(), in); e != nil {
		t.Fatal(e)
	}
	got, e := s.FindSpaceByAlias(context.Background(), a)
	if e != nil || !got.TokenHash.Verify(tok) {
		t.Fatal(got, e)
	}
	all, e := s.ListSpaces(context.Background())
	if e != nil || len(all) != 1 {
		t.Fatal(all, e)
	}
	b, e := os.ReadFile(s.dbPath())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), tok.Reveal()) {
		t.Fatal("plaintext token persisted")
	}
	if e = s.CreateSpace(context.Background(), in); !domain.IsKind(e, domain.Conflict) {
		t.Fatalf("want conflict: %v", e)
	}
	_, e = s.FindSpace(context.Background(), "missing")
	if !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
}
func (s *Store) dbPath() string {
	var p string
	_ = s.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &p)
	return p
}
func TestLinksConcurrentUniqueScope(t *testing.T) {
	s := testStore(t)
	c := context.Background()
	now := time.Now()
	for _, x := range []domain.Space{space("s1", "one", now.Add(time.Hour)), space("s2", "two", now.Add(time.Hour))} {
		if e := s.CreateSpace(c, x); e != nil {
			t.Fatal(e)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = s.CreateLink(c, link(domainID(i), "s1", "api", now.Add(time.Hour)))
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else if !domain.IsKind(e, domain.Conflict) {
			t.Fatal(e)
		}
	}
	if ok != 1 {
		t.Fatal(errs)
	}
	if e := s.CreateLink(c, link("other", "s2", "api", now.Add(time.Hour))); e != nil {
		t.Fatal(e)
	}
	links, err := s.ListLinks(c, "s1")
	if err != nil || len(links) != 1 || links[0].Name != "api" {
		t.Fatalf("list: %#v %v", links, err)
	}
	links, err = s.ListLinks(c, "none")
	if err != nil || len(links) != 0 {
		t.Fatalf("empty list: %#v %v", links, err)
	}
}
func domainID(i int) string {
	if i == 0 {
		return "a"
	}
	return "b"
}
func TestDeleteSpaceArchivesLinksTransactionally(t *testing.T) {
	s := testStore(t)
	c := context.Background()
	now := time.Now()
	if e := s.CreateSpace(c, space("s", "alpha", now.Add(time.Hour))); e != nil {
		t.Fatal(e)
	}
	if e := s.CreateLink(c, link("l", "s", "api", now.Add(time.Hour))); e != nil {
		t.Fatal(e)
	}
	if e := s.DeleteSpace(c, "s"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.FindSpace(c, "s"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
	if _, e := s.FindLink(c, "s", "api"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM links WHERE space_id='s' AND deleted_at IS NOT NULL AND status='deleted'`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if e := s.DeleteSpace(c, "s"); e != nil {
		t.Fatal(e)
	}
}
func TestWithinRollbackAndSaveDelete(t *testing.T) {
	s := testStore(t)
	c := context.Background()
	now := time.Now()
	sp := space("s", "alpha", now.Add(time.Hour))
	if e := s.CreateSpace(c, sp); e != nil {
		t.Fatal(e)
	}
	want := link("l", "s", "api", now.Add(time.Hour))
	boom := s.Within(c, func(c context.Context, r ports.Repository) error {
		if e := r.CreateLink(c, want); e != nil {
			return e
		}
		return os.ErrInvalid
	})
	if boom == nil {
		t.Fatal("expected rollback")
	}
	if _, e := s.FindLink(c, "s", "api"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
	if e := s.CreateLink(c, want); e != nil {
		t.Fatal(e)
	}
	want.Status = domain.StatusFailed
	if e := s.SaveLink(c, want); e != nil {
		t.Fatal(e)
	}
	missing := want
	missing.ID = "missing"
	if e := s.SaveLink(c, missing); !domain.IsKind(e, domain.NotFound) {
		t.Fatalf("missing save: %v", e)
	}
	got, e := s.FindLink(c, "s", "api")
	if e != nil || got.Status != domain.StatusFailed {
		t.Fatal(got, e)
	}
	if e := s.DeleteLink(c, "l"); e != nil {
		t.Fatal(e)
	}
	if e := s.DeleteLink(c, "l"); !domain.IsKind(e, domain.NotFound) {
		t.Fatal(e)
	}
}
func TestExpiryReconcileAndAudit(t *testing.T) {
	s := testStore(t)
	c := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if e := s.CreateSpace(c, space("s", "alpha", now.Add(time.Hour))); e != nil {
		t.Fatal(e)
	}
	for _, x := range []domain.Link{link("old", "s", "old", now), link("new", "s", "new", now.Add(time.Nanosecond)), link("dead", "s", "dead", now.Add(time.Hour))} {
		if e := s.CreateLink(c, x); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.DeleteLink(c, "dead"); e != nil {
		t.Fatal(e)
	}
	exp, e := s.ExpiredLinks(c, now)
	if e != nil || len(exp) != 1 || exp[0].ID != "old" {
		t.Fatal(exp, e)
	}
	rec, e := s.ReconciliationLinks(c, now)
	if e != nil || len(rec) != 1 || rec[0].ID != "new" {
		t.Fatal(rec, e)
	}
	if e := s.Record(c, ports.AuditEvent{At: now, SpaceID: "s", Action: "force_delete", Reason: "ticket"}); e != nil {
		t.Fatal(e)
	}
	if e := s.Record(c, ports.AuditEvent{}); !domain.IsKind(e, domain.Validation) {
		t.Fatal(e)
	}
	if e := s.Record(c, ports.AuditEvent{Reason: strings.Repeat("x", 1025)}); !domain.IsKind(e, domain.Validation) {
		t.Fatal(e)
	}
}
func TestUnsupportedMigrationAndClosedDB(t *testing.T) {
	s := testStore(t)
	if _, e := s.db.Exec(`INSERT INTO schema_migrations(version) VALUES(99)`); e != nil {
		t.Fatal(e)
	}
	p := s.dbPath()
	s.Close()
	if _, e := Open(p); e == nil || !strings.Contains(e.Error(), "unsupported") {
		t.Fatal(e)
	}
	if e := s.CreateSpace(context.Background(), space("x", "beta", time.Now())); e == nil {
		t.Fatal("closed database accepted write")
	}
	if _, e := Open(""); e == nil {
		t.Fatal("empty path")
	}
	parent := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(parent, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(filepath.Join(parent, "x.db")); e == nil {
		t.Fatal("unavailable parent accepted")
	}
}
func TestConstraintAndSQLShape(t *testing.T) {
	s := testStore(t)
	if _, e := s.db.Exec(`INSERT INTO spaces(id,alias,token_hash,expires_at,created_at) VALUES('bad','bad',X'01',0,0)`); e != nil {
		t.Fatal(e)
	}
	_, e := s.FindSpace(context.Background(), "bad")
	if e == nil || !strings.Contains(e.Error(), "invalid token hash") {
		t.Fatal(e)
	}
	_ = sql.ErrNoRows
}
