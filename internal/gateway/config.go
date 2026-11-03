package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
)

// Config describes the local model gateway. The gateway has one fixed upstream
// per process. Provider switching remains owned by CC Switch.
type Config struct {
	Listen                   string            `json:"listen"`
	Upstream                 string            `json:"upstream"`
	AllowedUpstreamHosts     []string          `json:"allowed_upstream_hosts"`
	AllowedPaths             []string          `json:"allowed_paths"`
	ClientTokenEnv           string            `json:"client_token_env"`
	ClientTokenHeader        string            `json:"client_token_header"`
	AdminTokenEnv            string            `json:"admin_token_env"`
	OutboundProxyEnv         string            `json:"outbound_proxy_env"`
	ForwardAuthorization     bool              `json:"forward_authorization"`
	StaticHeaders            map[string]string `json:"static_headers"`
	HeadersFromEnv           map[string]string `json:"headers_from_env"`
	RequestTimeoutSeconds    int               `json:"request_timeout_seconds"`
	ReadHeaderTimeoutSeconds int               `json:"read_header_timeout_seconds"`
}

func DefaultConfig() Config {
	return Config{
		Listen:                   "127.0.0.1:8787",
		RequestTimeoutSeconds:    120,
		ReadHeaderTimeoutSeconds: 15,
		AllowedPaths: []string{
			"/responses", "/responses/compact", "/models",
			"/v1/responses", "/v1/responses/compact", "/v1/models",
			"/chat/completions", "/v1/chat/completions",
			"/backend-api/codex/responses", "/backend-api/codex/models",
		},
		ClientTokenEnv:    "CODEX_GATEWAY_CLIENT_TOKEN",
		ClientTokenHeader: "X-Codex-Gateway-Token",
		AdminTokenEnv:     "CODEX_GATEWAY_ADMIN_TOKEN",
		StaticHeaders:     map[string]string{},
		HeadersFromEnv:    map[string]string{},
	}
}

func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	c := DefaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("config must contain exactly one JSON object")
	}
	return c, ValidateConfig(c)
}

func ValidateConfig(c Config) error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen is required")
	}
	if !isLoopbackAddress(c.Listen) {
		return errors.New("listen must be a numeric loopback IP and port")
	}
	if strings.TrimSpace(c.Upstream) == "" {
		return errors.New("upstream is required")
	}
	if c.RequestTimeoutSeconds <= 0 {
		return errors.New("request_timeout_seconds must be positive")
	}
	if c.ReadHeaderTimeoutSeconds <= 0 {
		return errors.New("read_header_timeout_seconds must be positive")
	}
	if strings.TrimSpace(c.AdminTokenEnv) == "" {
		return errors.New("admin_token_env must not be empty")
	}
	if strings.TrimSpace(c.ClientTokenEnv) == "" {
		return errors.New("client_token_env must not be empty")
	}
	if err := validateInjectedHeader(c.ClientTokenHeader); err != nil || strings.EqualFold(c.ClientTokenHeader, "Authorization") {
		return errors.New("client_token_header must be a permitted non-Authorization header")
	}
	if len(c.AllowedPaths) == 0 {
		return errors.New("allowed_paths must not be empty")
	}
	for _, path := range c.AllowedPaths {
		if !isAllowedPathPattern(path) {
			return fmt.Errorf("allowed_paths contains invalid path %q", path)
		}
	}
	target, err := url.Parse(c.Upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("upstream must be an absolute HTTP(S) URL")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("upstream scheme %q is not allowed", target.Scheme)
	}
	if target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("upstream must not contain userinfo, query, or fragment")
	}
	if len(c.AllowedUpstreamHosts) == 0 {
		return errors.New("allowed_upstream_hosts must contain the upstream host")
	}
	allowed := false
	for _, host := range c.AllowedUpstreamHosts {
		if strings.EqualFold(strings.TrimSpace(host), target.Host) {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("upstream host %q is not in allowed_upstream_hosts", target.Host)
	}
	listenHost, listenPort, _ := net.SplitHostPort(c.Listen)
	targetPort := target.Port()
	if targetPort == "" {
		if target.Scheme == "https" {
			targetPort = "443"
		} else {
			targetPort = "80"
		}
	}
	targetIP := net.ParseIP(target.Hostname())
	if strings.EqualFold(target.Host, c.Listen) || (targetPort == listenPort && ((targetIP != nil && targetIP.IsLoopback()) || strings.EqualFold(target.Hostname(), "localhost")) && net.ParseIP(listenHost).IsLoopback()) {
		return errors.New("upstream cannot point to the gateway listen address")
	}
	for name := range c.StaticHeaders {
		if err := validateInjectedHeader(name); err != nil {
			return fmt.Errorf("static_headers: %w", err)
		}
		if strings.EqualFold(name, c.ClientTokenHeader) {
			return errors.New("cannot inject the gateway client authentication header upstream")
		}
	}
	for name, env := range c.HeadersFromEnv {
		if err := validateInjectedHeader(name); err != nil {
			return fmt.Errorf("headers_from_env: %w", err)
		}
		if strings.TrimSpace(env) == "" {
			return fmt.Errorf("headers_from_env[%q] has empty environment variable name", name)
		}
		if strings.EqualFold(name, c.ClientTokenHeader) {
			return errors.New("cannot inject the gateway client authentication header upstream")
		}
	}
	return nil
}

func isAllowedPathPattern(path string) bool {
	switch path {
	case "/responses", "/responses/compact", "/models", "/v1/responses", "/v1/responses/compact", "/v1/models", "/backend-api/codex/responses", "/backend-api/codex/models", "/chat/completions", "/v1/chat/completions":
		return true
	default:
		return false
	}
}

func validateInjectedHeader(name string) error {
	if name == "" {
		return errors.New("header name is empty")
	}
	for _, ch := range name {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", ch)) {
			return errors.New("invalid header name")
		}
	}
	lower := strings.ToLower(name)
	for _, denied := range []string{"host", "cookie", "set-cookie", "proxy-authorization", "chatgpt-account-id", "openai-organization", "openai-project", "connection", "content-length", "transfer-encoding", "trailer", "upgrade", "te", "proxy-connection", "x-forwarded-for", "forwarded"} {
		if lower == denied || strings.HasPrefix(lower, "x-chatgpt-") {
			return fmt.Errorf("header %q is not allowed", name)
		}
	}
	return nil
}

func (c Config) Target() (*url.URL, error) {
	if err := ValidateConfig(c); err != nil {
		return nil, err
	}
	u, _ := url.Parse(c.Upstream)
	return u, nil
}

func (c Config) InjectedHeaders() map[string]string {
	out := make(map[string]string, len(c.StaticHeaders)+len(c.HeadersFromEnv))
	for k, v := range c.StaticHeaders {
		out[k] = v
	}
	for k, env := range c.HeadersFromEnv {
		if value, ok := os.LookupEnv(env); ok {
			out[k] = value
		}
	}
	return out
}
