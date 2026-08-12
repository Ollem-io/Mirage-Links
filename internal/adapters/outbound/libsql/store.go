// Package libsql implements Mirage's repository ports on an embedded libSQL database.
package libsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	_ "github.com/tursodatabase/go-libsql"
)

const currentSchemaVersion = 1

// Embedded libSQL applies PRAGMA foreign_keys per connection. A single owned
// connection makes that invariant durable for every operation (and avoids a
// pool silently issuing work on a connection without foreign-key enforcement).
var openMigrationMu sync.Mutex

// Store owns an embedded libSQL connection. It is safe for concurrent use.
type Store struct{ db *sql.DB }

// Open opens path and applies all migrations before returning. It intentionally
// creates parent directories, but rejects a path whose existing parent is not a directory.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("libsql: empty database path")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("libsql: create data directory: %w", err)
	}
	// The go-libsql driver opens file: URLs as local embedded libSQL databases.
	db, err := sql.Open("libsql", "file:"+path)
	if err != nil {
		return nil, fmt.Errorf("libsql: connect: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("libsql: enable foreign keys: %w", err)
	}
	// Serializes in-process simultaneous Open calls before schema DDL. The
	// migration transaction remains safe against other processes.
	openMigrationMu.Lock()
	defer openMigrationMu.Unlock()
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("libsql: begin migration: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY NOT NULL)`); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return err
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("libsql: unsupported schema version %d", version)
	}
	if version < 1 {
		// Archive fields preserve lifecycle history while normal reads exclude deleted spaces.
		stmts := []string{
			`CREATE TABLE spaces (id TEXT PRIMARY KEY NOT NULL, alias TEXT NOT NULL UNIQUE, token_hash BLOB NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL, deleted_at INTEGER)`,
			`CREATE TABLE links (id TEXT PRIMARY KEY NOT NULL, space_id TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE, name TEXT NOT NULL, status TEXT NOT NULL, command TEXT NOT NULL, folder TEXT NOT NULL, health_method TEXT NOT NULL, health_url TEXT NOT NULL, grace_ns INTEGER NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, allocated_port INTEGER, process_identity TEXT, restart_count INTEGER NOT NULL DEFAULT 0, next_restart_at INTEGER, deleted_at INTEGER, UNIQUE(space_id, name))`,
			`CREATE INDEX links_by_expiry ON links(expires_at)`,
			`CREATE INDEX links_by_reconcile ON links(status, expires_at)`,
			`CREATE TABLE audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, space_id TEXT NOT NULL, link_id TEXT, action TEXT NOT NULL, reason TEXT NOT NULL)`,
			`INSERT INTO schema_migrations(version) VALUES (1)`,
		}
		for _, stmt := range stmts {
			if _, err = tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("libsql: migrate v1: %w", err)
			}
		}
	}
	return tx.Commit()
}

type runner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type repository struct{ q runner }

func (s *Store) Within(ctx context.Context, fn func(context.Context, ports.Repository) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(ctx, repository{q: tx}); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CreateSpace(c context.Context, v domain.Space) error {
	return repository{s.db}.CreateSpace(c, v)
}
func (s *Store) FindSpace(c context.Context, id domain.SpaceID) (domain.Space, error) {
	return repository{s.db}.FindSpace(c, id)
}
func (s *Store) FindSpaceByAlias(c context.Context, a domain.Alias) (domain.Space, error) {
	return repository{s.db}.FindSpaceByAlias(c, a)
}
func (s *Store) ListSpaces(c context.Context) ([]domain.Space, error) {
	return repository{s.db}.ListSpaces(c)
}
func (s *Store) DeleteSpace(c context.Context, id domain.SpaceID) error {
	return s.Within(c, func(c context.Context, r ports.Repository) error { return r.DeleteSpace(c, id) })
}
func (s *Store) CreateLink(c context.Context, v domain.Link) error {
	return repository{s.db}.CreateLink(c, v)
}
func (s *Store) FindLink(c context.Context, sid domain.SpaceID, n domain.LinkName) (domain.Link, error) {
	return repository{s.db}.FindLink(c, sid, n)
}
func (s *Store) ListLinks(c context.Context, sid domain.SpaceID) ([]domain.Link, error) {
	return repository{s.db}.ListLinks(c, sid)
}
func (s *Store) SaveLink(c context.Context, v domain.Link) error {
	return repository{s.db}.SaveLink(c, v)
}
func (s *Store) DeleteLink(c context.Context, id domain.LinkID) error {
	return repository{s.db}.DeleteLink(c, id)
}

func (r repository) CreateSpace(c context.Context, v domain.Space) error {
	_, err := r.q.ExecContext(c, `INSERT INTO spaces(id,alias,token_hash,expires_at,created_at) VALUES(?,?,?,?,?)`, v.ID, v.Alias, v.TokenHash[:], unix(v.ExpiresAt), time.Now().UTC().UnixNano())
	return translate(err, "space already exists")
}
func scanSpace(row interface{ Scan(...any) error }) (domain.Space, error) {
	var x domain.Space
	var h []byte
	var expiry int64
	err := row.Scan(&x.ID, &x.Alias, &h, &expiry)
	if err != nil {
		return x, translate(err, "space not found")
	}
	if len(h) != len(x.TokenHash) {
		return x, fmt.Errorf("libsql: invalid token hash")
	}
	copy(x.TokenHash[:], h)
	x.ExpiresAt = time.Unix(0, expiry).UTC()
	return x, nil
}
func (r repository) FindSpace(c context.Context, id domain.SpaceID) (domain.Space, error) {
	return scanSpace(r.q.QueryRowContext(c, `SELECT id,alias,token_hash,expires_at FROM spaces WHERE id=? AND deleted_at IS NULL`, id))
}
func (r repository) FindSpaceByAlias(c context.Context, a domain.Alias) (domain.Space, error) {
	return scanSpace(r.q.QueryRowContext(c, `SELECT id,alias,token_hash,expires_at FROM spaces WHERE alias=? AND deleted_at IS NULL`, a))
}
func (r repository) ListSpaces(c context.Context) ([]domain.Space, error) {
	rows, e := r.q.QueryContext(c, `SELECT id,alias,token_hash,expires_at FROM spaces WHERE deleted_at IS NULL ORDER BY alias`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Space{}
	for rows.Next() {
		v, e := scanSpace(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteSpace archives its links and space atomically. It is idempotent for an already archived ID.
func (r repository) DeleteSpace(c context.Context, id domain.SpaceID) error {
	now := time.Now().UTC().UnixNano()
	res, e := r.q.ExecContext(c, `UPDATE spaces SET deleted_at=? WHERE id=? AND deleted_at IS NULL`, now, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	_, e = r.q.ExecContext(c, `UPDATE links SET status=?,deleted_at=?,updated_at=? WHERE space_id=? AND deleted_at IS NULL`, domain.StatusDeleted, now, now, id)
	return e
}
func (r repository) CreateLink(c context.Context, v domain.Link) error {
	now := time.Now().UTC().UnixNano()
	// Embedded SQLite permits a single writer. Briefly retry a lock caused by
	// concurrent requests; the unique constraint remains the authoritative
	// idempotency/concurrency decision.
	var e error
	for attempt := 0; attempt < 8; attempt++ {
		_, e = r.q.ExecContext(c, `INSERT INTO links(id,space_id,name,status,command,folder,health_method,health_url,grace_ns,expires_at,created_at,updated_at,allocated_port,process_identity,restart_count,next_restart_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, v.ID, v.SpaceID, v.Name, v.Status, v.Command, v.Folder, v.HealthCheck.Method, v.HealthCheck.URL, int64(v.Grace), unix(v.ExpiresAt), now, now, nullablePort(v.AllocatedPort), nullableString(v.ProcessIdentity), v.RestartCount, nullableTime(v.NextRestartAt))
		if e == nil || !contains(e.Error(), "database is locked") {
			break
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(time.Duration(attempt+1) * time.Millisecond):
		}
	}
	return translate(e, "link name already exists")
}
func scanLink(row interface{ Scan(...any) error }) (domain.Link, error) {
	var x domain.Link
	var expiry, grace int64
	var port sql.NullInt64
	var process sql.NullString
	var next sql.NullInt64
	e := row.Scan(&x.ID, &x.SpaceID, &x.Name, &x.Status, &x.Command, &x.Folder, &x.HealthCheck.Method, &x.HealthCheck.URL, &grace, &expiry, &port, &process, &x.RestartCount, &next)
	if e != nil {
		return x, translate(e, "link not found")
	}
	x.Grace, x.ExpiresAt = time.Duration(grace), time.Unix(0, expiry).UTC()
	if port.Valid {
		x.AllocatedPort = int(port.Int64)
	}
	if process.Valid {
		x.ProcessIdentity = process.String
	}
	if next.Valid {
		x.NextRestartAt = time.Unix(0, next.Int64).UTC()
	}
	return x, nil
}
func nullablePort(p int) any {
	if p == 0 {
		return nil
	}
	return p
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return unix(t)
}

func (r repository) FindLink(c context.Context, s domain.SpaceID, n domain.LinkName) (domain.Link, error) {
	return scanLink(r.q.QueryRowContext(c, `SELECT id,space_id,name,status,command,folder,health_method,health_url,grace_ns,expires_at,allocated_port,process_identity,restart_count,next_restart_at FROM links WHERE space_id=? AND name=? AND deleted_at IS NULL`, s, n))
}
func (r repository) ListLinks(c context.Context, s domain.SpaceID) ([]domain.Link, error) {
	rows, e := r.q.QueryContext(c, `SELECT id,space_id,name,status,command,folder,health_method,health_url,grace_ns,expires_at,allocated_port,process_identity,restart_count,next_restart_at FROM links WHERE space_id=? AND deleted_at IS NULL ORDER BY name`, s)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Link{}
	for rows.Next() {
		v, e := scanLink(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r repository) SaveLink(c context.Context, v domain.Link) error {
	res, e := r.q.ExecContext(c, `UPDATE links SET status=?,command=?,folder=?,health_method=?,health_url=?,grace_ns=?,expires_at=?,updated_at=?,allocated_port=?,process_identity=?,restart_count=?,next_restart_at=? WHERE id=? AND deleted_at IS NULL`, v.Status, v.Command, v.Folder, v.HealthCheck.Method, v.HealthCheck.URL, int64(v.Grace), unix(v.ExpiresAt), time.Now().UTC().UnixNano(), nullablePort(v.AllocatedPort), nullableString(v.ProcessIdentity), v.RestartCount, nullableTime(v.NextRestartAt), v.ID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.NewNotFound("link not found")
	}
	return nil
}
func (r repository) DeleteLink(c context.Context, id domain.LinkID) error {
	res, e := r.q.ExecContext(c, `UPDATE links SET status=?,deleted_at=?,updated_at=? WHERE id=? AND deleted_at IS NULL`, domain.StatusDeleted, time.Now().UTC().UnixNano(), time.Now().UTC().UnixNano(), id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.NewNotFound("link not found")
	}
	return nil
}

// ActiveSpaces returns non-archived spaces whose expiry is strictly after now.
func (s *Store) ActiveSpaces(c context.Context, now time.Time) ([]domain.Space, error) {
	return querySpaces(c, s.db, `SELECT id,alias,token_hash,expires_at FROM spaces WHERE deleted_at IS NULL AND expires_at>? ORDER BY expires_at,id`, unix(now))
}

// ExpiredSpaces returns non-archived spaces expiring at or before now.
func (s *Store) ExpiredSpaces(c context.Context, now time.Time) ([]domain.Space, error) {
	return querySpaces(c, s.db, `SELECT id,alias,token_hash,expires_at FROM spaces WHERE deleted_at IS NULL AND expires_at<=? ORDER BY expires_at,id`, unix(now))
}
func querySpaces(c context.Context, q runner, stmt string, args ...any) ([]domain.Space, error) {
	rows, e := q.QueryContext(c, stmt, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.Space
	for rows.Next() {
		x, e := scanSpace(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ExpiredLinks returns live links whose expiry is at or before now, ordered to make cleanup deterministic.
func (s *Store) ExpiredLinks(c context.Context, now time.Time) ([]domain.Link, error) {
	return queryLinks(c, s.db, `SELECT id,space_id,name,status,command,folder,health_method,health_url,grace_ns,expires_at,allocated_port,process_identity,restart_count,next_restart_at FROM links WHERE deleted_at IS NULL AND expires_at<=? ORDER BY expires_at,id`, unix(now))
}

// ReconciliationLinks returns live, nonterminal links deterministically.
func (s *Store) ReconciliationLinks(c context.Context, now time.Time) ([]domain.Link, error) {
	return queryLinks(c, s.db, `SELECT id,space_id,name,status,command,folder,health_method,health_url,grace_ns,expires_at,allocated_port,process_identity,restart_count,next_restart_at FROM links WHERE deleted_at IS NULL AND expires_at>? AND status NOT IN (?,?) ORDER BY id`, unix(now), domain.StatusDeleted, domain.StatusExpired)
}
func queryLinks(c context.Context, q runner, stmt string, args ...any) ([]domain.Link, error) {
	rows, e := q.QueryContext(c, stmt, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []domain.Link
	for rows.Next() {
		x, e := scanLink(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RecordAudit persists bounded reason metadata. Reasons are capped so audit input cannot grow the database unboundedly.
func (s *Store) Record(c context.Context, e ports.AuditEvent) error {
	if len(e.Reason) == 0 {
		return domain.NewValidation("reason", "must not be empty")
	}
	if len(e.Reason) > 1024 {
		return domain.NewValidation("reason", "must be at most 1024 bytes")
	}
	_, err := s.db.ExecContext(c, `INSERT INTO audit_events(at,space_id,link_id,action,reason) VALUES(?,?,?,?,?)`, unix(e.At), e.SpaceID, e.LinkID, e.Action, e.Reason)
	return err
}
func unix(t time.Time) int64 { return t.UTC().UnixNano() }
func translate(err error, msg string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewNotFound(msg)
	}
	if err != nil { // libSQL error text is stable across sqlite constraint variants.
		if contains(err.Error(), "UNIQUE constraint failed") || contains(err.Error(), "constraint failed") {
			return domain.NewConflict(msg)
		}
	}
	return err
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ ports.Repository = (*Store)(nil)
var _ ports.UnitOfWork = (*Store)(nil)
var _ ports.Audit = (*Store)(nil)
