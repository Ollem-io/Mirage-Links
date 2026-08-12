// Package cli implements the small bootstrap command surface.
//
// Product commands are deliberately added in later milestones. Keeping command
// parsing here lets main remain a thin, injectable process boundary.
package cli

import (
	"fmt"
	"io"
	"strings"
)

const usage = `Mirage manages temporary local application environments.

Usage:
  mirage [--help] [--version]
  mirage version

Commands:
  version     Print Mirage version information

Use "mirage --help" for this help text.
`

// VersionSource returns version metadata. It is a function to make command
// behavior independent of linker state in tests.
type VersionSource func() string

// Command parses bootstrap arguments and writes only to injected streams.
type Command struct {
	stdout  io.Writer
	stderr  io.Writer
	version VersionSource
}

// New constructs a command with explicit process dependencies.
func New(stdout, stderr io.Writer, version VersionSource) Command {
	return Command{stdout: stdout, stderr: stderr, version: version}
}

// Execute runs a command and returns the process exit status. It never calls
// os.Exit, making both success and failure paths testable in-process.
func (c Command) Execute(args []string) int {
	if len(args) == 0 {
		c.writeUsage(c.stdout)
		return 0
	}

	switch args[0] {
	case "--help", "-h", "help":
		if len(args) != 1 {
			return c.usageError("help does not accept arguments")
		}
		c.writeUsage(c.stdout)
		return 0
	case "--version", "-v", "version":
		if len(args) != 1 {
			return c.usageError("version does not accept arguments")
		}
		_, _ = fmt.Fprintf(c.stdout, "mirage %s\n", c.version())
		return 0
	default:
		return c.usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func (c Command) usageError(message string) int {
	_, _ = fmt.Fprintf(c.stderr, "mirage: %s\n", message)
	_, _ = fmt.Fprint(c.stderr, "Run 'mirage --help' for usage.\n")
	return 2
}

func (c Command) writeUsage(w io.Writer) {
	_, _ = io.WriteString(w, strings.TrimPrefix(usage, "\n"))
}
