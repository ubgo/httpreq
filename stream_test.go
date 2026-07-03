package httpreq

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failWriter fails on every Write, to exercise the stream-copy error path.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// TestWithRequest_MutatesAndOrders runs multiple hooks in order and lets them
// set both a header and a field net/http keeps off the header map (Host).
func TestWithRequest_MutatesAndOrders(t *testing.T) {
	var gotA, gotB, gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotA, gotB, gotHost = r.Header.Get("X-A"), r.Header.Get("X-B"), r.Host
	}))
	defer srv.Close()

	var order []string
	_, err := Do(context.Background(), srv.URL,
		WithRequest(func(req *http.Request) error {
			order = append(order, "first")
			req.Header.Set("X-A", "1")
			return nil
		}),
		WithRequest(func(req *http.Request) error {
			order = append(order, "second")
			req.Header.Set("X-B", "2")
			req.Host = "virtual.example"
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotA != "1" || gotB != "2" {
		t.Errorf("headers not applied: X-A=%q X-B=%q", gotA, gotB)
	}
	if gotHost != "virtual.example" {
		t.Errorf("Host = %q, want virtual.example", gotHost)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("hook order = %v, want [first second]", order)
	}
}

// TestWithRequest_ErrorAborts stops before sending when a hook errors.
func TestWithRequest_ErrorAborts(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithRequest(func(*http.Request) error { return errors.New("sign failed") }),
	)
	if err == nil || !strings.Contains(err.Error(), "request hook") {
		t.Fatalf("err = %v, want a 'request hook' error", err)
	}
	if hit {
		t.Error("request was sent despite the hook error")
	}
}

// TestWithRequest_Curl proves hooks run for Curl too, so a dump reflects them.
func TestWithRequest_Curl(t *testing.T) {
	got, err := Curl(context.Background(), "https://api.example.com/x",
		WithRequest(func(req *http.Request) error {
			req.Header.Set("X-Signature", "abc123")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Curl: %v", err)
	}
	if !strings.Contains(got, `-H 'X-Signature: abc123'`) {
		t.Errorf("curl missing hook-applied header: %q", got)
	}
}

// TestWithRequest_NilIgnored covers the nil-hook branch.
func TestWithRequest_NilIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := Do(context.Background(), srv.URL, WithRequest(nil)); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

// TestWithResponseWriter_Success streams the body to a writer and reports the
// copied byte count via the observer.
func TestWithResponseWriter_Success(t *testing.T) {
	const payload = "streamed-body-contents"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	var tr Trace
	_, err := Do(context.Background(), srv.URL,
		WithResponseWriter(&buf),
		WithObserver(func(_ context.Context, t Trace) { tr = t }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if buf.String() != payload {
		t.Errorf("streamed = %q, want %q", buf.String(), payload)
	}
	if tr.RespBytes != len(payload) {
		t.Errorf("RespBytes = %d, want %d", tr.RespBytes, len(payload))
	}
}

// TestWithResponseWriter_ErrorBuffered asserts an error response is NOT streamed
// to the writer but buffered into the HTTPError instead.
func TestWithResponseWriter_ErrorBuffered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	_, err := Do(context.Background(), srv.URL, WithResponseWriter(&buf))

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if buf.Len() != 0 {
		t.Errorf("error body was streamed to the writer: %q", buf.String())
	}
	if string(he.Body) != "upstream down" {
		t.Errorf("HTTPError.Body = %q, want 'upstream down'", he.Body)
	}
}

// TestWithResponseWriter_CopyError covers the io.Copy failure path.
func TestWithResponseWriter_CopyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some bytes"))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL, WithResponseWriter(failWriter{}))
	if err == nil || !strings.Contains(err.Error(), "stream body") {
		t.Fatalf("err = %v, want a 'stream body' error", err)
	}
}

// TestWithExpectStatus_Allows treats a listed non-2xx code as success: no
// HTTPError, and the body is still decoded.
func TestWithExpectStatus_Allows(t *testing.T) {
	// 304 Not Modified: not followed by http.Client, normally an HTTPError.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithExpectStatus(http.StatusNotModified),
	)
	if err != nil {
		t.Fatalf("304 with WithExpectStatus should be nil error, got %v", err)
	}

	// A body-bearing expected status decodes normally.
	var out struct{ OK bool }
	_, err = Do(context.Background(), redirectBodyServer(t), // 307 + JSON body
		WithExpectStatus(http.StatusTemporaryRedirect),
		WithResponseInto(&out),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !out.OK {
		t.Error("expected-status body was not decoded")
	}
}

// TestWithExpectStatus_UnlistedStillErrors confirms an unlisted non-2xx is
// still an HTTPError.
func TestWithExpectStatus_UnlistedStillErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithExpectStatus(http.StatusTeapot), // allow 418, not 304
	)
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusNotModified {
		t.Fatalf("err = %v, want *HTTPError with 304", err)
	}
}

// redirectBodyServer returns a server that responds 307 with a JSON body and
// no Location, so http.Client does not follow it and Do sees the 307.
func redirectBodyServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
