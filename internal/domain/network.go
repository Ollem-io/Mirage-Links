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
	u, e := url.Parse(h.URL)
	if e != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.User != nil {
		return HealthCheck{}, NewValidation("health_check", "must be an absolute HTTP URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return HealthCheck{}, NewValidation("health_check", "host must be loopback")
	}
	return h, nil
}
