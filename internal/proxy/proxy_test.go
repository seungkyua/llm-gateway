package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"llm-gateway/internal/loadbalancer"
)

func newTestRouter(t *testing.T, backendURL string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var lb *loadbalancer.LoadBalancer
	if backendURL == "" {
		lb = loadbalancer.New(nil, &loadbalancer.RoundRobinHealthcheck{})
	} else {
		b := loadbalancer.NewBackend(backendURL)
		b.SetStatus(loadbalancer.Healthy)
		lb = loadbalancer.New([]*loadbalancer.Backend{b}, &loadbalancer.RoundRobinHealthcheck{})
	}

	p := New(lb, &http.Client{})
	r := gin.New()
	r.POST("/v1/chat/completions", p.Handler())
	return r
}

func TestHandlerShouldRelayResponseBodyUnchangedWhenBackendRespondsNormally(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer backend.Close()
	r := newTestRouter(t, backend.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","messages":[]}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "chatcmpl-1") {
		t.Errorf("expected backend body to be relayed unchanged, got %s", w.Body.String())
	}
}

func TestHandlerShouldReturn503WhenNoBackendAvailable(t *testing.T) {
	r := newTestRouter(t, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "no_available_backend" {
		t.Errorf("expected no_available_backend, got %v", resp["error"])
	}
}

func TestHandlerShouldRelayBackend5xxStatusAndBodyUnchanged(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"backend exploded"}`))
	}))
	defer backend.Close()
	r := newTestRouter(t, backend.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 passed through, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "backend exploded") {
		t.Errorf("expected backend error body relayed unchanged, got %s", w.Body.String())
	}
}

func TestHandlerShouldReturn502WhenBackendRespondsWithEmptyBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	r := newTestRouter(t, backend.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"]["code"] != "bad_gateway" {
		t.Errorf("expected bad_gateway, got %v", resp["error"])
	}
}

func TestHandlerShouldReturn502WhenBackendRespondsWithHTML(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body>Bad Gateway</body></html>"))
	}))
	defer backend.Close()
	r := newTestRouter(t, backend.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerShouldRelayAllSSEChunksAndSetEventStreamContentTypeWhenStreaming(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			w.Write([]byte("data: {\"chunk\":" + string(rune('0'+i)) + "}\n\n"))
			flusher.Flush()
		}
	}))
	defer backend.Close()
	r := newTestRouter(t, backend.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","stream":true}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream content type, got %q", ct)
	}
	chunkCount := strings.Count(w.Body.String(), "data: {\"chunk\":")
	if chunkCount != 3 {
		t.Errorf("expected 3 chunks relayed, got %d: %s", chunkCount, w.Body.String())
	}
}

// TestHandlerShouldSendErrorEventAfterPartialChunksWhenConnectionDropsMidStream
// uses a raw TCP listener (rather than httptest.Server) so we can hijack and
// abruptly close the connection mid-response, simulating a backend crash
// during streaming.
func TestHandlerShouldSendErrorEventAfterPartialChunksWhenConnectionDropsMidStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the request headers/body (best effort).
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n"))
		conn.Write([]byte("1e\r\ndata: {\"chunk\":0}\n\n\r\n"))
		time.Sleep(20 * time.Millisecond)
		// Abruptly close without a terminating zero-length chunk, simulating
		// a backend crash mid-stream.
	}()
	backendURL := "http://" + ln.Addr().String()
	defer ln.Close()

	r := newTestRouter(t, backendURL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"m","stream":true}`))
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		r.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after backend connection dropped (possible hang)")
	}

	if !strings.Contains(w.Body.String(), "chunk\":0") {
		t.Errorf("expected partial chunk to be preserved, got %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("expected an error event to terminate the stream, got %s", w.Body.String())
	}
}
