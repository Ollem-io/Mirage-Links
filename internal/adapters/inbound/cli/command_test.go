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

func TestInitAdminTokenAtomicModesNoOverwrite(t *testing.T) {
	d := t.TempDir()
	tokenPath, hashPath := filepath.Join(d, "admin.token"), filepath.Join(d, "admin.sha256")
	if err := initAdminToken(tokenPath, hashPath); err != nil {
		t.Fatal(err)
	}
	tok, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hash), "mir_admin_") || !strings.HasPrefix(string(tok), "mir_admin_") {
		t.Fatal("credential leaked or malformed")
	}
	if len(strings.TrimSpace(string(hash))) != 64 {
		t.Fatalf("hash=%q", hash)
	}
	for p, want := range map[string]os.FileMode{tokenPath: 0600, hashPath: 0640} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s mode=%v err=%v", p, fi.Mode().Perm(), err)
		}
	}
	before := append([]byte(nil), tok...)
	if err := initAdminToken(tokenPath, filepath.Join(d, "other.sha256")); err == nil {
		t.Fatal("overwrote existing token")
	}
	after, _ := os.ReadFile(tokenPath)
	if !bytes.Equal(before, after) {
		t.Fatal("token changed")
	}
}

func TestInitAdminTokenRollsBackTokenWhenHashCannotBeCreated(t *testing.T) {
	d := t.TempDir()
	tokenPath := filepath.Join(d, "admin.token")
	if err := initAdminToken(tokenPath, filepath.Join(d, "missing", "admin.sha256")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Lstat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("token not rolled back: %v", err)
	}
}

func TestInitAdminTokenRejectsExistingAndNonRegularPaths(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := initAdminToken(link, filepath.Join(d, "hash")); err == nil {
		t.Fatal("accepted symlink")
	}
	dir := filepath.Join(d, "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := initAdminToken(dir, filepath.Join(d, "hash")); err == nil {
		t.Fatal("accepted existing directory")
	}
}
