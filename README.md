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

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
