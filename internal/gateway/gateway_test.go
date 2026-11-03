package gateway

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testConfig(upstream string) Config {
	target, err := url.Parse(upstream)
	if err != nil {
		panic(err)
	}
	return Config{
		Listen:                   "127.0.0.1:0",
		Upstream:                 upstream,
		AllowedUpstreamHosts:     []string{target.Host},
		AllowedPaths:             DefaultConfig().AllowedPaths,
		ClientTokenEnv:           "TEST_GATEWAY_CLIENT_TOKEN",
		ClientTokenHeader:        "X-Test-Gateway-Token",
		AdminTokenEnv:            "CODEX_GATEWAY_ADMIN_TOKEN",
		RequestTimeoutSeconds:    30,
		ReadHeaderTimeoutSeconds: 5,
		StaticHeaders:            map[string]string{"X-Test-Gateway": "ok"},
		HeadersFromEnv:           map[string]string{},
	}
}

func serveGateway(t *testing.T, c Config) (*Gateway, string) {
	t.Helper()
	t.Setenv(c.ClientTokenEnv, "test-client-token")
	g, err := New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = g.Serve(ln) }()
	t.Cleanup(func() { _ = g.Shutdown(context.Background()) })
	return g, "http://" + ln.Addr().String()
}

func TestGatewayHealth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	g, addr := serveGateway(t, testConfig(upstream.URL+"/v1"))
	_ = g
	deadline := time.Now().Add(time.Second)
	for {
		resp, err := http.Get(addr + "/healthz")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("health status: %d", resp.StatusCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGatewayStripsOfficialHeadersAndForwardsConfiguredHeaders(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL+"/v1"))
	req, _ := http.NewRequest(http.MethodPost, addr+"/responses", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("Authorization", "Bearer official-token")
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	req.Header.Set("ChatGPT-Account-ID", "account")
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Proxy-Authorization", "Basic secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	h := <-seen
	for _, name := range []string{"Authorization", "ChatGPT-Account-ID", "Cookie", "Proxy-Authorization", "X-Test-Gateway-Token"} {
		if value := h.Get(name); value != "" {
			t.Errorf("%s leaked: %q", name, value)
		}
	}
	if h.Get("X-Test-Gateway") != "ok" {
		t.Errorf("configured header missing")
	}
}

func TestGatewayCanExplicitlyForwardThirdPartyAuthorization(t *testing.T) {
	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	c := testConfig(upstream.URL)
	c.ForwardAuthorization = true
	_, addr := serveGateway(t, c)
	req, _ := http.NewRequest(http.MethodGet, addr+"/models", nil)
	req.Header.Set("Authorization", "Bearer third-party-test")
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := <-seen; got != "Bearer third-party-test" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestGatewayStreamsSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream lacks flusher")
		}
		_, _ = io.WriteString(w, "data: one\n\n")
		flusher.Flush()
		time.Sleep(10 * time.Millisecond)
		_, _ = io.WriteString(w, "data: two\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL))
	req, _ := http.NewRequest(http.MethodPost, addr+"/responses", strings.NewReader(`{"model":"test"}`))
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q", got)
	}
	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		if scanner.Text() != "" {
			lines = append(lines, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "data: one|data: two" {
		t.Fatalf("stream = %q", lines)
	}
}

func TestGatewayRejectsUnknownPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request to upstream")
	}))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL))
	resp, err := http.Get(addr + "/not-a-model-endpoint")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGatewayRejectsUnsupportedMethod(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request to upstream")
	}))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL))
	req, _ := http.NewRequest(http.MethodPost, addr+"/models", nil)
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGatewayRejectsMissingClientToken(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL))
	req, _ := http.NewRequest(http.MethodGet, addr+"/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || called {
		t.Fatalf("status=%d upstream_called=%v", resp.StatusCode, called)
	}
}

func TestGatewayAdminRequiresTokenAndLoopbackOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	c := testConfig(upstream.URL)
	c.AdminTokenEnv = "TEST_GATEWAY_ADMIN_TOKEN"
	t.Setenv(c.AdminTokenEnv, "test-admin-token")
	_, addr := serveGateway(t, c)

	req, _ := http.NewRequest(http.MethodGet, addr+"/admin/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, addr+"/admin/status", nil)
	req.Header.Set("Authorization", "Bearer test-admin-token")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad-origin status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, addr+"/admin/status", nil)
	req.Header.Set("Authorization", "Bearer test-admin-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin status = %d", resp.StatusCode)
	}
}

func TestGatewayCannotStripUpstreamCredentialsUsingConnectionHeader(t *testing.T) {
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Header.Clone(); w.WriteHeader(204) }))
	defer upstream.Close()
	c := testConfig(upstream.URL)
	c.StaticHeaders["X-API-Key"] = "upstream-key"
	_, addr := serveGateway(t, c)
	req, _ := http.NewRequest(http.MethodGet, addr+"/models", nil)
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	req.Header.Set("Connection", "X-API-Key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if h := <-seen; h.Get("X-API-Key") != "upstream-key" {
		t.Fatal("connection header removed gateway-injected upstream key")
	}
}

func TestGatewaySSEArrivesBeforeResponseCompletesAndCancellationReachesUpstream(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	_, addr := serveGateway(t, testConfig(upstream.URL))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, addr+"/responses", strings.NewReader(`{"stream":true}`))
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event %q: %v", line, err)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive cancellation")
	}
}

func TestGatewayTransportLogsNeverIncludeQuerySecrets(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := "http://" + listener.Addr().String()
	listener.Close()
	var logs bytes.Buffer
	c := testConfig(upstream)
	t.Setenv(c.ClientTokenEnv, "test-client-token")
	g, err := New(c, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/models?key=DO_NOT_LOG", nil)
	req.Header.Set("X-Test-Gateway-Token", "Bearer test-client-token")
	recorder := httptest.NewRecorder()
	g.serveHTTP(recorder, req)
	if recorder.Code != 502 {
		t.Fatalf("status %d", recorder.Code)
	}
	if strings.Contains(logs.String(), "DO_NOT_LOG") {
		t.Fatal("query secret leaked to logs")
	}
}
