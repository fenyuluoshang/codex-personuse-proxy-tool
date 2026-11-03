package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
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
		StaticHeaders:            map[string]string{},
		HeadersFromEnv:           map[string]string{},
	}
}

func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	c := DefaultConfig()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, ValidateConfig(c)
}

func ValidateConfig(c Config) error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen is required")
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
	if strings.EqualFold(target.Host, c.Listen) {
		return errors.New("upstream cannot point to the gateway listen address")
	}
	for name := range c.StaticHeaders {
		if err := validateInjectedHeader(name); err != nil {
			return fmt.Errorf("static_headers: %w", err)
		}
	}
	for name, env := range c.HeadersFromEnv {
		if err := validateInjectedHeader(name); err != nil {
			return fmt.Errorf("headers_from_env: %w", err)
		}
		if strings.TrimSpace(env) == "" {
			return fmt.Errorf("headers_from_env[%q] has empty environment variable name", name)
		}
	}
	return nil
}

func validateInjectedHeader(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("header name is empty")
	}
	lower := strings.ToLower(name)
	for _, denied := range []string{"host", "cookie", "set-cookie", "proxy-authorization", "chatgpt-account-id", "openai-organization", "openai-project"} {
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
		out[k] = os.ExpandEnv(v)
	}
	for k, env := range c.HeadersFromEnv {
		if value, ok := os.LookupEnv(env); ok {
			out[k] = value
		}
	}
	return out
}
