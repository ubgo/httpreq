package httpreq_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
