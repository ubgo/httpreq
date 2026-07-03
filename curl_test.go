package httpreq

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCurl_GET renders a bare GET: no -X, no body, just the quoted URL.
func TestCurl_GET(t *testing.T) {
	got, err := Curl(context.Background(), "https://api.example.com/x",
		WithQueryParam("q", "1"),
	)
	if err != nil {
		t.Fatalf("Curl: %v", err)
	}
	if strings.Contains(got, "-X") {
		t.Errorf("GET curl should omit -X: %q", got)
	}
	if !strings.Contains(got, "'https://api.example.com/x?q=1'") {
		t.Errorf("URL missing or unquoted: %q", got)
	}
}

// TestCurl_PostJSON renders method, sorted headers, and body.
func TestCurl_PostJSON(t *testing.T) {
	got, err := Curl(context.Background(), "https://api.example.com/users",
		WithMethod(http.MethodPost),
		WithBearerToken("tok"),
		WithJSONBody(map[string]int{"a": 1}),
	)
	if err != nil {
		t.Fatalf("Curl: %v", err)
	}
	want := `curl -X POST -H 'Authorization: Bearer tok' -H 'Content-Type: application/json' -H 'User-Agent: httpreq/` + Version + `' --data-raw '{"a":1}' 'https://api.example.com/users'`
	if got != want {
		t.Errorf("curl mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestCurl_ShellEscaping asserts embedded single quotes are escaped so the
// command survives a paste into a POSIX shell.
func TestCurl_ShellEscaping(t *testing.T) {
	got, err := Curl(context.Background(), "https://api.example.com/x",
		WithMethod(http.MethodPost),
		WithHeader("X-Note", "it's fine"),
		WithRawBody([]byte(`a'b`)),
	)
	if err != nil {
		t.Fatalf("Curl: %v", err)
	}
	if !strings.Contains(got, `-H 'X-Note: it'\''s fine'`) {
		t.Errorf("header single-quote not escaped: %q", got)
	}
	if !strings.Contains(got, `--data-raw 'a'\''b'`) {
		t.Errorf("body single-quote not escaped: %q", got)
	}
}

// TestCurl_OptionError propagates an option error (nil client).
func TestCurl_OptionError(t *testing.T) {
	_, err := Curl(context.Background(), "https://x", WithHTTPClient(nil))
	if err == nil {
		t.Fatal("Curl: want option error, got nil")
	}
}

// TestCurl_NewRequestError propagates a build error (invalid method).
func TestCurl_NewRequestError(t *testing.T) {
	_, err := Curl(context.Background(), "https://x", WithMethod("BAD METHOD"))
	if err == nil || !strings.Contains(err.Error(), "new request") {
		t.Fatalf("err = %v, want a 'new request' error", err)
	}
}

// TestWithCurl_FiresAndDoesNotConsumeBody proves the callback receives the
// rendered command during Do AND that the real body still reaches the server.
func TestWithCurl_FiresAndDoesNotConsumeBody(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var curl string
	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPost),
		WithRawBody([]byte("payload-123")),
		WithCurl(func(c string) { curl = c }),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(curl, `--data-raw 'payload-123'`) {
		t.Errorf("curl missing body: %q", curl)
	}
	if received != "payload-123" {
		t.Errorf("server got %q, want payload-123 (body was consumed by curl render)", received)
	}
}

// TestRequestCurl_NilURLNoBody covers the bare-request branches: empty method
// (no -X), nil body, nil URL.
func TestRequestCurl_NilURLNoBody(t *testing.T) {
	got, err := RequestCurl(&http.Request{Header: http.Header{}})
	if err != nil {
		t.Fatalf("RequestCurl: %v", err)
	}
	if got != "curl" {
		t.Errorf("got %q, want \"curl\"", got)
	}
}

// TestRequestCurl_GetBodyFallback covers the read-and-restore path taken when
// req.GetBody is nil: the body is rendered and the request stays sendable.
func TestRequestCurl_GetBodyFallback(t *testing.T) {
	u, _ := url.Parse("http://x/y")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader("hello")),
	}
	got, err := RequestCurl(req)
	if err != nil {
		t.Fatalf("RequestCurl: %v", err)
	}
	if !strings.Contains(got, `--data-raw 'hello'`) {
		t.Errorf("body missing: %q", got)
	}
	// Body must be restored, not consumed.
	restored, _ := io.ReadAll(req.Body)
	if string(restored) != "hello" {
		t.Errorf("body not restored: got %q", restored)
	}
}

// TestRequestCurl_GetBodyError covers the GetBody error branch.
func TestRequestCurl_GetBodyError(t *testing.T) {
	u, _ := url.Parse("http://x")
	req := &http.Request{
		Method:  http.MethodPost,
		URL:     u,
		Header:  http.Header{},
		Body:    io.NopCloser(strings.NewReader("x")),
		GetBody: func() (io.ReadCloser, error) { return nil, errors.New("getbody boom") },
	}
	_, err := RequestCurl(req)
	if err == nil || !strings.Contains(err.Error(), "read body for curl") {
		t.Fatalf("err = %v, want a 'read body for curl' error", err)
	}
}

// TestRequestCurl_FallbackReadError covers the io.ReadAll error branch of the
// GetBody-nil fallback, using a body that fails on Read (errBody, defined in
// observe_test.go).
func TestRequestCurl_FallbackReadError(t *testing.T) {
	u, _ := url.Parse("http://x")
	req := &http.Request{
		Method: http.MethodPost,
		URL:    u,
		Header: http.Header{},
		Body:   errBody{},
	}
	_, err := RequestCurl(req)
	if err == nil || !strings.Contains(err.Error(), "read body for curl") {
		t.Fatalf("err = %v, want a 'read body for curl' error", err)
	}
}
