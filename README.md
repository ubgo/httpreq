# httpreq

[![Go Reference](https://pkg.go.dev/badge/github.com/ubgo/httpreq.svg)](https://pkg.go.dev/github.com/ubgo/httpreq)
[![Go Report Card](https://goreportcard.com/badge/github.com/ubgo/httpreq)](https://goreportcard.com/report/github.com/ubgo/httpreq)
[![Tests](https://github.com/ubgo/httpreq/actions/workflows/test.yml/badge.svg)](https://github.com/ubgo/httpreq/actions/workflows/test.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-00ADD8.svg)](go.mod)
![Zero dependencies](https://img.shields.io/badge/dependencies-0-brightgreen.svg)

> JSON-over-HTTP convenience layer on top of `net/http`. Stdlib-only. One function, one options pattern, typed errors, and dependency-free observability. No surprises.

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

## Why httpreq?

The standard library is the right foundation, but the most common service call — marshal a body, set headers, send with a timeout, decode JSON, turn non-2xx into an error — is ~15 lines of boilerplate every time. Full-featured clients solve that by pulling in a dependency tree and a large API surface. httpreq takes the opposite bet: a single `Do` call, a handful of composable options, and **zero third-party dependencies** — so it never conflicts with your other modules and never surprises you at upgrade time.

| | httpreq | net/http (raw) | resty / req |
|---|---|---|---|
| Third-party dependencies | **0** | 0 | several |
| Lines for a JSON POST + decode | ~5 | ~15 | ~5 |
| Typed non-2xx error with raw body | ✅ `HTTPError` | ❌ manual | ⚠️ varies |
| Built-in observability (trace/slog/timing) | ✅ dependency-free | ❌ | ✅ |
| API surface to learn | tiny | n/a | large |
| Connection pooling | ✅ via `WithHTTPClient` | ✅ | ✅ |

Reach for a full client when you need retries, rate limiting, or protocol helpers out of the box. Reach for httpreq when you want the stdlib with the boilerplate removed and nothing else added.

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
| `WithCurl(func(curl string))` | Callback fired with the request as a runnable `curl` command, just before send. |

## Dump as curl

Get the exact request as a runnable `curl` command — to print, log, drop in a bug report, or replay on the command line. Pick by what you have in hand:

**1. You're using httpreq's options → `Curl` — get the string without sending.**

Build the command from the same options you'd pass to `Do`, but nothing goes on the wire:

```go
cmd, _ := httpreq.Curl(ctx, "https://api/users",
    httpreq.WithMethod(http.MethodPost),
    httpreq.WithJSONBody(payload),
)
fmt.Println(cmd)
// curl -X POST -H 'Content-Type: application/json' --data-raw '{...}' 'https://api/users'
```

**2. You're calling `Do` → `WithCurl` — log exactly what gets sent.**

The callback fires just before the request goes out, so there's no option duplication and no separate build step:

```go
_, _ = httpreq.Do(ctx, "https://api/users",
    httpreq.WithJSONBody(payload),
    httpreq.WithCurl(func(cmd string) { log.Println(cmd) }),
)
```

**3. You already hold a plain `*http.Request` → `RequestCurl` — render that.**

This is the low-level primitive the other two are built on. Use it when the request came from somewhere else — a middleware, a custom `http.RoundTripper`, another library — and you're not going through `Do` at all:

```go
req, _ := http.NewRequest(http.MethodPost, "https://api/users", body)
req.Header.Set("Authorization", "Bearer "+token)

cmd, _ := httpreq.RequestCurl(req)
```

If you only ever call `httpreq.Do`, reach for `WithCurl` and you'll never need `RequestCurl` directly.

In all three, headers are emitted in sorted order and every value is shell-quoted, so the command survives special characters and is stable across runs. The request body is read without consuming it, so a request rendered mid-`Do` still sends normally.

> **Security:** the rendered command is a *faithful, full* dump — it includes the `Authorization` header, cookies, and body. That's the point, but it means secrets land in whatever you do with the string. Redact before writing to a shared log. (This is the opposite trade-off from `Trace`, which is metadata-only by design.)

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

## FAQ

**Does httpreq have any third-party dependencies?**
No. `go.mod` is stdlib-only and stays that way — that's a hard rule, enforced in CI. Everything, including the observability layer, is built on `net/http`, `log/slog`, and `net/http/httptrace`.

**How do I add retries or a circuit breaker?**
Install them at the transport layer and pass the client with `WithHTTPClient`. Because httpreq wraps a standard `*http.Client`, any `http.RoundTripper`-based middleware (retry, tracing, rate limiting) composes without httpreq needing to know about it.

**How do I get request logging, metrics, or tracing?**
Register `WithObserver` to receive a `Trace` (method, status, byte counts, duration, typed error) once per request. Use the built-in `SlogObserver` for `log/slog` logging, feed the `Trace` into a Prometheus histogram, or add `WithConnTrace()` for DNS/TLS/TTFB timing. OpenTelemetry works via a transport — see [Observability](#observability). No dependency is added on your behalf.

**Will anything sensitive end up in my logs?**
No. The `Trace` passed to observers carries metadata only — never request/response bodies and never headers — so tokens and cookies can't leak by accident. If you need header or body content, install a custom transport where you own the redaction.

**How do I see the raw request as a curl command?**
Use `WithCurl(func(cmd string){ ... })` while calling `Do` to log exactly what's sent, or `Curl(ctx, url, opts...)` to get the string without sending. If you already hold a plain `*http.Request` from elsewhere, `RequestCurl(req)` renders it. See [Dump as curl](#dump-as-curl). The output is a full, faithful dump — including `Authorization` — so redact before logging to a shared sink.

**Can I send non-JSON bodies?**
Yes. Use `WithRawBody([]byte)` for form posts, protobuf, or any pre-encoded payload, and set `Content-Type` with `WithHeader`.

**Is the API stable?**
The module is pre-1.0. The surface is small and unlikely to change much, but breaking changes are possible before v1.0.0; after that they require a major version bump.

## License

Apache-2.0 — see [`LICENSE`](LICENSE).

---

<sub>Go HTTP client · JSON API client for Go · net/http wrapper · stdlib-only · zero-dependency · typed HTTP errors · request observability · slog HTTP logging · httptrace timing</sub>
