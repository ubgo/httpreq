# httpreq

> JSON-over-HTTP convenience layer on top of `net/http`. Stdlib-only.
> One function, one options pattern, no surprises.

```go
import "github.com/ubgo/httpreq"

var resp UserResponse
_, err := httpreq.Do(ctx, "https://api.example.com/users",
    httpreq.WithMethod(http.MethodPost),
    httpreq.WithJSONBody(req),
    httpreq.WithBearerToken(token),
    httpreq.WithTimeout(5 * time.Second),
    httpreq.WithResponseInto(&resp),
)
```

## What it does

- Builds the request from options.
- Adds `Authorization`, `Content-Type`, custom headers, and query params.
- Sends with a per-request timeout and your context.
- Reads the body once.
- Returns `*HTTPError` for non-2xx (with the raw body captured).
- JSON-decodes the body into the target you supplied.

## What it doesn't do

- Retries / backoff / circuit breaking — install those at the transport
  layer (`http.RoundTripper`).
- Streaming responses — for large bodies use `net/http` directly.
- GraphQL helpers — those live in separate packages.
- A global default client — pass `WithHTTPClient` if you need pooling.

## Options

| Option | Effect |
|--------|--------|
| `WithMethod(string)` | HTTP method. Default: GET. |
| `WithHeader(k, v)` | Add a header. Repeat for multi-value. |
| `WithBearerToken(string)` | `Authorization: Bearer <t>`. No-op when empty. |
| `WithQueryParam(k, v)` | Append a query string parameter. Repeat for multi-value. |
| `WithJSONBody(any)` | Marshal body as JSON, set `Content-Type`. `nil` clears. |
| `WithRawBody([]byte)` | Send bytes verbatim. Caller sets `Content-Type`. |
| `WithTimeout(time.Duration)` | Default: 30s. Set to 0 to use ctx deadline only. |
| `WithHTTPClient(*http.Client)` | Override the underlying client. |
| `WithResponseInto(any)` | JSON-decode response into v (must be a pointer). |
| `WithObserver(func(ctx, Trace))` | Callback fired once per attempt with metadata (see Observability). Repeatable. |
| `WithConnTrace()` | Fill DNS/Connect/TLS/TTFB timings on the `Trace` via `httptrace`. |

## Error types

Non-2xx responses surface `*HTTPError`:

```go
_, err := httpreq.Do(ctx, "https://api/x")
var herr *httpreq.HTTPError
if errors.As(err, &herr) {
    log.Printf("status=%d body=%s", herr.StatusCode, herr.Body)
}
```

Transport errors (DNS, connection, timeout, ctx-cancel) come back as
wrapped errors from `http.Client`. Decode errors are wrapped JSON errors.

## Observability

Register an observer to receive a `Trace` once per request attempt — on success and on every failure path (non-2xx, network error, decode error). The `Trace` carries metadata only (method, final URL, status, request/response byte counts, total duration, the typed error, attempt number). It never contains bodies or headers, so nothing sensitive leaks into your logs by accident. This is the single hook for all three observability pillars: route the `Trace` to a logger, a metrics recorder, or a span.

```go
_, err := httpreq.Do(ctx, "https://api/x",
    httpreq.WithObserver(func(ctx context.Context, t httpreq.Trace) {
        // metrics, logging, spans — your call
    }),
)
```

### Structured logging (`log/slog`)

`SlogObserver` is a batteries-included adapter — still stdlib-only. Failures log at `ERROR` regardless of the level you pass.

```go
logger := slog.Default()
_, err := httpreq.Do(ctx, "https://api/x",
    httpreq.WithObserver(httpreq.SlogObserver(logger, slog.LevelInfo)),
)
```

### Connection-phase timing (`net/http/httptrace`)

Add `WithConnTrace()` to populate DNS/Connect/TLS/TTFB on the `Trace` — the phase breakdown you actually need when debugging "why is this call slow." `Connect` and `TLS` stay zero on a reused keep-alive connection because no new dial or handshake happened.

```go
_, err := httpreq.Do(ctx, "https://api/x",
    httpreq.WithConnTrace(),
    httpreq.WithObserver(func(_ context.Context, t httpreq.Trace) {
        log.Printf("dns=%s connect=%s tls=%s ttfb=%s total=%s",
            t.DNS, t.Connect, t.TLS, t.TTFB, t.Duration)
    }),
)
```

### Metrics (Prometheus, etc.)

There is no stdlib metrics API, so the `Trace` callback *is* the metrics seam — no dependency is imported on your behalf. Record straight from the callback:

```go
httpreq.WithObserver(func(_ context.Context, t httpreq.Trace) {
    reqDuration.WithLabelValues(t.Method, strconv.Itoa(t.StatusCode)).Observe(t.Duration.Seconds())
})
```

### Distributed tracing (OpenTelemetry)

OTel is not a dependency of this package. Because your context flows through `Do`, wire tracing at the transport with `otelhttp` and spans nest correctly — no httpreq-specific glue needed:

```go
client := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
_, err := httpreq.Do(ctx, "https://api/x", httpreq.WithHTTPClient(client))
```

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
