// Package cli implements Mirage's HTTP command-line client.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/primeintellect/mirage/internal/domain"
	"github.com/spf13/cobra"
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
	waitSignal     func()
}

func New(stdout, stderr io.Writer, version VersionSource) Command {
	return NewWithStart(stdout, stderr, version, nil)
}
func NewWithStart(stdout, stderr io.Writer, version VersionSource, start StartFunc) Command {
	return Command{stdout: stdout, stderr: stderr, version: version, http: &http.Client{Timeout: 30 * time.Second}, start: start, getenv: os.Getenv, getwd: os.Getwd, waitSignal: waitForSignal}
}
func (c Command) usageError(s string) int {
	fmt.Fprintf(c.stderr, "mirage: %s\nRun 'mirage --help' for usage.\n", s)
	return 2
}
func (c Command) Execute(args []string) int {
	// Cobra owns the command vocabulary and help metadata. Argument resolution
	// remains injectable below so HTTP behavior can be tested without a process.
	root := &cobra.Command{Use: "mirage", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "private Mirage server URL")
	root.PersistentFlags().String("token", "", "space bearer token")
	root.PersistentFlags().Bool("json", false, "emit JSON")
	root.PersistentFlags().String("config", "", "configuration file")
	_ = root
	args = expandEquals(args)
	if containsHelp(args) {
		root := c.cobraTree()
		root.SetOut(c.stdout)
		root.SetErr(c.stderr)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			return c.usageError(err.Error())
		}
		return 0
	}
	if len(args) == 0 {
		c.help()
		return 0
	}
	// Persistent globals may appear before or after subcommands, exactly like
	// Cobra persistent flags. Remove them before subcommand-specific parsing.
	cfgPath, server, globalToken, jsonOut := "", "", "", false
	remaining := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server", "--token", "--config":
			if i+1 >= len(args) {
				return c.usageError(args[i] + " requires a value")
			}
			v := args[i+1]
			i++
			switch args[i-1] {
			case "--server":
				server = v
			case "--token":
				globalToken = v
			case "--config":
				cfgPath = v
			}
		case "--json":
			jsonOut = true
		default:
			remaining = append(remaining, args[i])
		}
	}
	args = remaining
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		if len(args) == 1 {
			c.help()
			return 0
		}
		return c.usageError("help does not accept arguments")
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v" || args[0] == "version") {
		if len(args) == 1 {
			fmt.Fprintf(c.stdout, "mirage %s\n", c.version())
			return 0
		}
		return c.usageError("version does not accept arguments")
	}
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
	c.waitSignal()
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
			if _, e := domain.ParseAlias(a[0]); e != nil {
				return c.fail(e)
			}
			path += "/" + url.PathEscape(a[0])
		}
		return c.request(base, "GET", path, "", nil, jout, func(v map[string]any) { c.printRows(v, "spaces", []string{"alias", "expires_at"}) })
	case "delete":
		if len(a) == 0 {
			return c.usageError("space delete requires an alias")
		}
		alias := a[0]
		if _, e := domain.ParseAlias(alias); e != nil {
			return c.fail(e)
		}
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
		return c.request(base, "DELETE", "/api/v1/spaces/"+url.PathEscape(alias), tok, map[string]any{"force": force, "reason": reason}, jout, func(map[string]any) { fmt.Fprintln(c.stdout, "Deleted space", alias) })
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
		if _, e := domain.ParseLinkName(name); e != nil {
			return c.fail(e)
		}
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
		path := fmt.Sprintf("/api/v1/links/%s/logs?tail=%d", url.PathEscape(name), tail)
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
		if _, e := domain.ParseLinkName(name); e != nil {
			return c.fail(e)
		}
		a = a[1:]
		if e := popToken(); e != nil {
			return c.fail(e)
		}
		method, path := "DELETE", "/api/v1/links/"+url.PathEscape(name)
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
		var decoded any
		if json.Unmarshal(raw, &decoded) == nil {
			key := responseListKey(path)
			switch x := decoded.(type) {
			case []any:
				raw, _ = json.Marshal(map[string]any{key: x})
			case map[string]any:
				if items, ok := x["items"]; ok {
					delete(x, "items")
					x[key] = items
					raw, _ = json.Marshal(x)
				}
			}
		}
		c.stdout.Write(append(bytes.TrimSpace(raw), '\n'))
		return 0
	}
	var decoded any
	if e = json.Unmarshal(raw, &decoded); e != nil {
		return c.fail(e)
	}
	v, ok := decoded.(map[string]any)
	if !ok {
		if items, arrayOK := decoded.([]any); arrayOK {
			v = map[string]any{responseListKey(path): items}
		} else {
			return c.fail(fmt.Errorf("unexpected API response shape"))
		}
	}
	if items, exists := v["items"]; exists {
		if _, already := v[responseListKey(path)]; !already {
			v[responseListKey(path)] = items
		}
	}
	human(v)
	return 0
}
func (c Command) follow(base, path, tok string) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signalNotify(sig)
	defer signalStop(sig)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()
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
	if e != nil && ctx.Err() == nil {
		return c.fail(e)
	}
	return 0
}
func responseListKey(path string) string {
	if strings.Contains(path, "/logs") {
		return "logs"
	}
	if strings.Contains(path, "/links") {
		return "links"
	}
	return "spaces"
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
	oneKey := strings.TrimSuffix(key, "s")
	if one, ok := v[oneKey].(map[string]any); ok {
		for _, f := range fields {
			if x, ok := one[f]; ok {
				fmt.Fprintf(c.stdout, "%s: %v\n", strings.Title(strings.ReplaceAll(f, "_", " ")), x)
			}
		}
		return
	}
	items, ok := v[key].([]any)
	if !ok {
		return
	}
	for _, x := range items {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		for i, f := range fields {
			if i > 0 {
				fmt.Fprint(c.stdout, "\t")
			}
			fmt.Fprint(c.stdout, m[f])
		}
		fmt.Fprintln(c.stdout)
	}
}

func containsHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// expandEquals accepts the standard pflag/Cobra --flag=value spelling.
func expandEquals(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		if k, v, ok := strings.Cut(a, "="); ok && (k == "--server" || k == "--token" || k == "--config" || k == "--public" || k == "--private" || k == "--ttl" || k == "--alias" || k == "--name" || k == "--command" || k == "--execution-folder" || k == "--health-check" || k == "--grace" || k == "--tail" || k == "--force") {
			out = append(out, k, v)
		} else {
			out = append(out, a)
		}
	}
	return out
}

func (c Command) help() {
	root := c.cobraTree()
	root.SetOut(c.stdout)
	_ = root.Help()
}

// cobraTree is the canonical command/flag contract used for generated help and
// shell completion. Execution delegates to the HTTP handlers above so they stay
// independently testable.
func (c Command) cobraTree() *cobra.Command {
	root := &cobra.Command{Use: "mirage", Short: "Mirage manages temporary local application environments", Run: func(*cobra.Command, []string) {}}
	root.PersistentFlags().String("server", "", "private Mirage server URL")
	root.PersistentFlags().String("token", "", "space bearer token")
	root.PersistentFlags().Bool("json", false, "emit JSON")
	start := &cobra.Command{Use: "start", Short: "Start Mirage", Run: func(*cobra.Command, []string) {}}
	start.Flags().String("public", "9955", "public port/address")
	start.Flags().String("private", "9956", "private port/address")
	start.Flags().String("config", "", "configuration file")
	space := &cobra.Command{Use: "space", Short: "Manage spaces"}
	spaceCreate := &cobra.Command{Use: "create", Short: "create a space", Run: func(*cobra.Command, []string) {}}
	spaceCreate.Flags().String("ttl", "", "space TTL")
	spaceCreate.Flags().String("alias", "", "space alias")
	spaceList := &cobra.Command{Use: "list [alias]", Short: "list spaces", Args: cobra.MaximumNArgs(1), Run: func(*cobra.Command, []string) {}}
	spaceDelete := &cobra.Command{Use: "delete <alias>", Short: "delete a space", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	spaceDelete.Flags().String("force", "", "administrative audit reason")
	space.AddCommand(spaceCreate, spaceList, spaceDelete)
	link := &cobra.Command{Use: "link", Short: "Manage links"}
	linkCreate := &cobra.Command{Use: "create", Short: "create a link", Run: func(*cobra.Command, []string) {}}
	for _, f := range []string{"name", "command", "execution-folder", "health-check", "grace", "ttl"} {
		linkCreate.Flags().String(f, "", f)
	}
	linkCreate.Flags().Bool("restarts", false, "automatic restarts")
	linkList := &cobra.Command{Use: "list", Short: "list links", Run: func(*cobra.Command, []string) {}}
	linkLogs := &cobra.Command{Use: "logs <name>", Short: "show link logs", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	linkLogs.Flags().Int("tail", 100, "lines to show")
	linkLogs.Flags().Bool("follow", false, "follow logs")
	linkRestart := &cobra.Command{Use: "restart <name>", Short: "restart a link", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	linkDelete := &cobra.Command{Use: "delete <name>", Short: "delete a link", Args: cobra.ExactArgs(1), Run: func(*cobra.Command, []string) {}}
	link.AddCommand(linkCreate, linkList, linkLogs, linkRestart, linkDelete)
	root.AddCommand(start, space, link)
	return root
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
	if c.BaseHost != "" {
		if _, err := domain.ParseBaseHost(c.BaseHost); err != nil {
			return c, fmt.Errorf("config base_host: %w", err)
		}
	}
	for _, a := range []string{c.PublicAddress, c.PrivateAddress} {
		if a != "" && !validBind(a) {
			return c, fmt.Errorf("invalid bind address %q", a)
		}
	}
	return c, nil
}
func validBind(a string) bool {
	_, p, err := net.SplitHostPort(a)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(p)
	return err == nil && n > 0 && n < 65536
}
