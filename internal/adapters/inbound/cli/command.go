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

type exitError struct{ code int }

func (e exitError) Error() string { return "command failed" }

func (c Command) Execute(args []string) int {
	root := c.cobraTree()
	root.SetOut(c.stdout)
	root.SetErr(c.stderr)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if x, ok := err.(exitError); ok {
			return x.code
		}
		return c.usageError(err.Error())
	}
	return 0
}

func commandResult(code int) error {
	if code == 0 {
		return nil
	}
	return exitError{code: code}
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
	// Validate the fully resolved listener addresses before composition starts
	// Caddy, opens libSQL, or binds anything. This keeps malformed CLI input a
	// side-effect-free usage error.
	if o.PublicAddress == "" {
		o.PublicAddress = ":9955"
	}
	if o.PrivateAddress == "" {
		o.PrivateAddress = "127.0.0.1:9956"
	}
	if !validBind(o.PublicAddress) {
		return c.fail(fmt.Errorf("invalid public bind address %q", o.PublicAddress))
	}
	if !validBind(o.PrivateAddress) {
		return c.fail(fmt.Errorf("invalid private bind address %q", o.PrivateAddress))
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

// cobraTree is the executing command tree. Cobra owns parsing, validation,
// help, and dispatch; the leaf handlers perform only API/start behavior.
func (c Command) cobraTree() *cobra.Command {
	var server, token, cfgPath string
	var jsonOut bool
	root := &cobra.Command{
		Use: "mirage", Short: "Mirage manages temporary local application environments",
		SilenceUsage: true, SilenceErrors: true,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.PersistentFlags().StringVar(&server, "server", "", "private Mirage server URL")
	root.PersistentFlags().StringVar(&token, "token", "", "space bearer token")
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit JSON")
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "configuration file")
	root.Flags().BoolP("version", "v", false, "print version")
	root.PreRunE = func(cmd *cobra.Command, _ []string) error {
		v, _ := cmd.Flags().GetBool("version")
		if v {
			fmt.Fprintf(c.stdout, "mirage %s\n", c.version())
			return exitError{code: 0}
		}
		return nil
	}
	// Cobra treats a successful sentinel like an error, so version is a real command
	// and --version is handled before Execute below via a root flag callback.
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		v, _ := cmd.Flags().GetBool("version")
		if v {
			fmt.Fprintf(c.stdout, "mirage %s\n", c.version())
			return nil
		}
		return cmd.Help()
	}
	root.SetHelpCommand(&cobra.Command{Use: "help", Args: func(*cobra.Command, []string) error { return nil }, RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("help does not accept arguments")
		}
		return root.Help()
	}})
	root.AddCommand(&cobra.Command{Use: "version", Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("version does not accept arguments")
		}
		return nil
	}, Run: func(*cobra.Command, []string) { fmt.Fprintf(c.stdout, "mirage %s\n", c.version()) }})

	resolve := func() (config, string, error) {
		conf, err := loadConfig(cfgPath, c.getenv, c.getwd)
		if err != nil {
			return conf, "", err
		}
		base := server
		if base == "" {
			base = c.getenv("MIRAGE_SERVER")
		}
		if base == "" {
			base = conf.PrivateAddress
		}
		if base == "" {
			base = "http://127.0.0.1:9956"
		}
		base = strings.TrimRight(base, "/")
		if !strings.Contains(base, "://") {
			base = "http://" + base
		}
		c.forcedToken = token
		return conf, base, nil
	}
	failResolve := func(err error) error { return commandResult(c.fail(err)) }

	var public, private string
	start := &cobra.Command{Use: "start", Short: "Start Mirage", Args: cobra.NoArgs}
	start.Flags().StringVar(&public, "public", "", "public port/address")
	start.Flags().StringVar(&private, "private", "", "private port/address")
	start.RunE = func(cmd *cobra.Command, _ []string) error {
		conf, _, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		a := []string{}
		if cmd.Flags().Changed("public") {
			a = append(a, "--public", public)
		}
		if cmd.Flags().Changed("private") {
			a = append(a, "--private", private)
		}
		return commandResult(c.doStart(a, conf, cfgPath))
	}

	space := &cobra.Command{Use: "space", Short: "Manage spaces", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return fmt.Errorf("space requires a subcommand") }}
	var ttl, alias, force string
	spaceCreate := &cobra.Command{Use: "create", Short: "create a space", Args: cobra.NoArgs}
	spaceCreate.Flags().StringVar(&ttl, "ttl", "", "space TTL")
	spaceCreate.Flags().StringVar(&alias, "alias", "", "space alias")
	spaceCreate.RunE = func(*cobra.Command, []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		a := []string{"create"}
		if ttl != "" {
			a = append(a, "--ttl", ttl)
		}
		if alias != "" {
			a = append(a, "--alias", alias)
		}
		return commandResult(c.space(a, base, jsonOut, conf))
	}
	spaceList := &cobra.Command{Use: "list [alias]", Short: "list spaces", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		return commandResult(c.space(append([]string{"list"}, a...), base, jsonOut, conf))
	}}
	spaceDelete := &cobra.Command{Use: "delete <alias>", Short: "delete a space", Args: cobra.ExactArgs(1)}
	spaceDelete.Flags().StringVar(&force, "force", "", "administrative audit reason")
	spaceDelete.RunE = func(_ *cobra.Command, a []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		x := []string{"delete", a[0]}
		if force != "" {
			x = append(x, "--force", force)
		}
		return commandResult(c.space(x, base, jsonOut, conf))
	}
	space.AddCommand(spaceCreate, spaceList, spaceDelete)

	link := &cobra.Command{Use: "link", Short: "Manage links", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return fmt.Errorf("link requires a subcommand") }}
	var name, command, folder, healthCheck, grace, linkTTL string
	var restarts bool
	linkCreate := &cobra.Command{Use: "create", Short: "create a link", Args: cobra.NoArgs}
	for n, target := range map[string]*string{"name": &name, "command": &command, "execution-folder": &folder, "health-check": &healthCheck, "grace": &grace, "ttl": &linkTTL} {
		linkCreate.Flags().StringVar(target, n, "", n)
	}
	linkCreate.Flags().BoolVar(&restarts, "restarts", false, "automatic restarts")
	linkCreate.RunE = func(*cobra.Command, []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		a := []string{"create"}
		for _, x := range [][2]string{{"name", name}, {"command", command}, {"execution-folder", folder}, {"health-check", healthCheck}, {"grace", grace}, {"ttl", linkTTL}} {
			if x[1] != "" {
				a = append(a, "--"+x[0], x[1])
			}
		}
		if restarts {
			a = append(a, "--restarts")
		}
		return commandResult(c.link(a, base, jsonOut, conf))
	}
	linkList := &cobra.Command{Use: "list", Short: "list links", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		return commandResult(c.link([]string{"list"}, base, jsonOut, conf))
	}}
	var tail int
	var follow bool
	linkLogs := &cobra.Command{Use: "logs <name>", Short: "show link logs", Args: cobra.ExactArgs(1)}
	linkLogs.Flags().IntVar(&tail, "tail", 100, "lines to show")
	linkLogs.Flags().BoolVar(&follow, "follow", false, "follow logs")
	linkLogs.RunE = func(_ *cobra.Command, a []string) error {
		conf, base, err := resolve()
		if err != nil {
			return failResolve(err)
		}
		x := []string{"logs", a[0], "--tail", strconv.Itoa(tail)}
		if follow {
			x = append(x, "--follow")
		}
		return commandResult(c.link(x, base, jsonOut, conf))
	}
	leaf := func(verb string) *cobra.Command {
		return &cobra.Command{Use: verb + " <name>", Short: verb + " a link", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			conf, base, err := resolve()
			if err != nil {
				return failResolve(err)
			}
			return commandResult(c.link([]string{verb, a[0]}, base, jsonOut, conf))
		}}
	}
	link.AddCommand(linkCreate, linkList, linkLogs, leaf("restart"), leaf("delete"))
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
	// An operator-selected file is a contract: silently falling back after a
	// typo could launch against unintended ports/data. Only the conventional
	// implicit HOME default is optional.
	explicit := path != ""
	if path == "" {
		if x := getenv("MIRAGE_CONFIG"); x != "" {
			path, explicit = x, true
		} else if home := getenv("HOME"); home != "" {
			path = filepath.Join(home, ".config/mirage/config.yaml")
		}
	}
	if path == "" {
		return c, nil
	}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) && !explicit {
		return c, nil
	}
	if os.IsNotExist(e) {
		return c, fmt.Errorf("config %q: %w", path, e)
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
