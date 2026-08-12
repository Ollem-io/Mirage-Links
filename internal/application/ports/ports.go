// Package ports declares the application boundary; adapters implement these interfaces.
package ports

import (
	"context"
	"github.com/primeintellect/mirage/internal/domain"
	"time"
)

type Repository interface {
	FindSpace(context.Context, domain.SpaceID) (domain.Space, error)
	SaveSpace(context.Context, domain.Space) error
	FindLink(context.Context, domain.SpaceID, domain.LinkName) (domain.Link, error)
	SaveLink(context.Context, domain.Link) error
}
type UnitOfWork interface {
	Within(context.Context, func(context.Context, Repository) error) error
}
type TokenGenerator interface{ Generate() (domain.Token, error) }
type Clock interface{ Now() time.Time }
type Proxy interface {
	Add(context.Context, domain.Link, string) error
	Remove(context.Context, domain.LinkID) error
}
type Process interface {
	Start(context.Context, domain.Link) error
	Stop(context.Context, domain.LinkID) error
}
type Health interface {
	Check(context.Context, domain.HealthCheck) error
}
type LogStream interface {
	Tail(context.Context, domain.LinkID, int) ([]Entry, error)
}
type Entry struct {
	At     time.Time
	Stream string
	Text   string
}
