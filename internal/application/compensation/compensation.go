// Package compensation centralizes the irreversible side-effect cleanup order.
package compensation

import "context"

// Steps are optional idempotent compensators. Run always attempts every present
// step in route, process, port order and returns the first error.
type Steps struct {
	RemoveRoute func(context.Context) error
	StopProcess func(context.Context) error
	ReleasePort func(context.Context) error
}

func Run(ctx context.Context, s Steps) error {
	var first error
	for _, fn := range []func(context.Context) error{s.RemoveRoute, s.StopProcess, s.ReleasePort} {
		if fn != nil {
			if err := fn(ctx); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}
