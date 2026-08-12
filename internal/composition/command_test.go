package composition

import (
	"bytes"
	"strings"
	"testing"

	"github.com/primeintellect/mirage/internal/buildinfo"
)

func TestNewCommandUsesBuildVersion(t *testing.T) {
	original := buildinfo.Version
	buildinfo.Version = "test-build"
	t.Cleanup(func() { buildinfo.Version = original })

	var stdout, stderr bytes.Buffer
	exit := NewCommand(&stdout, &stderr).Execute([]string{"version"})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if got := stdout.String(); got != "mirage test-build\n" {
		t.Fatalf("stdout = %q", got)
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
