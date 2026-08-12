// Package architecture contains repository-level dependency boundary tests.
package architecture

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCorePackagesDoNotDependOnAdapters(t *testing.T) {
	t.Parallel()
	core := []string{
		"github.com/primeintellect/mirage/internal/domain",
		"github.com/primeintellect/mirage/internal/application",
	}
	for _, pkg := range core {
		pkg := pkg
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()
			output, err := exec.Command("go", "list", "-f", "{{range .Deps}}{{.}} {{end}}", pkg).CombinedOutput()
			if err != nil {
				t.Fatalf("go list %s: %v\n%s", pkg, err, output)
			}
			for _, dependency := range strings.Fields(string(output)) {
				if strings.HasPrefix(dependency, "github.com/primeintellect/mirage/internal/adapters/") {
					t.Fatalf("%s illegally depends on adapter %s", pkg, dependency)
				}
			}
		})
	}
}
