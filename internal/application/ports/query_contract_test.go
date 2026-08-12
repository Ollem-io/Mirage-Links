package ports_test

import (
	"context"
	"time"

	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
)

// queryContractFake is intentionally independent of any storage adapter. These
// assignments make the active/expiry/reconciliation surface an application port
// contract and prevent a concrete adapter-only API from satisfying MIR-03.
type queryContractFake struct{}

func (queryContractFake) CreateSpace(context.Context, domain.Space) error { return nil }
func (queryContractFake) FindSpace(context.Context, domain.SpaceID) (domain.Space, error) {
	return domain.Space{}, nil
}
func (queryContractFake) FindSpaceByAlias(context.Context, domain.Alias) (domain.Space, error) {
	return domain.Space{}, nil
}
func (queryContractFake) ListSpaces(context.Context) ([]domain.Space, error) { return nil, nil }
func (queryContractFake) ActiveSpaces(context.Context, time.Time) ([]domain.Space, error) {
	return nil, nil
}
func (queryContractFake) ExpiredSpaces(context.Context, time.Time) ([]domain.Space, error) {
	return nil, nil
}
func (queryContractFake) DeleteSpace(context.Context, domain.SpaceID) error { return nil }
func (queryContractFake) CreateLink(context.Context, domain.Link) error     { return nil }
func (queryContractFake) FindLink(context.Context, domain.SpaceID, domain.LinkName) (domain.Link, error) {
	return domain.Link{}, nil
}
func (queryContractFake) ListLinks(context.Context, domain.SpaceID) ([]domain.Link, error) {
	return nil, nil
}
func (queryContractFake) SaveLink(context.Context, domain.Link) error { return nil }
func (queryContractFake) ExpiredLinks(context.Context, time.Time) ([]domain.Link, error) {
	return nil, nil
}
func (queryContractFake) ReconciliationLinks(context.Context, time.Time) ([]domain.Link, error) {
	return nil, nil
}
func (queryContractFake) DeleteLink(context.Context, domain.LinkID) error { return nil }

var _ ports.Repository = queryContractFake{}
