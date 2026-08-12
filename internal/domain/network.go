package domain

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type BaseHost string

func ParseBaseHost(s string) (BaseHost, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	suffix := strings.HasPrefix(s, "-")
	raw := strings.TrimPrefix(s, "-")
	if raw == "" || strings.Contains(raw, "/") {
		return "", NewValidation("base_host", "invalid base host")
	}
	for _, p := range strings.Split(raw, ".") {
		if err := validateLabel(p, "base_host"); err != nil {
			return "", err
		}
	}
	if suffix {
		return BaseHost("-" + raw), nil
	}
	return BaseHost(raw), nil
}
func (b BaseHost) Host(name LinkName, alias Alias) string {
	if strings.HasPrefix(string(b), "-") {
		return fmt.Sprintf("%s-%s%s", name, alias, b)
	}
	return fmt.Sprintf("%s-%s.%s", name, alias, b)
}
func PublicURL(b BaseHost, name LinkName, alias Alias, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", NewValidation("public_port", "must be 1 to 65535")
	}
	host := b.Host(name, alias)
	if port == 80 {
		return "http://" + host, nil
	}
	if port == 443 {
		return "https://" + host, nil
	}
	return "http://" + host + ":" + strconv.Itoa(port), nil
}

type HealthMethod string

const (
	HealthGET  HealthMethod = "GET"
	HealthHEAD HealthMethod = "HEAD"
	HealthPOST HealthMethod = "POST"
)

type HealthCheck struct {
	Method HealthMethod `json:"method"`
	URL    string       `json:"url"`
}

func ParseHealthCheck(s string) (HealthCheck, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return HealthCheck{}, NewValidation("health_check", "must be METHOD URL")
	}
	h := HealthCheck{HealthMethod(strings.ToUpper(f[0])), f[1]}
	if h.Method != HealthGET && h.Method != HealthHEAD && h.Method != HealthPOST {
		return HealthCheck{}, NewValidation("health_check", "unsupported method")
	}
	validationURL := strings.ReplaceAll(h.URL, "{port}", "1")
	u, e := url.ParseRequestURI(validationURL)
	if strings.Contains(h.URL, "#") || e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return HealthCheck{}, NewValidation("health_check", "must be an absolute HTTP URL")
	}
	if !validAuthority(u) {
		return HealthCheck{}, NewValidation("health_check", "invalid authority or port")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return HealthCheck{}, NewValidation("health_check", "host must be loopback")
	}
	return h, nil
}

// validAuthority rejects ambiguous authorities and requires an explicit port to
// contain only decimal digits in the TCP range.
func validAuthority(u *url.URL) bool {
	a := u.Host
	host := u.Hostname()
	if host == "" {
		return false
	}
	explicit := false
	p := ""
	if strings.HasPrefix(a, "[") {
		end := strings.LastIndex(a, "]")
		if end < 1 {
			return false
		}
		rest := a[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return false
			}
			explicit = true
			p = rest[1:]
		}
	} else if strings.Count(a, ":") == 1 {
		_, p, _ = strings.Cut(a, ":")
		explicit = true
	}
	if !explicit {
		return true
	}
	if p == "" {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, e := strconv.Atoi(p)
	return e == nil && n >= 1 && n <= 65535
}
