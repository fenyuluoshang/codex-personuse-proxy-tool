package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponsesRedactsCredentialAndReturnsSSE(t *testing.T) {
	s := &server{state: &settings{status: http.StatusOK, stream: true, chunkSize: 4}}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"fixture-model","input":"secret text"}`))
	r.Header.Set("Authorization", "Bearer test-token")
	r.Header.Set("Cookie", "session=secret")
	w := httptest.NewRecorder()
	s.responses(w, r)
	if got := w.Code; got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("missing done event: %s", body)
	}

	s.state.mu.RLock()
	meta := s.state.last
	s.state.mu.RUnlock()
	if !meta.AuthPresent || !meta.CookiePresent {
		t.Fatalf("sensitive-header presence was not recorded")
	}
	if meta.AuthSHA256 == "Bearer test-token" || strings.Contains(string(body), "secret text") {
		t.Fatalf("fixture leaked secret")
	}
	if meta.Model != "fixture-model" {
		t.Fatalf("model = %q", meta.Model)
	}
}

func TestConfigRejectsInvalidStatusAndSupportsJSONMode(t *testing.T) {
	s := &server{state: &settings{status: http.StatusOK, stream: true, chunkSize: 4}}
	bad := httptest.NewRequest(http.MethodPut, "/__mock/config", strings.NewReader(`{"status":99}`))
	bw := httptest.NewRecorder()
	s.config(bw, bad)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("invalid status code = %d", bw.Code)
	}

	good := httptest.NewRequest(http.MethodPut, "/__mock/config", strings.NewReader(`{"stream":false,"status":201}`))
	gw := httptest.NewRecorder()
	s.config(gw, good)
	if gw.Code != http.StatusOK {
		t.Fatalf("config code = %d", gw.Code)
	}
	s.state.mu.RLock()
	stream, status := s.state.stream, s.state.status
	s.state.mu.RUnlock()
	if stream || status != 201 {
		t.Fatalf("config = stream:%v status:%d", stream, status)
	}

	data, _ := io.ReadAll(gw.Result().Body)
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("config response: %v", err)
	}
	if got["stream"] != false {
		t.Fatalf("stream response = %#v", got["stream"])
	}
}
