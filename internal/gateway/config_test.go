package gateway

import "testing"

func TestValidateConfigRequiresAllowlist(t *testing.T) {
	c := DefaultConfig()
	c.Upstream = "http://127.0.0.1:9999/v1"
	if err := ValidateConfig(c); err == nil {
		t.Fatal("expected missing allowlist error")
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
