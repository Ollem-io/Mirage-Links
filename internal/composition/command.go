// Package composition wires adapters at the executable boundary.
package composition

import (
	"io"

	"github.com/primeintellect/mirage/internal/adapters/inbound/cli"
	"github.com/primeintellect/mirage/internal/buildinfo"
)

// NewCommand supplies production dependencies to the inbound CLI adapter.
func NewCommand(stdout, stderr io.Writer) cli.Command {
	return cli.New(stdout, stderr, func() string { return buildinfo.Version })
}
