package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecuteSuccess(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty shows help", nil, "Usage:"},
		{"long help", []string{"--help"}, "Mirage manages"},
		{"short help", []string{"-h"}, "Commands:"},
		{"help command", []string{"help"}, "Usage:"},
		{"version command", []string{"version"}, "mirage test-version\n"},
		{"long version", []string{"--version"}, "mirage test-version\n"},
		{"short version", []string{"-v"}, "mirage test-version\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := New(&stdout, &stderr, func() string { return "test-version" })
			if got := command.Execute(tc.args); got != 0 {
				t.Fatalf("Execute() exit = %d, want 0", got)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout %q does not contain %q", stdout.String(), tc.want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestExecuteFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown", []string{"spaces"}, `unknown command "spaces"`},
		{"help argument", []string{"help", "extra"}, "help does not accept arguments"},
		{"version argument", []string{"version", "extra"}, "version does not accept arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := New(&stdout, &stderr, func() string { return "ignored" })
			if got := command.Execute(tc.args); got != 2 {
				t.Fatalf("Execute() exit = %d, want 2", got)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), "mirage --help") {
				t.Fatalf("stderr = %q, want diagnostic and help hint", stderr.String())
			}
		})
	}
}

func TestLoadConfigExternalAdvertisement(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.yaml")
	if err := os.WriteFile(p, []byte("base_host: temp.lab.ollem.io\nexternal_scheme: HTTPS\nexternal_port: 8443\ndashboard_ssl: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(p, func(string) string { return "" }, os.Getwd)
	if err != nil || c.ExternalScheme != "https" || c.ExternalPort != 8443 || !c.DashboardSSL {
		t.Fatalf("%+v %v", c, err)
	}
	if err := os.WriteFile(p, []byte("external_scheme: ftp\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadConfig(p, func(string) string { return "" }, os.Getwd); err == nil {
		t.Fatal("accepted scheme")
	}
	if err := os.WriteFile(p, []byte("external_port: 65536\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadConfig(p, func(string) string { return "" }, os.Getwd); err == nil {
		t.Fatal("accepted port")
	}
}
