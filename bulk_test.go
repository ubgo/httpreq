package httpreq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestWithHeaders merges a header set, preserves an already-set header, and
// keeps multi-value headers.
func TestWithHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithHeader("X-Existing", "keep"),
		WithHeaders(http.Header{
			"X-A":    {"1"},
			"Accept": {"application/json", "text/plain"},
		}),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got.Get("X-Existing") != "keep" {
		t.Errorf("existing header lost: %q", got.Get("X-Existing"))
	}
	if got.Get("X-A") != "1" {
		t.Errorf("X-A = %q, want 1", got.Get("X-A"))
	}
	if len(got.Values("Accept")) != 2 {
		t.Errorf("Accept values = %v, want 2", got.Values("Accept"))
	}
}

// TestWithHeaders_NilNoop covers the nil/empty no-op path.
func TestWithHeaders_NilNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := Do(context.Background(), srv.URL, WithHeaders(nil)); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

// TestWithQuery merges query params, preserves an already-set param, and keeps
// multi-value params.
func TestWithQuery(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithQueryParam("existing", "keep"),
		WithQuery(url.Values{
			"page": {"2"},
			"tag":  {"go", "http"},
		}),
	)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got.Get("existing") != "keep" {
		t.Errorf("existing param lost: %q", got.Get("existing"))
	}
	if got.Get("page") != "2" {
		t.Errorf("page = %q, want 2", got.Get("page"))
	}
	if len(got["tag"]) != 2 {
		t.Errorf("tag values = %v, want 2", got["tag"])
	}
}

// TestWithQuery_NilNoop covers the nil/empty no-op path.
func TestWithQuery_NilNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	if _, err := Do(context.Background(), srv.URL, WithQuery(nil)); err != nil {
		t.Fatalf("Do: %v", err)
	}
}
