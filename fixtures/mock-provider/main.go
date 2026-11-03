// Mock provider for isolated integration tests.
// It intentionally never persists request bodies or credentials.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type settings struct {
	mu          sync.RWMutex
	status      int
	delay       time.Duration
	chunkDelay  time.Duration
	stream      bool
	chunkSize   int
	requestHash string
	requests    int
	last        requestMeta
}

type requestMeta struct {
	At            string `json:"at"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	ContentType   string `json:"content_type,omitempty"`
	Model         string `json:"model,omitempty"`
	BodyBytes     int64  `json:"body_bytes"`
	BodySHA256    string `json:"body_sha256"`
	AuthPresent   bool   `json:"authorization_present"`
	AuthSHA256    string `json:"authorization_sha256,omitempty"`
	CookiePresent bool   `json:"cookie_present"`
}

type server struct{ state *settings }

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address; loopback is the safe default")
	flag.Parse()

	state := &settings{status: http.StatusOK, stream: true, chunkSize: 64}
	s := &server{state: state}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/__mock/config", s.config)
	mux.HandleFunc("/__mock/requests", s.requests)
	mux.HandleFunc("/v1/models", s.models)
	mux.HandleFunc("/v1/responses", s.responses)
	mux.HandleFunc("/backend-api/codex/models", s.models)
	mux.HandleFunc("/backend-api/codex/responses", s.responses)

	log.Printf("mock-provider listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, requestLogger(mux)); err != nil {
		log.Fatal(err)
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__mock/requests" && r.URL.Path != "/__mock/config" {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "mock-provider"})
}

func (s *server) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	s.state.mu.RLock()
	status := s.state.status
	delay := s.state.delay
	s.state.mu.RUnlock()
	if !sleepOrCancel(w, r, delay) {
		return
	}
	if status != http.StatusOK {
		writeError(w, status, "configured_mock_status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []any{map[string]any{
			"id": "mock-model", "object": "model", "owned_by": "mock-provider",
		}},
	})
}

func (s *server) responses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_failed")
		return
	}
	meta := metadata(r, body)
	s.state.mu.Lock()
	s.state.requests++
	meta.At = time.Now().UTC().Format(time.RFC3339Nano)
	status := s.state.status
	delay, chunkDelay := s.state.delay, s.state.chunkDelay
	stream, chunkSize := s.state.stream, s.state.chunkSize
	s.state.last = meta
	s.state.mu.Unlock()
	if !sleepOrCancel(w, r, delay) {
		return
	}
	if status != http.StatusOK {
		writeError(w, status, "configured_mock_status")
		return
	}

	if stream {
		writeSSE(w, r, chunkDelay, chunkSize)
		return
	}
	writeJSON(w, http.StatusOK, responsePayload())
}

func metadata(r *http.Request, body []byte) requestMeta {
	bodySum := sha256.Sum256(body)
	m := requestMeta{
		Method:        r.Method,
		Path:          r.URL.Path,
		ContentType:   r.Header.Get("Content-Type"),
		BodyBytes:     int64(len(body)),
		BodySHA256:    hex.EncodeToString(bodySum[:]),
		AuthPresent:   r.Header.Get("Authorization") != "",
		CookiePresent: r.Header.Get("Cookie") != "",
	}
	if token := r.Header.Get("Authorization"); token != "" {
		sum := sha256.Sum256([]byte(token))
		m.AuthSHA256 = hex.EncodeToString(sum[:])
	}
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) == nil {
		m.Model = payload.Model
	}
	return m
}

func (s *server) requests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"requests": s.state.requests,
		"last":     s.state.last,
	})
}

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.configSnapshot())
	case http.MethodPut:
		var input struct {
			Status       *int   `json:"status"`
			DelayMS      *int64 `json:"delay_ms"`
			ChunkDelayMS *int64 `json:"chunk_delay_ms"`
			Stream       *bool  `json:"stream"`
			ChunkSize    *int   `json:"chunk_size"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_config")
			return
		}
		s.state.mu.Lock()
		if input.Status != nil {
			if *input.Status < 100 || *input.Status > 599 {
				s.state.mu.Unlock()
				writeError(w, http.StatusBadRequest, "invalid_status")
				return
			}
			s.state.status = *input.Status
		}
		if input.DelayMS != nil {
			s.state.delay = time.Duration(*input.DelayMS) * time.Millisecond
		}
		if input.ChunkDelayMS != nil {
			s.state.chunkDelay = time.Duration(*input.ChunkDelayMS) * time.Millisecond
		}
		if input.Stream != nil {
			s.state.stream = *input.Stream
		}
		if input.ChunkSize != nil {
			if *input.ChunkSize < 1 || *input.ChunkSize > 1<<20 {
				s.state.mu.Unlock()
				writeError(w, http.StatusBadRequest, "invalid_chunk_size")
				return
			}
			s.state.chunkSize = *input.ChunkSize
		}
		s.state.mu.Unlock()
		writeJSON(w, http.StatusOK, s.configSnapshot())
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (s *server) configSnapshot() map[string]any {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	return map[string]any{
		"status":         s.state.status,
		"delay_ms":       s.state.delay.Milliseconds(),
		"chunk_delay_ms": s.state.chunkDelay.Milliseconds(),
		"stream":         s.state.stream,
		"chunk_size":     s.state.chunkSize,
	}
}

func writeSSE(w http.ResponseWriter, r *http.Request, chunkDelay time.Duration, chunkSize int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	f, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_not_supported")
		return
	}
	w.WriteHeader(http.StatusOK)
	// Emit complete Responses lifecycle events. chunk_size controls network byte
	// fragments, deliberately including fragments inside JSON and SSE boundaries.
	for sequence, event := range responseEvents() {
		event["sequence_number"] = sequence
		payload, _ := json.Marshal(event)
		frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event["type"], payload))
		size := chunkSize
		if size <= 0 {
			size = len(frame)
		}
		for i := 0; i < len(frame); i += size {
			end := i + size
			if end > len(frame) {
				end = len(frame)
			}
			if _, err := w.Write(frame[i:end]); err != nil {
				return
			}
			f.Flush()
			if !sleepOrCancel(w, r, chunkDelay) {
				return
			}
		}
	}
}

func responseEvents() []map[string]any {
	part := map[string]any{"type": "output_text", "text": "mock response", "annotations": []any{}}
	item := map[string]any{"id": "msg_mock_1", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	return []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": "resp_mock_1", "object": "response", "status": "in_progress", "output": []any{}}},
		{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_mock_1", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
		{"type": "response.content_part.added", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}},
		{"type": "response.output_text.delta", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "delta": "mock "},
		{"type": "response.output_text.delta", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "delta": "response"},
		{"type": "response.output_text.done", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "text": "mock response"},
		{"type": "response.content_part.done", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "part": part},
		{"type": "response.output_item.done", "output_index": 0, "item": item},
		{"type": "response.completed", "response": responsePayload()},
	}
}

func responsePayload() map[string]any {
	return map[string]any{
		"id": "resp_mock_1", "object": "response", "status": "completed", "model": "mock-model",
		"output": []any{map[string]any{
			"id": "msg_mock_1", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "mock response", "annotations": []any{}}},
		}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 2, "total_tokens": 3, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}},
	}
}

func sleepOrCancel(w http.ResponseWriter, r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": "mock_error", "code": code, "message": strings.ReplaceAll(code, "_", " ")}})
}
