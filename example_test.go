package httpreq_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"

	"github.com/ubgo/httpreq"
)

// ExampleDo shows the common case: POST a JSON body and decode the JSON
// response into a struct.
func ExampleDo() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"alice"}`))
	}))
	defer srv.Close()

	var out struct {
		Name string `json:"name"`
	}
	_, err := httpreq.Do(context.Background(), srv.URL,
		httpreq.WithMethod(http.MethodPost),
		httpreq.WithJSONBody(map[string]int{"id": 1}),
		httpreq.WithResponseInto(&out),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(out.Name)
	// Output: alice
}

// ExampleHTTPError shows how to recover the status code and raw body from a
// non-2xx response via errors.As.
func ExampleHTTPError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	defer srv.Close()

	_, err := httpreq.Do(context.Background(), srv.URL)

	var herr *httpreq.HTTPError
	if errors.As(err, &herr) {
		fmt.Printf("status=%d body=%s\n", herr.StatusCode, herr.Body)
	}
	// Output: status=404 body=missing
}

// ExampleWithObserver registers an observer that receives one Trace per
// request attempt. The Trace carries metadata only — never bodies or headers.
func ExampleWithObserver() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	_, _ = httpreq.Do(context.Background(), srv.URL,
		httpreq.WithObserver(func(_ context.Context, t httpreq.Trace) {
			fmt.Printf("%s -> %d\n", t.Method, t.StatusCode)
		}),
	)
	// Output: GET -> 204
}

// ExampleSlogObserver wires the batteries-included log/slog adapter. Failures
// log at ERROR regardless of the level passed. Output is not asserted here
// because log lines carry volatile timing fields.
func ExampleSlogObserver() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	_, _ = httpreq.Do(context.Background(), "https://api.example.com/health",
		httpreq.WithObserver(httpreq.SlogObserver(logger, slog.LevelInfo)),
	)
}

// ExampleWithResponseBytes captures a non-JSON response body verbatim — useful
// for HTML, text, XML, or binary payloads that WithResponseInto can't decode.
func ExampleWithResponseBytes() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<h1>hello</h1>"))
	}))
	defer srv.Close()

	var raw []byte
	if _, err := httpreq.Do(context.Background(), srv.URL, httpreq.WithResponseBytes(&raw)); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(string(raw))
	// Output: <h1>hello</h1>
}

// ExampleWithErrorInto decodes a structured error payload from a non-2xx
// response while still returning the *HTTPError.
func ExampleWithErrorInto() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_field","message":"name is required"}`))
	}))
	defer srv.Close()

	var apiErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_, err := httpreq.Do(context.Background(), srv.URL, httpreq.WithErrorInto(&apiErr))

	var herr *httpreq.HTTPError
	if errors.As(err, &herr) {
		fmt.Printf("%d %s: %s\n", herr.StatusCode, apiErr.Code, apiErr.Message)
	}
	// Output: 400 invalid_field: name is required
}

// ExampleWithBasicAuth sends HTTP Basic credentials. It overrides any bearer
// token set earlier — the last auth option wins.
func ExampleWithBasicAuth() {
	_, _ = httpreq.Do(context.Background(), "https://api.example.com/private",
		httpreq.WithBasicAuth("alice", "s3cret"),
	)
}

// ExampleWithFormBody posts application/x-www-form-urlencoded values, the shape
// OAuth token endpoints expect.
func ExampleWithFormBody() {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "read")

	_, _ = httpreq.Do(context.Background(), "https://auth.example.com/token",
		httpreq.WithMethod(http.MethodPost),
		httpreq.WithBasicAuth("client-id", "client-secret"),
		httpreq.WithFormBody(form),
	)
}

// ExampleWithUserAgent sets a custom User-Agent. Without this option,
// httpreq.DefaultUserAgent is sent; pass "" to suppress the header entirely.
func ExampleWithUserAgent() {
	_, _ = httpreq.Do(context.Background(), "https://api.example.com/x",
		httpreq.WithUserAgent("my-service/1.4.2"),
	)
}

// ExampleCurl renders a request as a runnable curl command without sending it —
// handy for logging or reproducing a call on the command line.
func ExampleCurl() {
	cmd, err := httpreq.Curl(context.Background(), "https://api.example.com/users",
		httpreq.WithMethod(http.MethodPost),
		httpreq.WithHeader("Accept", "application/json"),
		httpreq.WithJSONBody(map[string]string{"name": "alice"}),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cmd)
	// Output: curl -X POST -H 'Accept: application/json' -H 'Content-Type: application/json' -H 'User-Agent: httpreq/0.2.0' --data-raw '{"name":"alice"}' 'https://api.example.com/users'
}

// ExampleRequestCurl renders a plain *http.Request you already hold — built
// with the standard library, not through httpreq — as a curl command.
func ExampleRequestCurl() {
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/users", nil)
	req.Header.Set("Accept", "application/json")

	cmd, err := httpreq.RequestCurl(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cmd)
	// Output: curl -H 'Accept: application/json' 'https://api.example.com/users'
}

// ExampleWithCurl logs the exact request Do sends, right before it goes out.
func ExampleWithCurl() {
	_, _ = httpreq.Do(context.Background(), "https://api.example.com/health",
		httpreq.WithCurl(func(cmd string) {
			// print it, log it, whatever you want
			_ = cmd
		}),
	)
}

// ExampleWithConnTrace adds connection-phase timing (DNS/Connect/TLS/TTFB) to
// the Trace. Connect and TLS stay zero on a reused keep-alive connection.
// Output is not asserted because timings vary per run.
func ExampleWithConnTrace() {
	_, _ = httpreq.Do(context.Background(), "https://api.example.com/health",
		httpreq.WithConnTrace(),
		httpreq.WithObserver(func(_ context.Context, t httpreq.Trace) {
			fmt.Printf("dns=%s connect=%s tls=%s ttfb=%s total=%s\n",
				t.DNS, t.Connect, t.TLS, t.TTFB, t.Duration)
		}),
	)
}
