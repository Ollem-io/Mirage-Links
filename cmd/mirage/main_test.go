package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"version"}, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d, want 0", got)
	}
	if !strings.HasPrefix(stdout.String(), "mirage ") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
