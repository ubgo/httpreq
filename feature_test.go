package httpreq

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestWithBasicAuth sends the base64-encoded credentials and, as the last auth
// option, overrides a bearer token set earlier.
func TestWithBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithBearerToken("should-be-overridden"),
		WithBasicAuth("alice", "s3cret"),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestWithUserAgent covers all three UA paths: explicit value, default when
// unset, and suppression via the empty string.
func TestWithUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer srv.Close()

	// Explicit.
	if _, err := Do(context.Background(), srv.URL, WithUserAgent("myapp/9")); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotUA != "myapp/9" {
		t.Errorf("explicit UA = %q, want myapp/9", gotUA)
	}

	// Default when unset.
	if _, err := Do(context.Background(), srv.URL); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotUA != DefaultUserAgent {
		t.Errorf("default UA = %q, want %q", gotUA, DefaultUserAgent)
	}

	// Suppressed with empty string — net/http omits the header entirely.
	if _, err := Do(context.Background(), srv.URL, WithUserAgent("")); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotUA != "" {
		t.Errorf("suppressed UA = %q, want empty", gotUA)
	}
}

// TestWithFormBody sends url-encoded values with the right Content-Type.
func TestWithFormBody(t *testing.T) {
	var gotType, gotGrant, gotScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotGrant = r.PostForm.Get("grant_type")
		gotScope = r.PostForm.Get("scope")
	}))
	defer srv.Close()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "read write")
	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPost),
		WithFormBody(form),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotGrant != "client_credentials" || gotScope != "read write" {
		t.Errorf("form = grant:%q scope:%q", gotGrant, gotScope)
	}
}

// TestWithFormBody_NilClears covers the nil branch (clears any prior body).
func TestWithFormBody_NilClears(t *testing.T) {
	r := &request{headers: http.Header{}}
	if err := WithRawBody([]byte("stale"))(r); err != nil {
		t.Fatal(err)
	}
	if err := WithFormBody(nil)(r); err != nil {
		t.Fatal(err)
	}
	if r.body != nil {
		t.Error("WithFormBody(nil) did not clear the body")
	}
}

// TestWithResponseBytes captures a non-JSON body verbatim, on both success and
// error status.
func TestWithResponseBytes(t *testing.T) {
	const html = "<html><body>hi</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fail") == "1" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	// Success.
	var raw []byte
	if _, err := Do(context.Background(), srv.URL, WithResponseBytes(&raw)); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != html {
		t.Errorf("raw = %q, want %q", raw, html)
	}

	// Error status: bytes still captured alongside the HTTPError.
	var rawErr []byte
	_, err := Do(context.Background(), srv.URL+"?fail=1", WithResponseBytes(&rawErr))
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if string(rawErr) != html {
		t.Errorf("error-path raw = %q, want %q", rawErr, html)
	}
}

// TestWithResponseBytes_Empty sets a non-nil empty slice on an empty response.
func TestWithResponseBytes_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var raw []byte
	if _, err := Do(context.Background(), srv.URL, WithResponseBytes(&raw)); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if raw == nil {
		t.Error("raw is nil, want non-nil empty slice")
	}
	if len(raw) != 0 {
		t.Errorf("len(raw) = %d, want 0", len(raw))
	}
}

// TestWithErrorInto decodes a structured error body on non-2xx, and leaves the
// target untouched when the body is not JSON.
func TestWithErrorInto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		if r.URL.Query().Get("plain") == "1" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		_, _ = w.Write([]byte(`{"code":"invalid","message":"bad field"}`))
	}))
	defer srv.Close()

	type apiErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	// JSON error body decodes.
	var ae apiErr
	_, err := Do(context.Background(), srv.URL, WithErrorInto(&ae))
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if ae.Code != "invalid" || ae.Message != "bad field" {
		t.Errorf("decoded error = %+v", ae)
	}

	// Non-JSON error body leaves the target zero, HTTPError still returned.
	var ae2 apiErr
	_, err = Do(context.Background(), srv.URL+"?plain=1", WithErrorInto(&ae2))
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if ae2.Code != "" || ae2.Message != "" {
		t.Errorf("target should be untouched for non-JSON body, got %+v", ae2)
	}
}
