// Package httpreq is a small JSON-over-HTTP client convenience layer on
// top of net/http.
//
// The standard library is fine but verbose for the most common service
// call: marshal a request body, set headers, send with a timeout, decode
// a JSON response, and turn non-2xx into an error. This package collapses
// that into a single options-pattern call:
//
//	var resp UserResponse
//	httpResp, err := httpreq.Do(ctx, "https://api/users",
//	    httpreq.WithMethod(http.MethodPost),
//	    httpreq.WithJSONBody(req),
//	    httpreq.WithBearerToken(token),
//	    httpreq.WithTimeout(5*time.Second),
//	    httpreq.WithResponseInto(&resp),
//	)
//
// Observability is built in and dependency-free: register a callback with
// [WithObserver] to receive a [Trace] (method, status, byte counts, duration,
// typed error) once per attempt, add [WithConnTrace] for DNS/TLS/TTFB phase
// timing via net/http/httptrace, or drop in [SlogObserver] for structured
// logging through log/slog. Traces carry metadata only — no bodies, no
// headers — so nothing sensitive leaks into your logs by accident.
//
// Beyond the basics it covers common day-to-day needs without leaving the
// standard library: bearer and basic auth ([WithBearerToken], [WithBasicAuth]),
// JSON / form / raw bodies ([WithJSONBody], [WithFormBody], [WithRawBody]),
// and flexible response handling — decode JSON ([WithResponseInto]), grab the
// raw bytes for non-JSON payloads ([WithResponseBytes]), stream large downloads
// to a writer ([WithResponseWriter]), or decode a structured error body on
// non-2xx ([WithErrorInto]). [WithExpectStatus] accepts extra status codes as
// success (e.g. 304), and [WithRequest] is an escape hatch to mutate or sign
// the built request before it is sent.
//
// For debugging, render any request as a runnable curl command: [WithCurl]
// hands the string to a callback just before [Do] sends, [Curl] returns it as
// a value without sending, and [RequestCurl] renders an arbitrary
// [*http.Request].
//
// What's deliberately NOT here:
//
//   - Retries, circuit breaking, rate limiting. Use a dedicated transport
//     or library for those concerns.
//   - GraphQL helpers, Shopify-specific extensions, etc. The lace.io
//     ancestor of this package mixed those in; they have been removed.
//   - A global default client. Pass [WithHTTPClient] explicitly if you
//     want to share connection pools.
package httpreq

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Version is the package version, used only to build [DefaultUserAgent]. Bump
// it on release.
const Version = "0.2.0"

// DefaultUserAgent is sent as the User-Agent header when the caller sets none.
// To suppress it entirely, set an empty User-Agent via WithUserAgent("").
const DefaultUserAgent = "httpreq/" + Version

// HTTPError is returned when the response status is outside 2xx. The raw
// response body is captured in Body so callers can decode their own error
// shape if needed.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       []byte
}

func (e *HTTPError) Error() string {
	if len(e.Body) > 0 {
		return fmt.Sprintf("httpreq: %s: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("httpreq: %s", e.Status)
}

// Option configures a single request.
type Option func(*request) error

type request struct {
	method       string
	headers      http.Header
	queryParams  url.Values
	body         io.Reader
	reqBytes     int
	timeout      time.Duration
	client       *http.Client
	respInto     any
	rawRespInto  *[]byte
	errInto      any
	observers    []func(context.Context, Trace)
	connTrace    bool
	curlFn       func(string)
	reqFns       []func(*http.Request) error
	respWriter   io.Writer
	expectStatus map[int]bool
}

// Trace is metadata about one completed request attempt, delivered to the
// callbacks registered via [WithObserver]. It fires once per attempt on
// every path — success, non-2xx ([HTTPError]), network failure, and decode
// failure — so it is a complete lifecycle signal for logs, metrics, and
// spans.
//
// SHAPE CONTRACT: Trace carries metadata ONLY. It never contains request or
// response bodies and never contains headers (so no Authorization / cookies
// / API keys leak into your logs by accident). If you need header or body
// content in your observability, install a custom [http.RoundTripper] via
// [WithHTTPClient] where you own the redaction policy.
//
// The DNS/Connect/TLS/TTFB fields are populated only when [WithConnTrace] is
// set; otherwise they are zero. Connect and TLS are zero on a reused
// keep-alive connection (no new dial/handshake happened).
type Trace struct {
	Method     string
	URL        string        // final URL including query params
	ReqBytes   int           // request body size; 0 for bodyless or streamed bodies
	RespBytes  int           // response body size actually read
	StatusCode int           // 0 if the request never got a response
	Duration   time.Duration // wall time around send + full body read
	Err        error         // nil on success; network err, *HTTPError, or decode err
	Attempt    int           // 1 today; reserved for a future retry feature

	// Connection-phase timings, set only with WithConnTrace(). See the type
	// doc for the keep-alive caveat.
	DNS     time.Duration
	Connect time.Duration
	TLS     time.Duration
	TTFB    time.Duration // start of send to first response byte
}

// WithMethod sets the HTTP method. Default: GET. Methods are not
// validated against [http.Method*] constants — the request goes out as
// you specify it.
func WithMethod(m string) Option {
	return func(r *request) error {
		r.method = m
		return nil
	}
}

// WithHeader sets a single header. Repeat the option to set multiple. To
// set repeated values for the same header (e.g. multi-value Accept), call
// it once per value.
func WithHeader(key, value string) Option {
	return func(r *request) error {
		r.headers.Add(key, value)
		return nil
	}
}

// WithHeaders adds every key/value in h to the request, appending to any
// headers already set (same semantics as calling [WithHeader] once per value).
// A nil or empty h is a no-op.
func WithHeaders(h http.Header) Option {
	return func(r *request) error {
		for key, values := range h {
			for _, v := range values {
				r.headers.Add(key, v)
			}
		}
		return nil
	}
}

// WithBearerToken sets `Authorization: Bearer <token>`. No-op if the
// token is empty.
func WithBearerToken(token string) Option {
	return func(r *request) error {
		if token == "" {
			return nil
		}
		r.headers.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// WithBasicAuth sets `Authorization: Basic <base64(user:password)>`. It
// overwrites any Authorization set earlier (e.g. via [WithBearerToken]) —
// last auth option wins.
func WithBasicAuth(user, password string) Option {
	return func(r *request) error {
		enc := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
		r.headers.Set("Authorization", "Basic "+enc)
		return nil
	}
}

// WithUserAgent sets the User-Agent header. Pass an empty string to suppress
// the User-Agent entirely (no header sent). When this option is not used,
// [DefaultUserAgent] is sent.
func WithUserAgent(ua string) Option {
	return func(r *request) error {
		r.headers.Set("User-Agent", ua)
		return nil
	}
}

// WithQueryParam appends a URL query parameter. Repeat for multiple
// values of the same key.
func WithQueryParam(key, value string) Option {
	return func(r *request) error {
		r.queryParams.Add(key, value)
		return nil
	}
}

// WithQuery adds every key/value in q to the URL query string, appending to
// any parameters already set (same semantics as calling [WithQueryParam] once
// per value). A nil or empty q is a no-op.
func WithQuery(q url.Values) Option {
	return func(r *request) error {
		for key, values := range q {
			for _, v := range values {
				r.queryParams.Add(key, v)
			}
		}
		return nil
	}
}

// WithJSONBody marshals body as JSON and sets Content-Type:
// application/json. Pass nil to clear a previously set body.
func WithJSONBody(body any) Option {
	return func(r *request) error {
		if body == nil {
			r.body = nil
			return nil
		}
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("httpreq: marshal body: %w", err)
		}
		r.body = bytes.NewReader(data)
		r.reqBytes = len(data)
		r.headers.Set("Content-Type", "application/json")
		return nil
	}
}

// WithRawBody sends body bytes verbatim. Caller must set Content-Type via
// [WithHeader] if it matters. Useful for form posts, protobuf, or
// pre-encoded payloads.
func WithRawBody(body []byte) Option {
	return func(r *request) error {
		r.body = bytes.NewReader(body)
		r.reqBytes = len(body)
		return nil
	}
}

// WithFormBody URL-encodes values and sends them as an
// application/x-www-form-urlencoded body — the shape OAuth token endpoints and
// classic HTML form posts expect. Pass nil to clear a previously set body.
func WithFormBody(values url.Values) Option {
	return func(r *request) error {
		if values == nil {
			r.body = nil
			return nil
		}
		encoded := values.Encode()
		r.body = strings.NewReader(encoded)
		r.reqBytes = len(encoded)
		r.headers.Set("Content-Type", "application/x-www-form-urlencoded")
		return nil
	}
}

// WithTimeout caps the total request time including dialing, headers,
// body, and response reading. Default: 30 seconds. Pass 0 to use the
// caller's context deadline alone.
func WithTimeout(d time.Duration) Option {
	return func(r *request) error {
		r.timeout = d
		return nil
	}
}

// WithHTTPClient overrides the underlying [*http.Client]. Use this to
// share a connection pool across requests or to install custom
// middleware via a transport. WithTimeout still applies — it sets
// Client.Timeout if the supplied client has none.
func WithHTTPClient(c *http.Client) Option {
	return func(r *request) error {
		if c == nil {
			return errors.New("httpreq: nil http client")
		}
		r.client = c
		return nil
	}
}

// WithRequest registers a hook that receives the fully built [*http.Request]
// after all other options are applied and just before it is sent, letting you
// set anything the options don't model: request signing (an auth header
// computed over the assembled request), fields net/http keeps off the header
// map ([http.Request.Host], cookies via [http.Request.AddCookie],
// [http.Request.Close], [http.Request.Trailer]), or any one-off tweak.
//
// Returning an error aborts [Do] (and [Curl]) before sending, wrapped as
// "httpreq: request hook". Repeat the option to chain hooks; they run in order.
// The hook also runs for [Curl]/[WithCurl], so a curl dump reflects the final,
// mutated request. A nil hook is ignored.
//
// This is an escape hatch — with it comes the ability to build an invalid
// request. Prefer a dedicated option when one exists.
func WithRequest(fn func(*http.Request) error) Option {
	return func(r *request) error {
		if fn != nil {
			r.reqFns = append(r.reqFns, fn)
		}
		return nil
	}
}

// WithResponseInto JSON-decodes the response body into v. v must be a
// pointer. Skip this option to discard the body.
func WithResponseInto(v any) Option {
	return func(r *request) error {
		r.respInto = v
		return nil
	}
}

// WithResponseBytes captures the raw response body into *b, regardless of
// status and regardless of whether [WithResponseInto] is also set. Use it for
// non-JSON responses (HTML, text, XML, binary) or when you need the exact
// bytes alongside a decoded value. On an empty response *b is set to an empty,
// non-nil slice.
func WithResponseBytes(b *[]byte) Option {
	return func(r *request) error {
		r.rawRespInto = b
		return nil
	}
}

// WithErrorInto JSON-decodes the response body into v when the status is
// non-2xx (i.e. when an [HTTPError] is returned), letting you read a structured
// error payload. v must be a pointer. The [HTTPError] is still returned; if the
// error body is not valid JSON, v is left unchanged and no extra error is
// raised (inspect HTTPError.Body for the raw bytes).
func WithErrorInto(v any) Option {
	return func(r *request) error {
		r.errInto = v
		return nil
	}
}

// WithResponseWriter streams a successful response body straight to w via
// io.Copy instead of buffering it in memory — the way to handle downloads or
// responses larger than RAM. Compose sinks with [io.MultiWriter] (e.g. write
// to a file and a hash at once).
//
// It applies only to success responses (status < 300, or a code allowed by
// [WithExpectStatus]). Error responses are still buffered so the [HTTPError]
// keeps the raw body. Because the body is streamed, not held, this option is
// mutually exclusive with [WithResponseInto] and [WithResponseBytes] on the
// same call — the writer takes precedence and those are not populated. A nil w
// is ignored (the body is buffered as usual).
func WithResponseWriter(w io.Writer) Option {
	return func(r *request) error {
		r.respWriter = w
		return nil
	}
}

// WithExpectStatus marks additional status codes as success, so they return a
// nil error instead of an [HTTPError] and their body is decoded/streamed like
// any 2xx. The classic case is 304 Not Modified with conditional requests, or
// a 3xx you handle yourself. Codes already in 2xx need not be listed.
func WithExpectStatus(codes ...int) Option {
	return func(r *request) error {
		if r.expectStatus == nil {
			r.expectStatus = make(map[int]bool, len(codes))
		}
		for _, c := range codes {
			r.expectStatus[c] = true
		}
		return nil
	}
}

// WithObserver registers a callback invoked exactly once when the request
// attempt completes — on success and on every failure path. See [Trace] for
// what is delivered (metadata only; no bodies or headers). Repeat the option
// to register multiple observers; all are called in registration order.
//
// The callback runs synchronously on the calling goroutine after the body
// has been read, so keep it fast: record a metric, emit a log line, annotate
// a span. Do not block on I/O inside it. A nil fn is ignored.
func WithObserver(fn func(context.Context, Trace)) Option {
	return func(r *request) error {
		if fn != nil {
			r.observers = append(r.observers, fn)
		}
		return nil
	}
}

// WithConnTrace enables connection-phase timing via [net/http/httptrace],
// populating the DNS/Connect/TLS/TTFB fields of the [Trace] passed to
// observers. It has a small per-request overhead and only matters when an
// observer is also registered. On a reused keep-alive connection the
// Connect and TLS fields stay zero because no new dial or handshake ran.
func WithConnTrace() Option {
	return func(r *request) error {
		r.connTrace = true
		return nil
	}
}

// WithCurl registers a callback that receives the request rendered as a
// runnable `curl` command string, invoked once by [Do] immediately before the
// request is sent — so it fires even when the send later fails. Use it to log
// or print exactly what goes on the wire.
//
// SECURITY: the rendered command reproduces the FULL request, including the
// Authorization header, cookies, and body. That is the point — it is a faithful
// dump — but it means secrets appear in whatever you do with the string. Redact
// before logging to a shared sink. A nil fn is ignored.
//
// To obtain the curl string as a value without sending, use [Curl]; to render
// an arbitrary request you already hold, use [RequestCurl].
func WithCurl(fn func(curl string)) Option {
	return func(r *request) error {
		r.curlFn = fn
		return nil
	}
}

// SlogObserver returns an observer that logs each completed request through
// l at the given level. Failures (non-nil [Trace.Err]) are logged at
// [slog.LevelError] regardless of level. Only metadata is logged — never
// bodies or headers. If l is nil, [slog.Default] is used.
//
// Wire it in with [WithObserver]:
//
//	httpreq.WithObserver(httpreq.SlogObserver(logger, slog.LevelInfo))
func SlogObserver(l *slog.Logger, level slog.Level) func(context.Context, Trace) {
	if l == nil {
		l = slog.Default()
	}
	return func(ctx context.Context, t Trace) {
		args := []any{
			"method", t.Method,
			"url", t.URL,
			"status", t.StatusCode,
			"duration", t.Duration,
			"req_bytes", t.ReqBytes,
			"resp_bytes", t.RespBytes,
		}
		if t.Err != nil {
			l.Log(ctx, slog.LevelError, "httpreq request failed", append(args, "err", t.Err)...)
			return
		}
		l.Log(ctx, level, "httpreq request", args...)
	}
}

// Do builds and sends the request. The returned *http.Response has its
// Body already drained and closed; the body bytes have been routed into
// the option supplied via [WithResponseInto], if any.
//
// Non-2xx responses return an *HTTPError so the caller can inspect the
// raw body. JSON decode errors are wrapped and returned.
func Do(ctx context.Context, endpoint string, opts ...Option) (*http.Response, error) {
	r, err := buildRequestState(opts)
	if err != nil {
		return nil, err
	}

	// connTrace is attached to the context before the request is built so
	// httptrace fires for the whole exchange, including dial and TLS.
	var ct *connTimer
	if r.connTrace {
		ct = &connTimer{}
		ctx = httptrace.WithClientTrace(ctx, ct.clientTrace())
	}

	req, finalURL, err := r.newRequest(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	// Render curl before sending so a later failure still gets logged.
	if r.curlFn != nil {
		if c, cerr := RequestCurl(req); cerr == nil {
			r.curlFn(c)
		}
	}

	client := r.client
	if client == nil {
		client = &http.Client{Timeout: r.timeout}
	} else if client.Timeout == 0 && r.timeout > 0 {
		clone := *client
		clone.Timeout = r.timeout
		client = &clone
	}

	// Observer bookkeeping. tr is mutated as the attempt progresses and read
	// in the deferred fire, so every return path below reports correctly.
	// start is set immediately before the send so Duration and TTFB are
	// measured from the same origin.
	tr := Trace{Method: r.method, URL: finalURL, ReqBytes: r.reqBytes, Attempt: 1}
	start := time.Now()
	if len(r.observers) > 0 {
		defer func() {
			tr.Duration = time.Since(start)
			if ct != nil {
				ct.fill(&tr, start)
			}
			for _, obs := range r.observers {
				obs(ctx, tr)
			}
		}()
	}

	res, err := client.Do(req)
	if err != nil {
		err = fmt.Errorf("httpreq: do: %w", err)
		tr.Err = err
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	tr.StatusCode = res.StatusCode

	// A status is an error unless it is 2xx or explicitly allowed via
	// WithExpectStatus.
	isErr := res.StatusCode >= 300 && !r.expectStatus[res.StatusCode]

	// Stream a successful body straight to the writer without buffering. Error
	// bodies fall through to the buffered path so the HTTPError keeps them.
	if !isErr && r.respWriter != nil {
		n, err := io.Copy(r.respWriter, res.Body)
		tr.RespBytes = int(n)
		if err != nil {
			err = fmt.Errorf("httpreq: stream body: %w", err)
			tr.Err = err
			return res, err
		}
		return res, nil
	}

	body, err := io.ReadAll(res.Body)
	tr.RespBytes = len(body)
	if err != nil {
		err = fmt.Errorf("httpreq: read body: %w", err)
		tr.Err = err
		return res, err
	}

	// Hand out the raw bytes regardless of status or decode target.
	if r.rawRespInto != nil {
		*r.rawRespInto = body
	}

	if isErr {
		herr := &HTTPError{
			StatusCode: res.StatusCode,
			Status:     res.Status,
			Body:       body,
		}
		// Best-effort decode of a structured error payload. A non-JSON body
		// leaves errInto untouched; the HTTPError is authoritative regardless.
		if r.errInto != nil && len(body) > 0 {
			_ = json.Unmarshal(body, r.errInto)
		}
		tr.Err = herr
		return res, herr
	}

	if r.respInto != nil && len(body) > 0 {
		if err := json.Unmarshal(body, r.respInto); err != nil {
			err = fmt.Errorf("httpreq: decode response: %w", err)
			tr.Err = err
			return res, err
		}
	}

	return res, nil
}

// connTimer captures httptrace connection-phase timestamps. Each hook is
// called on the client's connection goroutine; a single request never fires
// the same phase concurrently, so plain fields are safe without locking.
type connTimer struct {
	dnsStart, dnsDone   time.Time
	connStart, connDone time.Time
	tlsStart, tlsDone   time.Time
	firstByte           time.Time
}

func (c *connTimer) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { c.dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { c.dnsDone = time.Now() },
		ConnectStart:         func(_, _ string) { c.connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { c.connDone = time.Now() },
		TLSHandshakeStart:    func() { c.tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { c.tlsDone = time.Now() },
		GotFirstResponseByte: func() { c.firstByte = time.Now() },
	}
}

// fill computes phase durations into tr. Fields stay zero when the phase did
// not run (e.g. TLS on plain HTTP, or Connect/DNS on a reused keep-alive
// connection). start is the send origin used for TTFB.
func (c *connTimer) fill(tr *Trace, start time.Time) {
	if !c.dnsStart.IsZero() && !c.dnsDone.IsZero() {
		tr.DNS = c.dnsDone.Sub(c.dnsStart)
	}
	if !c.connStart.IsZero() && !c.connDone.IsZero() {
		tr.Connect = c.connDone.Sub(c.connStart)
	}
	if !c.tlsStart.IsZero() && !c.tlsDone.IsZero() {
		tr.TLS = c.tlsDone.Sub(c.tlsStart)
	}
	if !c.firstByte.IsZero() {
		tr.TTFB = c.firstByte.Sub(start)
	}
}

// buildRequestState applies the options onto a fresh request with the package
// defaults. Shared by [Do] and [Curl] so both interpret options identically.
func buildRequestState(opts []Option) (*request, error) {
	r := &request{
		method:      http.MethodGet,
		headers:     http.Header{},
		queryParams: url.Values{},
		timeout:     30 * time.Second,
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// newRequest builds the *http.Request from the resolved state, returning the
// final URL (endpoint plus query params) alongside it. It does not send.
func (r *request) newRequest(ctx context.Context, endpoint string) (*http.Request, string, error) {
	finalURL, err := buildURL(endpoint, r.queryParams)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, r.method, finalURL, r.body)
	if err != nil {
		return nil, "", fmt.Errorf("httpreq: new request: %w", err)
	}
	req.Header = r.headers
	// Apply the default User-Agent only when the caller set none. A caller that
	// wants no User-Agent uses WithUserAgent("") — the key is then present with
	// an empty value and we leave it alone. Done here (not at send) so a curl
	// dump reflects exactly what goes on the wire.
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header.Set("User-Agent", DefaultUserAgent)
	}
	// Request hooks run last so they see the final request (headers, UA, body)
	// — e.g. to sign it. An error here aborts before sending.
	for _, fn := range r.reqFns {
		if err := fn(req); err != nil {
			return nil, "", fmt.Errorf("httpreq: request hook: %w", err)
		}
	}
	return req, finalURL, nil
}

// Curl builds the request from the given options and returns it rendered as a
// runnable `curl` command string, WITHOUT sending anything. It is the value
// form of [WithCurl]: use it to print a request for docs, a dry run, or a debug
// log, then send the same options through [Do] when ready.
//
// SECURITY: the returned command contains the full request including the
// Authorization header and body — see [WithCurl] for the redaction note.
func Curl(ctx context.Context, endpoint string, opts ...Option) (string, error) {
	r, err := buildRequestState(opts)
	if err != nil {
		return "", err
	}
	req, _, err := r.newRequest(ctx, endpoint)
	if err != nil {
		return "", err
	}
	return RequestCurl(req)
}

// RequestCurl renders any [*http.Request] as a runnable, copy-pasteable `curl`
// command string. Headers are emitted in sorted order for stable output;
// values are shell-quoted so the command survives special characters. The
// method is included as -X for anything other than GET.
//
// The body is read via req.GetBody when available (as it is for requests built
// by this package), so the request's own body is left intact and the request
// stays sendable afterward. If GetBody is nil, req.Body is consumed and then
// restored with an equivalent reader.
//
// SECURITY: the output reproduces the full request, secrets included. Redact
// before writing to a shared log.
func RequestCurl(req *http.Request) (string, error) {
	var b strings.Builder
	b.WriteString("curl")

	if req.Method != "" && req.Method != http.MethodGet {
		b.WriteString(" -X ")
		b.WriteString(req.Method)
	}

	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range req.Header[k] {
			b.WriteString(" -H ")
			b.WriteString(shellQuote(k + ": " + v))
		}
	}

	body, err := requestBodyBytes(req)
	if err != nil {
		return "", fmt.Errorf("httpreq: read body for curl: %w", err)
	}
	if len(body) > 0 {
		b.WriteString(" --data-raw ")
		b.WriteString(shellQuote(string(body)))
	}

	if req.URL != nil {
		b.WriteString(" ")
		b.WriteString(shellQuote(req.URL.String()))
	}
	return b.String(), nil
}

// requestBodyBytes returns the request body without disturbing req.Body,
// preferring the replayable req.GetBody and falling back to a read-and-restore.
func requestBodyBytes(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(data))
	return data, nil
}

// shellQuote wraps s in single quotes for a POSIX shell, escaping any embedded
// single quote via the '\” idiom so the whole token is safe to paste.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func buildURL(endpoint string, q url.Values) (string, error) {
	if len(q) == 0 {
		return endpoint, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("httpreq: parse url: %w", err)
	}
	existing := u.Query()
	for k, vs := range q {
		for _, v := range vs {
			existing.Add(k, v)
		}
	}
	u.RawQuery = existing.Encode()
	return u.String(), nil
}
