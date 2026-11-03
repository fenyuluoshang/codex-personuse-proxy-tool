package gateway

import (
	"bufio"
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
		RequestTimeoutSeconds:    30,
		ReadHeaderTimeoutSeconds: 5,
		StaticHeaders:            map[string]string{"X-Test-Gateway": "ok"},
		HeadersFromEnv:           map[string]string{},
	}
}

func serveGateway(t *testing.T, c Config) (*Gateway, string) {
	t.Helper()
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
	for _, name := range []string{"Authorization", "ChatGPT-Account-ID", "Cookie", "Proxy-Authorization"} {
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
	resp, err := http.Get(addr + "/responses")
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
