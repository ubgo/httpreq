package httpreq

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"
)

// fakeTransport lets a test inject an exact response or error without a real
// server — used to exercise Do's read-body failure path deterministically.
type fakeTransport struct {
	resp *http.Response
	err  error
}

func (f fakeTransport) RoundTrip(*http.Request) (*http.Response, error) { return f.resp, f.err }

// errBody is a response body that fails on Read, so io.ReadAll returns an error.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("boom read") }
func (errBody) Close() error             { return nil }

// TestObserver_Success asserts the observer fires once on a 2xx with correct
// metadata and a nil error.
func TestObserver_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var traces []Trace
	var out struct{ OK bool }
	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPost),
		WithJSONBody(map[string]int{"a": 1}),
		WithResponseInto(&out),
		WithObserver(func(_ context.Context, t Trace) { traces = append(traces, t) }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("observer fired %d times, want 1", len(traces))
	}
	tr := traces[0]
	if tr.Err != nil {
		t.Errorf("Err = %v, want nil", tr.Err)
	}
	if tr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", tr.StatusCode)
	}
	if tr.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", tr.Method)
	}
	if tr.ReqBytes == 0 {
		t.Error("ReqBytes = 0, want >0 for a JSON body")
	}
	if tr.RespBytes == 0 {
		t.Error("RespBytes = 0, want >0")
	}
	if tr.Duration <= 0 {
		t.Error("Duration = 0, want >0")
	}
	if tr.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", tr.Attempt)
	}
}

// TestObserver_HTTPError asserts the observer fires with a *HTTPError on non-2xx.
func TestObserver_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	var tr Trace
	_, err := Do(context.Background(), srv.URL,
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if tr.StatusCode != http.StatusTeapot {
		t.Errorf("StatusCode = %d, want 418", tr.StatusCode)
	}
	if tr.Err == nil {
		t.Error("Trace.Err = nil, want *HTTPError")
	}
	if tr.RespBytes != len("nope") {
		t.Errorf("RespBytes = %d, want %d", tr.RespBytes, len("nope"))
	}
}

// TestObserver_NetworkError asserts the observer fires when the send itself
// fails (no response). StatusCode stays 0.
func TestObserver_NetworkError(t *testing.T) {
	var tr Trace
	fired := false
	_, err := Do(context.Background(), "http://127.0.0.1:1/", // nothing listens on port 1
		WithObserver(func(_ context.Context, t Trace) { fired = true; tr = t }),
	)
	if err == nil {
		t.Fatal("Do: want network error, got nil")
	}
	if !fired {
		t.Fatal("observer did not fire on network error")
	}
	if tr.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", tr.StatusCode)
	}
	if tr.Err == nil {
		t.Error("Trace.Err = nil, want network error")
	}
}

// TestObserver_DecodeError asserts the observer fires when the body is 2xx but
// not decodable into the target.
func TestObserver_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	var out struct{ N int }
	var tr Trace
	_, err := Do(context.Background(), srv.URL,
		WithResponseInto(&out),
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if err == nil {
		t.Fatal("Do: want decode error, got nil")
	}
	if tr.Err == nil {
		t.Error("Trace.Err = nil, want decode error")
	}
	if tr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", tr.StatusCode)
	}
}

// TestObserver_Multiple asserts every registered observer fires, in order.
func TestObserver_Multiple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var order []int
	_, err := Do(context.Background(), srv.URL,
		WithObserver(func(context.Context, Trace) { order = append(order, 1) }),
		WithObserver(func(context.Context, Trace) { order = append(order, 2) }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("observer order = %v, want [1 2]", order)
	}
}

// TestObserver_NoLeak asserts a Trace never carries auth headers or body bytes
// even when the request sets a bearer token and a body. This is the redaction
// contract, enforced by construction (Trace has no header/body fields), so we
// verify the whole struct against those secrets.
func TestObserver_NoLeak(t *testing.T) {
	const secret = "super-secret-token"
	const bodyMark = "sensitive-payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var tr Trace
	_, err := Do(context.Background(), srv.URL,
		WithBearerToken(secret),
		WithRawBody([]byte(bodyMark)),
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// Field-level check: URL is the only string field; it must not carry the
	// secret or body. (Method/status are non-string.)
	if strings.Contains(tr.URL, secret) || strings.Contains(tr.URL, bodyMark) {
		t.Errorf("Trace.URL leaked sensitive data: %q", tr.URL)
	}
}

// TestConnTrace_Populated asserts WithConnTrace fills phase timings against a
// TLS server: DNS may be skipped for a literal-IP host, but TLS and TTFB must
// be non-zero on a fresh connection.
func TestConnTrace_Populated(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))
	defer srv.Close()

	var tr Trace
	_, err := Do(context.Background(), srv.URL,
		WithHTTPClient(srv.Client()), // trusts the test server's cert
		WithConnTrace(),
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if tr.TLS <= 0 {
		t.Errorf("TLS = %v, want >0 on a fresh TLS connection", tr.TLS)
	}
	if tr.TTFB <= 0 {
		t.Errorf("TTFB = %v, want >0", tr.TTFB)
	}
}

// TestConnTrace_ZeroWithoutOption asserts phase fields stay zero when
// WithConnTrace is not set.
func TestConnTrace_ZeroWithoutOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	var tr Trace
	_, _ = Do(context.Background(), srv.URL,
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if tr.DNS != 0 || tr.Connect != 0 || tr.TLS != 0 || tr.TTFB != 0 {
		t.Errorf("phase timings non-zero without WithConnTrace: %+v", tr)
	}
}

// TestSlogObserver_SuccessAndNilLogger covers the success log path and the
// nil-logger fallback to slog.Default in one shot: passing nil must not panic
// and a 2xx must log at the requested level.
func TestSlogObserver_SuccessAndNilLogger(t *testing.T) {
	var buf bytes.Buffer
	// Point slog.Default at our buffer so the nil-logger branch is observable.
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithObserver(SlogObserver(nil, slog.LevelWarn)), // nil -> slog.Default()
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "httpreq request") {
		t.Errorf("success not logged at WARN via default logger; log = %q", out)
	}
}

// TestDo_NewRequestError covers the http.NewRequestWithContext failure path
// via an invalid method (a method string with a space is rejected).
func TestDo_NewRequestError(t *testing.T) {
	_, err := Do(context.Background(), "http://example.com",
		WithMethod("BAD METHOD"),
	)
	if err == nil {
		t.Fatal("Do: want new-request error, got nil")
	}
	if !strings.Contains(err.Error(), "new request") {
		t.Errorf("err = %v, want a 'new request' error", err)
	}
}

// TestDo_ReadBodyError covers the io.ReadAll failure path using a fake
// transport whose response body errors on Read. The observer must still fire
// with the read error and the status already recorded.
func TestDo_ReadBodyError(t *testing.T) {
	client := &http.Client{Transport: fakeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       errBody{},
		Header:     http.Header{},
	}}}

	var tr Trace
	_, err := Do(context.Background(), "http://x",
		WithHTTPClient(client),
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if err == nil || !strings.Contains(err.Error(), "read body") {
		t.Fatalf("err = %v, want a 'read body' error", err)
	}
	if tr.Err == nil {
		t.Error("Trace.Err = nil, want read error")
	}
	if tr.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200 (recorded before the read failed)", tr.StatusCode)
	}
}

// TestConnTimer_Unit drives the httptrace hooks directly so the DNS phase —
// which never fires for the literal-IP hosts used by httptest — is covered
// deterministically, along with the DNS branch of fill.
func TestConnTimer_Unit(t *testing.T) {
	c := &connTimer{}
	ct := c.clientTrace()

	ct.DNSStart(httptrace.DNSStartInfo{})
	ct.DNSDone(httptrace.DNSDoneInfo{})
	ct.ConnectStart("tcp", "1.2.3.4:443")
	ct.ConnectDone("tcp", "1.2.3.4:443", nil)
	ct.TLSHandshakeStart()
	ct.TLSHandshakeDone(tls.ConnectionState{}, nil)
	ct.GotFirstResponseByte()

	if c.dnsStart.IsZero() || c.dnsDone.IsZero() {
		t.Fatal("DNS hooks did not record timestamps")
	}

	var tr Trace
	// A start before firstByte guarantees a positive TTFB.
	c.fill(&tr, c.firstByte.Add(-time.Millisecond))
	if tr.TTFB <= 0 {
		t.Errorf("TTFB = %v, want >0", tr.TTFB)
	}
	// DNS/Connect/TLS branches executed (durations are >= 0 by construction).
	if tr.DNS < 0 || tr.Connect < 0 || tr.TLS < 0 {
		t.Errorf("negative phase duration: %+v", tr)
	}
}

// TestSlogObserver logs through a buffer handler and asserts the failure path
// logs at Error level while carrying no secret.
func TestSlogObserver(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _ = Do(context.Background(), srv.URL,
		WithBearerToken("leak-me-not"),
		WithObserver(SlogObserver(logger, slog.LevelInfo)),
	)
	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("failure not logged at ERROR level; log = %q", out)
	}
	if strings.Contains(out, "leak-me-not") {
		t.Errorf("slog output leaked bearer token: %q", out)
	}
}
