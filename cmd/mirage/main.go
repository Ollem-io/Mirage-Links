// Command mirage is the Mirage command-line executable.
package main

import (
	"io"
	"os"

	"github.com/primeintellect/mirage/internal/composition"
)

// run creates the command with injectable streams and returns a process exit
// status. main is the sole process-exit boundary.
func run(args []string, stdout, stderr io.Writer) int {
	return composition.NewCommand(stdout, stderr).Execute(args)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
