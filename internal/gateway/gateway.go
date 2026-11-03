package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Gateway is a loopback-only reverse proxy for one explicitly configured
// upstream. It deliberately does not implement provider selection.
type Gateway struct {
	config   Config
	target   *url.URL
	proxy    *httputil.ReverseProxy
	server   *http.Server
	logger   *slog.Logger
	started  atomic.Bool
	requests atomic.Uint64
	failures atomic.Uint64
	inflight atomic.Int64
	lastMu   sync.RWMutex
	last     requestSummary
}

type requestSummary struct {
	ID         uint64 `json:"id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseStatus) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
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
	g.proxy = &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) { g.director(pr.Out) }, FlushInterval: -1}
	var proxyFunc func(*http.Request) (*url.URL, error)
	if c.OutboundProxyEnv != "" {
		proxyURL, parseErr := url.Parse(os.Getenv(c.OutboundProxyEnv))
		if parseErr != nil || proxyURL.Host == "" || proxyURL.User != nil || proxyURL.RawQuery != "" || proxyURL.Fragment != "" || (proxyURL.Scheme != "http" && proxyURL.Scheme != "https") {
			return nil, errors.New("outbound proxy environment variable must contain an HTTP(S) proxy URL without credentials")
		}
		proxyFunc = func(r *http.Request) (*url.URL, error) {
			ip := net.ParseIP(r.URL.Hostname())
			if strings.EqualFold(r.URL.Hostname(), "localhost") || (ip != nil && ip.IsLoopback()) {
				return nil, nil
			}
			return proxyURL, nil
		}
	}
	g.proxy.Transport = &http.Transport{
		Proxy:                 proxyFunc,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: time.Duration(c.RequestTimeoutSeconds) * time.Second,
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
	g.proxy.ErrorLog = log.New(io.Discard, "", 0)
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
	g.started.Store(true)
	defer g.started.Store(false)
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
	g.started.Store(false)
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
		if !g.started.Load() {
			status = "stopped"
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, map[string]string{"status": status})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		g.serveAdmin(w, r)
		return
	}
	if !g.pathAllowed(r.URL.Path) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "path_not_allowed"})
		return
	}
	if !g.methodAllowed(r.URL.Path, r.Method) {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	clientToken := strings.TrimSpace(os.Getenv(g.config.ClientTokenEnv))
	if clientToken == "" || !constantTimeBearerMatch(r.Header.Get(g.config.ClientTokenHeader), clientToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "gateway_auth_required"})
		return
	}
	if !isLoopbackRequestHost(r.Host) || (r.Header.Get("Origin") != "" && !sameLoopbackOrigin(r.Header.Get("Origin"), r.Host)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "request_origin_not_allowed"})
		return
	}
	id := g.requests.Add(1)
	start := time.Now()
	path := r.URL.Path
	g.inflight.Add(1)
	defer g.inflight.Add(-1)
	statusWriter := &responseStatus{ResponseWriter: w}
	g.proxy.ServeHTTP(statusWriter, r)
	status := statusWriter.status
	if status == 0 {
		status = http.StatusOK
	}
	summary := requestSummary{ID: id, Method: r.Method, Path: path, Status: status, DurationMS: time.Since(start).Milliseconds()}
	g.lastMu.Lock()
	g.last = summary
	g.lastMu.Unlock()
	g.logger.Info("request completed", "request_id", id, "method", r.Method, "path", path, "status", status, "duration_ms", summary.DurationMS)
}

func (g *Gateway) pathAllowed(path string) bool {
	for _, pattern := range g.config.AllowedPaths {
		if path == pattern {
			return true
		}
	}
	return false
}

func (g *Gateway) methodAllowed(path, method string) bool {
	if strings.HasSuffix(path, "/models") {
		return method == http.MethodGet
	}
	if strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/responses/compact") || strings.HasSuffix(path, "/chat/completions") {
		return method == http.MethodPost
	}
	return false
}

func (g *Gateway) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequestHost(r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin_host_not_allowed"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "admin_read_only"})
		return
	}
	token := strings.TrimSpace(os.Getenv(g.config.AdminTokenEnv))
	if token == "" || !constantTimeBearerMatch(r.Header.Get("Authorization"), token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "admin_auth_required"})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameLoopbackOrigin(origin, r.Host) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin_origin_not_allowed"})
		return
	}
	switch r.URL.Path {
	case "/admin/status":
		g.lastMu.RLock()
		last := g.last
		g.lastMu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "ready": g.started.Load(), "listen": g.config.Listen,
			"upstream_host": g.target.Host, "allowed_paths": g.config.AllowedPaths,
			"requests": g.requests.Load(), "failures": g.failures.Load(), "inflight": g.inflight.Load(),
			"last_request": last,
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "admin_path_not_found"})
	}
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
	r.Header.Del(g.config.ClientTokenHeader)
	for name, value := range g.config.InjectedHeaders() {
		r.Header.Set(name, value)
	}
	// Do not forward a client-provided host or proxy authentication header.
	r.Header.Del("Proxy-Authorization")
}

func (g *Gateway) modifyResponse(resp *http.Response) error {
	resp.Header.Del("Set-Cookie")
	if resp.StatusCode >= 400 {
		g.failures.Add(1)
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		resp.Header.Set("Cache-Control", "no-cache")
		resp.Header.Del("Content-Length")
	}
	return nil
}

func (g *Gateway) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	g.failures.Add(1)
	g.logger.Error("upstream request failed", "method", r.Method, "path", r.URL.Path, "stage", "upstream_transport")
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upstream_unavailable"})
}

func stripRequestHeaders(h http.Header, forwardAuthorization bool) {
	for name := range h {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-chatgpt-") || strings.HasPrefix(lower, "x-openai-") || strings.HasPrefix(lower, "x-forwarded-") || lower == "forwarded" {
			h.Del(name)
		}
	}
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

func isLoopbackRequestHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func sameLoopbackOrigin(origin, hostport string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.EqualFold(u.Host, hostport) && isLoopbackRequestHost(u.Host)
}

func constantTimeBearerMatch(header, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}
