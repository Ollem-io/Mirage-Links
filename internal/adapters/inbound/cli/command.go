// Package cli implements Mirage's HTTP command-line client.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VersionSource returns linker supplied version metadata.
type VersionSource func() string

// StartOptions are resolved configuration passed to executable composition.
type StartOptions struct {
	PublicAddress, PrivateAddress, ConfigPath, BaseHost, DataPath, CaddyAdmin, CaddyBinary string
	CaddyManaged                                                                           bool
}
type StartFunc func(context.Context, StartOptions) (func() error, error)

type Command struct {
	stdout, stderr io.Writer
	version        VersionSource
	http           *http.Client
	start          StartFunc
	getenv         func(string) string
	getwd          func() (string, error)
	forcedToken    string
}

func New(stdout, stderr io.Writer, version VersionSource) Command {
	return NewWithStart(stdout, stderr, version, nil)
}
func NewWithStart(stdout, stderr io.Writer, version VersionSource, start StartFunc) Command {
	return Command{stdout: stdout, stderr: stderr, version: version, http: &http.Client{Timeout: 30 * time.Second}, start: start, getenv: os.Getenv, getwd: os.Getwd}
}
func (c Command) usageError(s string) int {
	fmt.Fprintf(c.stderr, "mirage: %s\nRun 'mirage --help' for usage.\n", s)
	return 2
}
func (c Command) Execute(args []string) int {
	if len(args) == 0 {
		c.help()
		return 0
	}
	// global switches are accepted before every product command.
	cfgPath, server, globalToken, jsonOut := "", "", "", false
	for len(args) > 0 {
		switch args[0] {
		case "--help", "-h", "help":
			if len(args) == 1 {
				c.help()
				return 0
			}
			return c.usageError("help does not accept arguments")
		case "--version", "-v", "version":
			if len(args) == 1 {
				fmt.Fprintf(c.stdout, "mirage %s\n", c.version())
				return 0
			}
			return c.usageError("version does not accept arguments")
		case "--server":
			if len(args) < 2 {
				return c.usageError("--server requires a value")
			}
			server, args = args[1], args[2:]
		case "--token":
			if len(args) < 2 {
				return c.usageError("--token requires a value")
			}
			globalToken, args = args[1], args[2:]
		case "--config":
			if len(args) < 2 {
				return c.usageError("--config requires a value")
			}
			cfgPath, args = args[1], args[2:]
		case "--json":
			jsonOut = true
			args = args[1:]
		default:
			goto parsed
		}
	}
parsed:
	if len(args) == 0 {
		c.help()
		return 0
	}
	// start also accepts its configuration flag after the command word.
	if args[0] == "start" {
		for i := 1; i+1 < len(args); i += 2 {
			if args[i] == "--config" {
				cfgPath = args[i+1]
			}
		}
	}
	conf, err := loadConfig(cfgPath, c.getenv, c.getwd)
	if err != nil {
		fmt.Fprintln(c.stderr, "mirage:", err)
		return 1
	}
	if server == "" {
		server = c.getenv("MIRAGE_SERVER")
	}
	if server == "" {
		server = conf.PrivateAddress
	}
	if server == "" {
		server = "http://127.0.0.1:9956"
	}
	server = strings.TrimRight(server, "/")
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	c.forcedToken = globalToken
	if args[0] == "start" {
		return c.doStart(args[1:], conf, cfgPath)
	}
	switch args[0] {
	case "space":
		return c.space(args[1:], server, jsonOut, conf)
	case "link":
		return c.link(args[1:], server, jsonOut, conf)
	default:
		return c.usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}
func (c Command) doStart(a []string, conf config, path string) int {
	if c.start == nil {
		return c.fail(fmt.Errorf("start is unavailable"))
	}
	o := StartOptions{PublicAddress: conf.PublicAddress, PrivateAddress: conf.PrivateAddress, ConfigPath: path, BaseHost: conf.BaseHost, DataPath: conf.DataPath, CaddyAdmin: conf.Caddy.AdminURL, CaddyBinary: conf.Caddy.Binary, CaddyManaged: conf.Caddy.Managed}
	for len(a) > 0 {
		if len(a) < 2 {
			return c.usageError(a[0] + " requires a value")
		}
		v := a[1]
		switch a[0] {
		case "--public":
			o.PublicAddress = address(v)
		case "--private":
			o.PrivateAddress = address(v)
		case "--config": /* already resolved globally */
		default:
			return c.usageError("unknown start option " + a[0])
		}
		a = a[2:]
	}
	stop, e := c.start(context.Background(), o)
	if e != nil {
		return c.fail(e)
	}
	fmt.Fprintf(c.stdout, "Mirage ready: private %s public %s\n", o.PrivateAddress, o.PublicAddress)
	// A running server is intentionally foreground. SIGINT/SIGTERM ends it cleanly.
	sig := make(chan os.Signal, 1)
	signalNotify(sig)
	<-sig
	if e = stop(); e != nil {
		return c.fail(e)
	}
	return 0
}
func address(s string) string {
	if strings.Contains(s, ":") {
		return s
	}
	return ":" + s
}
func (c Command) space(a []string, base string, jout bool, conf config) int {
	if len(a) == 0 {
		return c.usageError("space requires a subcommand")
	}
	sub := a[0]
	a = a[1:]
	switch sub {
	case "create":
		ttl, alias := "", ""
		for len(a) > 0 {
			if a[0] == "--json" {
				jout = true
				a = a[1:]
				continue
			}
			if len(a) < 2 {
				return c.usageError(a[0] + " requires a value")
			}
			if a[0] == "--ttl" {
				ttl = a[1]
			} else if a[0] == "--alias" {
				alias = a[1]
			} else {
				return c.usageError("unknown space create option " + a[0])
			}
			a = a[2:]
		}
		return c.request(base, "POST", "/api/v1/spaces", "", map[string]string{"ttl": ttl, "alias": alias}, jout, func(v map[string]any) {
			s, _ := v["space"].(map[string]any)
			fmt.Fprintf(c.stdout, "Alias: %v\nToken: %v\nExpires: %v\n", s["alias"], v["token"], s["expires_at"])
		})
	case "list":
		if len(a) > 0 && a[0] == "--json" {
			jout = true
			a = a[1:]
		}
		path := "/api/v1/spaces"
		if len(a) > 0 {
			if len(a) != 1 {
				return c.usageError("space list accepts at most one alias")
			}
			path += "/" + a[0]
		}
		return c.request(base, "GET", path, "", nil, jout, func(v map[string]any) { c.printRows(v, "spaces", []string{"alias", "expires_at"}) })
	case "delete":
		if len(a) == 0 {
			return c.usageError("space delete requires an alias")
		}
		alias := a[0]
		a = a[1:]
		tok, force, reason := "", false, ""
		for len(a) > 0 {
			if a[0] == "--json" {
				jout = true
				a = a[1:]
				continue
			}
			if a[0] == "--token" && len(a) > 1 {
				tok = a[1]
				a = a[2:]
				continue
			}
			if a[0] == "--force" && len(a) > 1 {
				force = true
				reason = a[1]
				a = a[2:]
				continue
			}
			return c.usageError("invalid space delete option")
		}
		if !force {
			var e error
			tok, e = c.token(tok)
			if e != nil {
				return c.fail(e)
			}
		}
		return c.request(base, "DELETE", "/api/v1/spaces/"+alias, tok, map[string]any{"force": force, "reason": reason}, jout, func(map[string]any) { fmt.Fprintln(c.stdout, "Deleted space", alias) })
	}
	return c.usageError("unknown space command " + sub)
}
func (c Command) link(a []string, base string, jout bool, conf config) int {
	if len(a) == 0 {
		return c.usageError("link requires a subcommand")
	}
	sub := a[0]
	a = a[1:]
	tok := ""
	popToken := func() error { var e error; tok, e = c.takeToken(&a); return e }
	switch sub {
	case "list":
		if e := popToken(); e != nil {
			return c.fail(e)
		}
		if len(a) > 0 && a[0] == "--json" {
			jout = true
			a = a[1:]
		}
		if len(a) > 0 {
			return c.usageError("invalid link list option")
		}
		return c.request(base, "GET", "/api/v1/links", tok, nil, jout, func(v map[string]any) { c.printRows(v, "links", []string{"name", "url", "status", "expires_at"}) })
	case "create":
		m := map[string]any{}
		for len(a) > 0 {
			if a[0] == "--json" {
				jout = true
				a = a[1:]
				continue
			}
			if a[0] == "--restarts" {
				m["restarts"] = true
				a = a[1:]
				continue
			}
			if len(a) < 2 {
				return c.usageError(a[0] + " requires a value")
			}
			k := strings.TrimPrefix(a[0], "--")
			if k == "execution-folder" {
				k = "execution_folder"
			}
			if k != "token" && k != "name" && k != "command" && k != "execution_folder" && k != "health-check" && k != "grace" && k != "ttl" {
				return c.usageError("unknown link create option " + a[0])
			}
			if k == "token" {
				tok = a[1]
			} else if k == "health-check" {
				m["health_check"] = a[1]
			} else {
				m[k] = a[1]
			}
			a = a[2:]
		}
		var e error
		tok, e = c.token(tok)
		if e != nil {
			return c.fail(e)
		}
		return c.request(base, "POST", "/api/v1/links", tok, m, jout, func(v map[string]any) {
			l, _ := v["link"].(map[string]any)
			fmt.Fprintf(c.stdout, "Name: %v\nURL: %v\nStatus: %v\n", l["name"], l["url"], l["status"])
		})
	case "logs":
		if len(a) == 0 {
			return c.usageError("link logs requires a name")
		}
		name := a[0]
		a = a[1:]
		follow := false
		tail := 100
		for len(a) > 0 {
			if a[0] == "--follow" {
				follow = true
				a = a[1:]
				continue
			}
			if a[0] == "--json" {
				jout = true
				a = a[1:]
				continue
			}
			if a[0] == "--token" && len(a) > 1 {
				tok = a[1]
				a = a[2:]
				continue
			}
			if a[0] == "--tail" && len(a) > 1 {
				fmt.Sscan(a[1], &tail)
				a = a[2:]
				continue
			}
			return c.usageError("invalid link logs option")
		}
		var e error
		tok, e = c.token(tok)
		if e != nil {
			return c.fail(e)
		}
		path := fmt.Sprintf("/api/v1/links/%s/logs?tail=%d", name, tail)
		if follow {
			path += "&follow=true"
			return c.follow(base, path, tok)
		}
		return c.request(base, "GET", path, tok, nil, jout, func(v map[string]any) { c.printRows(v, "logs", []string{"at", "stream", "text"}) })
	case "restart", "delete":
		if len(a) == 0 {
			return c.usageError("link " + sub + " requires a name")
		}
		name := a[0]
		a = a[1:]
		if e := popToken(); e != nil {
			return c.fail(e)
		}
		method, path := "DELETE", "/api/v1/links/"+name
		if sub == "restart" {
			method = "POST"
			path += "/restart"
		}
		return c.request(base, method, path, tok, nil, jout, func(v map[string]any) {
			if sub == "delete" {
				fmt.Fprintln(c.stdout, "Deleted link", name)
			} else {
				fmt.Fprintln(c.stdout, "Restarted link", name)
			}
		})
	}
	return c.usageError("unknown link command " + sub)
}
func (c Command) takeToken(a *[]string) (string, error) {
	var token string
	for len(*a) > 0 && (*a)[0] == "--token" {
		if len(*a) < 2 {
			return "", fmt.Errorf("--token requires a value")
		}
		token = (*a)[1]
		*a = (*a)[2:]
	}
	return c.token(token)
}
func (c Command) token(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if strings.TrimSpace(c.forcedToken) != "" {
		return c.forcedToken, nil
	}
	if x := strings.TrimSpace(c.getenv("MIRAGE_TOKEN")); x != "" {
		return x, nil
	}
	wd, e := c.getwd()
	if e != nil {
		return "", e
	}
	p := filepath.Join(wd, ".mirage_token")
	b, e := os.ReadFile(p)
	if e != nil {
		return "", fmt.Errorf("token required: provide --token, MIRAGE_TOKEN, or ./.mirage_token")
	}
	if info, e := os.Stat(p); e == nil && info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(c.stderr, "mirage: warning: %s is group/world-readable\n", p)
	}
	if x := strings.TrimSpace(string(b)); x != "" {
		return x, nil
	}
	return "", fmt.Errorf("token required: ./.mirage_token is empty")
}
func (c Command) request(base, method, path, tok string, payload any, jout bool, human func(map[string]any)) int {
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, base+path, body)
	if e != nil {
		return c.fail(e)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, e := c.http.Do(req)
	if e != nil {
		return c.fail(e)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return c.apiFail(res.StatusCode, raw)
	}
	if len(raw) == 0 {
		if jout {
			fmt.Fprintln(c.stdout, "{}")
		}
		human(map[string]any{})
		return 0
	}
	if jout {
		c.stdout.Write(append(bytes.TrimSpace(raw), '\n'))
		return 0
	}
	var v map[string]any
	if e = json.Unmarshal(raw, &v); e != nil {
		return c.fail(e)
	}
	human(v)
	return 0
}
func (c Command) follow(base, path, tok string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if e != nil {
		return c.fail(e)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	res, e := c.http.Do(req)
	if e != nil {
		return c.fail(e)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(res.Body)
		return c.apiFail(res.StatusCode, b)
	}
	_, e = io.Copy(c.stdout, res.Body)
	if e != nil {
		return c.fail(e)
	}
	return 0
}
func (c Command) apiFail(status int, b []byte) int {
	var x struct{ Code, Message string }
	if json.Unmarshal(b, &x) == nil && x.Code != "" {
		fmt.Fprintf(c.stderr, "mirage: API error (%d %s): %s\n", status, x.Code, x.Message)
	} else {
		fmt.Fprintf(c.stderr, "mirage: API error: HTTP %d\n", status)
	}
	return 1
}
func (c Command) fail(e error) int { fmt.Fprintln(c.stderr, "mirage:", e); return 1 }
func (c Command) printRows(v map[string]any, key string, fields []string) {
	if one, ok := v[key[:len(key)-1]].(map[string]any); ok {
		for _, f := range fields {
			if x, ok := one[f]; ok {
				fmt.Fprintf(c.stdout, "%s: %v\n", strings.Title(strings.ReplaceAll(f, "_", " ")), x)
			}
		}
		return
	}
	for _, x := range v[key].([]any) {
		m, _ := x.(map[string]any)
		for i, f := range fields {
			if i > 0 {
				fmt.Fprint(c.stdout, "\t")
			}
			fmt.Fprint(c.stdout, m[f])
		}
		fmt.Fprintln(c.stdout)
	}
}
func (c Command) help() {
	fmt.Fprint(c.stdout, `Mirage manages temporary local application environments.

Usage: mirage [--server URL] [--token TOKEN] [--json] <command>
Commands:
  start [--public PORT] [--private PORT]
  space create|list|delete
  link create|list|logs|restart|delete
`)
}

type config struct {
	BaseHost, PublicAddress, PrivateAddress, DataPath string
	Caddy                                             struct {
		AdminURL, Binary string
		Managed          bool
	}
}

func loadConfig(path string, getenv func(string) string, getwd func() (string, error)) (config, error) {
	var c config
	if path == "" {
		if x := getenv("MIRAGE_CONFIG"); x != "" {
			path = x
		} else if home := getenv("HOME"); home != "" {
			path = filepath.Join(home, ".config/mirage/config.yaml")
		}
	}
	if path == "" {
		return c, nil
	}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.Split(line, "#")[0])
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		p := strings.SplitN(line, ":", 2)
		if len(p) != 2 {
			continue
		}
		k, v := strings.TrimSpace(p[0]), strings.Trim(strings.TrimSpace(p[1]), "\"'")
		if section == "caddy" {
			switch k {
			case "admin_url":
				c.Caddy.AdminURL = v
			case "binary":
				c.Caddy.Binary = v
			case "managed":
				c.Caddy.Managed = strings.EqualFold(v, "true")
			}
			continue
		}
		switch k {
		case "base_host":
			c.BaseHost = v
		case "public_address":
			c.PublicAddress = v
		case "private_address":
			c.PrivateAddress = v
		case "data_path":
			c.DataPath = v
		}
	}
	return c, nil
}
