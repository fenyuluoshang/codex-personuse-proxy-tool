package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Gateway is a loopback-only reverse proxy for one explicitly configured
// upstream. It deliberately does not implement provider selection.
type Gateway struct {
	config  Config
	target  *url.URL
	proxy   *httputil.ReverseProxy
	server  *http.Server
	logger  *slog.Logger
	started bool
}

func New(c Config, logger *slog.Logger) (*Gateway, error) {
	if logger == nil {
		logger = slog.Default()
	}
	target, err := c.Target()
	if err != nil {
		return nil, err
	}
	g := &Gateway{config: c, target: target, logger: logger}
	g.proxy = httputil.NewSingleHostReverseProxy(target)
	g.proxy.Director = g.director
	g.proxy.Transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	g.proxy.ModifyResponse = g.modifyResponse
	g.proxy.ErrorHandler = g.errorHandler
	handler := http.HandlerFunc(g.serveHTTP)
	g.server = &http.Server{
		Addr:              c.Listen,
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(c.ReadHeaderTimeoutSeconds) * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}
	return g, nil
}

func (g *Gateway) Config() Config { return g.config }

func (g *Gateway) Serve(l net.Listener) error {
	if l == nil {
		return errors.New("listener is nil")
	}
	if !isLoopbackListener(l.Addr()) {
		return fmt.Errorf("gateway listener %q is not loopback", l.Addr().String())
	}
	g.started = true
	return g.server.Serve(l)
}

func (g *Gateway) ListenAndServe() error {
	if !isLoopbackAddress(g.config.Listen) {
		return fmt.Errorf("listen address %q is not loopback", g.config.Listen)
	}
	ln, err := net.Listen("tcp", g.config.Listen)
	if err != nil {
		return err
	}
	return g.Serve(ln)
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	if g.server == nil {
		return nil
	}
	g.started = false
	return g.server.Shutdown(ctx)
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path == "/readyz" {
		status := "ready"
		code := http.StatusOK
		if !g.started {
			status = "stopped"
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]string{"status": status})
		return
	}
	if r.URL.Scheme != "" || r.Host != "" {
		// Absolute-form requests and a non-empty Host are normal for proxies;
		// the reverse proxy overwrites the destination. Host checks belong to the
		// management UI, while this endpoint is intentionally loopback-only.
	}
	g.proxy.ServeHTTP(w, r)
}

func (g *Gateway) director(r *http.Request) {
	originalPath := r.URL.Path
	basePath := strings.TrimRight(g.target.Path, "/")
	if basePath == "" {
		basePath = "/"
	}
	if basePath != "/" && (originalPath == basePath || strings.HasPrefix(originalPath, basePath+"/")) {
		// The client already included the provider base path.
	} else {
		joined := strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(originalPath, "/")
		if joined == "" {
			joined = "/"
		}
		r.URL.Path = joined
	}
	r.URL.Scheme = g.target.Scheme
	r.URL.Host = g.target.Host
	r.Host = g.target.Host
	r.URL.RawPath = ""
	stripRequestHeaders(r.Header, g.config.ForwardAuthorization)
	for name, value := range g.config.InjectedHeaders() {
		r.Header.Set(name, value)
	}
	// Do not forward a client-provided host or proxy authentication header.
	r.Header.Del("Proxy-Authorization")
}

func (g *Gateway) modifyResponse(resp *http.Response) error {
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		resp.Header.Set("Cache-Control", "no-cache")
		resp.Header.Del("Content-Length")
	}
	return nil
}

func (g *Gateway) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	g.logger.Error("upstream request failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream_unavailable"})
}

func stripRequestHeaders(h http.Header, forwardAuthorization bool) {
	for _, name := range []string{
		"ChatGPT-Account-ID", "Cookie", "Proxy-Authorization", "Proxy-Connection",
		"OpenAI-Organization", "OpenAI-Project", "X-OpenAI-Organization", "X-OpenAI-Project",
		"X-ChatGPT-Account-ID", "X-ChatGPT-User", "X-ChatGPT-Token",
	} {
		h.Del(name)
	}
	if !forwardAuthorization {
		h.Del("Authorization")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackListener(address net.Addr) bool {
	if tcp, ok := address.(*net.TCPAddr); ok {
		return tcp.IP != nil && tcp.IP.IsLoopback()
	}
	return isLoopbackAddress(address.String())
}
