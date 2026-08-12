// Package ports declares stable application boundaries. Implementations belong
// in adapters; no port imports an adapter package.
package ports

import (
	"context"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"time"
)

type SpaceRepository interface {
	CreateSpace(context.Context, domain.Space) error
	FindSpace(context.Context, domain.SpaceID) (domain.Space, error)
	FindSpaceByAlias(context.Context, domain.Alias) (domain.Space, error)
	ListSpaces(context.Context) ([]domain.Space, error)
	ActiveSpaces(context.Context, time.Time) ([]domain.Space, error)
	ExpiredSpaces(context.Context, time.Time) ([]domain.Space, error)
	DeleteSpace(context.Context, domain.SpaceID) error
}
type LinkRepository interface {
	CreateLink(context.Context, domain.Link) error
	FindLink(context.Context, domain.SpaceID, domain.LinkName) (domain.Link, error)
	ListLinks(context.Context, domain.SpaceID) ([]domain.Link, error)
	SaveLink(context.Context, domain.Link) error
	ExpiredLinks(context.Context, time.Time) ([]domain.Link, error)
	ReconciliationLinks(context.Context, time.Time) ([]domain.Link, error)
	DeleteLink(context.Context, domain.LinkID) error
}
type Repository interface {
	SpaceRepository
	LinkRepository
}
type UnitOfWork interface {
	Within(context.Context, func(context.Context, Repository) error) error
}
type Clock interface{ Now() time.Time }
type IDGenerator interface {
	NewSpaceID() domain.SpaceID
	NewLinkID() domain.LinkID
}
type AliasGenerator interface{ NewAlias() (domain.Alias, error) }
type TokenGenerator interface{ Generate() (domain.Token, error) }
type TokenHasher interface {
	Hash(domain.Token) domain.TokenHash
	Verify(domain.TokenHash, domain.Token) bool
}

// PortAllocator reserves a loopback TCP port until Release is called.
type PortAllocator interface {
	Allocate(context.Context) (Port, error)
	Release(context.Context, Port) error
}
type Port struct {
	Number  int
	Address string
}
type ProcessSupervisor interface {
	Start(context.Context, StartRequest) (ProcessIdentity, error)
	Stop(context.Context, ProcessIdentity, time.Duration) error
	Alive(context.Context, ProcessIdentity) (bool, error)
}
type StartRequest struct {
	LinkID      domain.LinkID
	Command     string
	Folder      string
	Port        Port
	Environment map[string]string
}
type ProcessIdentity struct{ Value string }
type HealthChecker interface {
	Check(context.Context, domain.HealthCheck) error
}
type Proxy interface {
	Add(context.Context, Route) error
	Remove(context.Context, domain.LinkID) error
	List(context.Context) ([]Route, error)
}
type Route struct {
	LinkID   domain.LinkID
	Hostname string
	Upstream string
}
type LogSink interface {
	Append(context.Context, domain.LinkID, LogEntry) error
}
type LogStream interface {
	Tail(context.Context, domain.LinkID, int) ([]LogEntry, error)
	Follow(context.Context, domain.LinkID) (io.ReadCloser, error)
}
type LogEntry struct {
	At     time.Time
	Stream string
	Text   string
}
type Audit interface {
	Record(context.Context, AuditEvent) error
}
type AuditEvent struct {
	At      time.Time
	SpaceID domain.SpaceID
	LinkID  domain.LinkID
	Reason  string
	Action  string
}
type Scheduler interface {
	Schedule(context.Context, time.Time, func(context.Context)) (CancelFunc, error)
}
type CancelFunc func()
