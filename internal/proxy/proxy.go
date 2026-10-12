// Package proxy relays /v1/chat/completions and /v1/completions requests
// to the vLLM backend chosen by internal/loadbalancer, unchanged (no field
// processing), including SSE streaming relay.
package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/seungkyua/llm-gateway/internal/apierror"
	"github.com/seungkyua/llm-gateway/internal/loadbalancer"
)

const (
	// defaultDialTimeout/defaultTLSHandshakeTimeout bound connection setup
	// to a backend; they do not limit how long a streaming response body
	// may remain open.
	defaultDialTimeout         = 5 * time.Second
	defaultTLSHandshakeTimeout = 5 * time.Second
)

// NewHTTPClient builds an *http.Client suitable for proxying, including
// long-lived SSE streaming responses (no overall response timeout).
func NewHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: defaultDialTimeout}).DialContext,
			TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
		},
	}
}

// Result summarizes one completed proxy call for metrics/logging purposes
// (see internal/metrics). ResponseBody is only populated for non-streaming
// responses (streaming bodies are relayed incrementally and never fully
// buffered).
type Result struct {
	BackendURL   string
	Status       int
	Stream       bool
	RequestBody  []byte
	ResponseBody []byte
	Start        time.Time
	ChunkTimes   []time.Time
	End          time.Time
}

// Proxy relays proxy API requests to a backend selected by a
// loadbalancer.LoadBalancer.
type Proxy struct {
	lb         *loadbalancer.LoadBalancer
	client     *http.Client
	onComplete func(*gin.Context, Result)
}

// Option configures optional Proxy behavior.
type Option func(*Proxy)

// WithOnComplete registers a callback invoked synchronously, after the
// response has been fully relayed to the client, with details needed for
// token counting/metrics/logging (internal/tokencount, internal/metrics).
// The gin.Context lets the caller read values set by earlier middleware
// (e.g. the authenticated key record, a request id).
func WithOnComplete(fn func(*gin.Context, Result)) Option {
	return func(p *Proxy) { p.onComplete = fn }
}

// New builds a Proxy. client is injected (not created internally) so
// callers control timeouts/transport configuration.
func New(lb *loadbalancer.LoadBalancer, client *http.Client, opts ...Option) *Proxy {
	p := &Proxy{lb: lb, client: client}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Handler returns a gin.HandlerFunc that proxies the current request.
func (p *Proxy) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		p.serve(c)
	}
}

func (p *Proxy) serve(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		apierror.Abort(c, http.StatusBadGateway, "failed to read request body", apierror.TypeAPIError, apierror.CodeBadGateway)
		return
	}

	backend, err := p.lb.Pick()
	if err != nil {
		apierror.Abort(c, http.StatusServiceUnavailable, "no available backend", apierror.TypeAPIError, apierror.CodeNoAvailableBackend)
		return
	}

	targetURL := backend.URL + c.Request.URL.Path
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		apierror.Abort(c, http.StatusBadGateway, "failed to build backend request", apierror.TypeAPIError, apierror.CodeBadGateway)
		return
	}
	contentType := c.GetHeader("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	if accept := c.GetHeader("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	// Deliberately not forwarding the client's Authorization header: the
	// gateway already authenticated it, and it must never reach the
	// backend or appear in logs (spec.md 로그의 민감정보).

	backend.IncInFlight()
	defer backend.DecInFlight()

	start := time.Now()

	resp, err := p.client.Do(req)
	if err != nil {
		apierror.Abort(c, http.StatusBadGateway, "backend request failed", apierror.TypeAPIError, apierror.CodeBadGateway)
		return
	}
	defer resp.Body.Close()

	respContentType := resp.Header.Get("Content-Type")
	if strings.Contains(respContentType, "text/event-stream") {
		p.relayStream(c, resp, backend.URL, body, start)
		return
	}
	p.relayNonStream(c, resp, respContentType, backend.URL, body, start)
}

func (p *Proxy) relayNonStream(c *gin.Context, resp *http.Response, contentType, backendURL string, requestBody []byte, start time.Time) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		apierror.Abort(c, http.StatusBadGateway, "failed to read backend response", apierror.TypeAPIError, apierror.CodeBadGateway)
		return
	}
	if len(bodyBytes) == 0 || strings.Contains(contentType, "text/html") {
		apierror.Abort(c, http.StatusBadGateway, "backend returned an invalid response", apierror.TypeAPIError, apierror.CodeBadGateway)
		return
	}
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, bodyBytes)

	if p.onComplete != nil {
		p.onComplete(c, Result{
			BackendURL:   backendURL,
			Status:       resp.StatusCode,
			Stream:       false,
			RequestBody:  requestBody,
			ResponseBody: bodyBytes,
			Start:        start,
			End:          time.Now(),
		})
	}
}

// sseEventDelimiter separates SSE events (a blank line).
var sseEventDelimiter = []byte("\n\n")

// errIncompleteSSEEvent signals that the stream ended (EOF, whether from a
// hard disconnect or a graceful peer close) while an SSE event was still
// buffered without its closing blank-line delimiter. splitSSEEvents
// surfaces it via bufio.Scanner.Err() so relayStream can treat "closed with
// an unfinished event" the same as "connection dropped" (spec.md 마일스톤
// 8, 인수 기준 19: TCP RST와 미완결 상태의 graceful close를 동일하게 취급).
var errIncompleteSSEEvent = errors.New("proxy: backend closed connection with an incomplete SSE event buffered")

// splitSSEEvents is a bufio.SplitFunc that tokenizes on blank-line-separated
// SSE events instead of individual lines. If the stream ends (EOF) while
// leftover bytes remain that never reached a closing blank line, that is
// treated as an error (errIncompleteSSEEvent) rather than a valid final
// event — regardless of whether the EOF came from a hard disconnect (TCP
// RST) or the peer closing the connection gracefully (FIN) mid-event.
func splitSSEEvents(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.Index(data, sseEventDelimiter); i >= 0 {
		return i + len(sseEventDelimiter), data[0:i], nil
	}
	if atEOF {
		if len(data) > 0 {
			return 0, nil, errIncompleteSSEEvent
		}
		return 0, nil, nil
	}
	return 0, nil, nil
}

func (p *Proxy) relayStream(c *gin.Context, resp *http.Response, backendURL string, requestBody []byte, start time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(resp.StatusCode)

	flusher, canFlush := c.Writer.(http.Flusher)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(splitSSEEvents)

	var chunkTimes []time.Time
	for scanner.Scan() {
		event := scanner.Bytes()
		if len(event) == 0 {
			continue
		}
		c.Writer.Write(event)
		c.Writer.Write(sseEventDelimiter)
		if canFlush {
			flusher.Flush()
		}
		chunkTimes = append(chunkTimes, time.Now())
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		// Backend connection dropped mid-stream: keep the chunks already
		// sent and terminate with an explicit error event instead of
		// hanging or panicking (spec.md: 부분 응답 유지 + 에러 이벤트).
		errBody, _ := json.Marshal(apierror.New("upstream connection lost while streaming", apierror.TypeAPIError, apierror.CodeBadGateway))
		c.Writer.Write([]byte("event: error\ndata: "))
		c.Writer.Write(errBody)
		c.Writer.Write(sseEventDelimiter)
		if canFlush {
			flusher.Flush()
		}
	}

	if p.onComplete != nil {
		p.onComplete(c, Result{
			BackendURL:  backendURL,
			Status:      resp.StatusCode,
			Stream:      true,
			RequestBody: requestBody,
			Start:       start,
			ChunkTimes:  chunkTimes,
			End:         time.Now(),
		})
	}
}
