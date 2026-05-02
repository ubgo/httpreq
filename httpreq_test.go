package httpreq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGet_DecodeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"alice","age":30}`))
	}))
	defer srv.Close()

	var body struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	resp, err := Do(context.Background(), srv.URL, WithResponseInto(&body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body.Name != "alice" || body.Age != 30 {
		t.Fatalf("body = %+v", body)
	}
}

func TestPost_JSONBody_AndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer abc" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Trace"); got != "tid" {
			t.Errorf("X-Trace = %q", got)
		}
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["k"] != "v" {
			t.Errorf("body = %v", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var resp struct {
		OK bool `json:"ok"`
	}
	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPost),
		WithJSONBody(map[string]string{"k": "v"}),
		WithBearerToken("abc"),
		WithHeader("X-Trace", "tid"),
		WithResponseInto(&resp),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatal("response not parsed")
	}
}

func TestQueryParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("a") != "1" || q.Get("b") != "two" {
			t.Errorf("query = %v", q)
		}
		if got := q["x"]; len(got) != 2 || got[0] != "first" || got[1] != "second" {
			t.Errorf("repeated x = %v", got)
		}
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL+"?a=1",
		WithQueryParam("b", "two"),
		WithQueryParam("x", "first"),
		WithQueryParam("x", "second"),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNon2xxReturnsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":"i_am_a_teapot"}`))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL)
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("err = %v, want HTTPError", err)
	}
	if herr.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d", herr.StatusCode)
	}
	if !strings.Contains(string(herr.Body), "teapot") {
		t.Fatalf("body lost: %s", herr.Body)
	}
	if !strings.Contains(herr.Error(), "418") {
		t.Fatalf("Error() = %q", herr.Error())
	}
}

func TestHTTPError_NoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL)
	var herr *HTTPError
	if !errors.As(err, &herr) {
		t.Fatal("expected HTTPError")
	}
	if !strings.Contains(herr.Error(), "Forbidden") {
		t.Fatalf("Error() = %q", herr.Error())
	}
}

func TestRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != "raw bytes" {
			t.Errorf("body = %q", body)
		}
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPut),
		WithRawBody([]byte("raw bytes")),
		WithHeader("Content-Type", "application/octet-stream"),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL, WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestCustomClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`ok`))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second}
	_, err := Do(context.Background(), srv.URL, WithHTTPClient(client))
	if err != nil {
		t.Fatal(err)
	}

	if err := WithHTTPClient(nil)(&request{}); err == nil {
		t.Fatal("nil client should error")
	}
}

func TestEmptyTokenSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization: %q", r.Header.Get("Authorization"))
		}
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL, WithBearerToken(""))
	if err != nil {
		t.Fatal(err)
	}
}

func TestNilJSONBodyClears(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) != 0 {
			t.Errorf("expected empty body, got %q", body)
		}
	}))
	defer srv.Close()

	_, err := Do(context.Background(), srv.URL,
		WithMethod(http.MethodPost),
		WithJSONBody(map[string]string{"k": "v"}),
		WithJSONBody(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInvalidJSONBody(t *testing.T) {
	_, err := Do(context.Background(), "http://example.com",
		WithJSONBody(make(chan int)),
	)
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestInvalidURL(t *testing.T) {
	_, err := Do(context.Background(), "://bad", WithQueryParam("k", "v"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	var v map[string]any
	_, err := Do(context.Background(), srv.URL, WithResponseInto(&v))
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := Do(ctx, srv.URL)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
