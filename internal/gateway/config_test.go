package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigRequiresAllowlist(t *testing.T) {
	c := DefaultConfig()
	c.Upstream = "http://127.0.0.1:9999/v1"
	if err := ValidateConfig(c); err == nil {
		t.Fatal("expected missing allowlist error")
	}
}

func TestValidateConfigRejectsNonLoopbackAndLoopAliases(t *testing.T) {
	for _, tc := range []struct{ listen, upstream string }{
		{"0.0.0.0:8787", "http://provider.example"},
		{"127.0.0.1:8787", "http://localhost:8787"},
		{"127.0.0.1:8787", "http://127.0.0.2:8787"},
	} {
		c := DefaultConfig()
		c.Listen = tc.listen
		c.Upstream = tc.upstream
		c.AllowedUpstreamHosts = []string{"provider.example", "localhost:8787", "127.0.0.2:8787"}
		if err := ValidateConfig(c); err == nil {
			t.Errorf("accepted %s -> %s", tc.listen, tc.upstream)
		}
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(path, []byte(`{"upstream":"http://provider.example","allowed_upstream_hosts":["provider.example"],"forward_authorisation":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("accepted unknown misspelled security property")
	}
}

func TestValidateInjectedHeaderNames(t *testing.T) {
	for _, name := range []string{"Header\r\nX-Injected", " Connection", "Connection", "Content-Length", "X-ChatGPT-Other", "Proxy-Authorization"} {
		if err := validateInjectedHeader(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
}

func TestValidateConfigRejectsGatewayLoop(t *testing.T) {
	c := DefaultConfig()
	c.Listen = "127.0.0.1:8787"
	c.Upstream = "http://127.0.0.1:8787/v1"
	c.AllowedUpstreamHosts = []string{"127.0.0.1:8787"}
	if err := ValidateConfig(c); err == nil {
		t.Fatal("expected loop error")
	}
}

func TestValidateConfigRejectsSensitiveInjectedHeaders(t *testing.T) {
	c := DefaultConfig()
	c.Upstream = "http://127.0.0.1:9999/v1"
	c.AllowedUpstreamHosts = []string{"127.0.0.1:9999"}
	c.StaticHeaders = map[string]string{"Cookie": "secret"}
	if err := ValidateConfig(c); err == nil {
		t.Fatal("expected sensitive header error")
	}
}
