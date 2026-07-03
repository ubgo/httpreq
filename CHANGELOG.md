# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Observability, dependency-free. `WithObserver(func(ctx, Trace))` fires once per request attempt on every path (success, non-2xx, network error, decode error) with metadata only — no bodies or headers.
- `Trace` type carrying method, URL, request/response byte counts, status, duration, typed error, attempt number, and optional connection-phase timings.
- `WithConnTrace()` populates DNS/Connect/TLS/TTFB timings via `net/http/httptrace`.
- curl rendering: `Curl(ctx, endpoint, opts...)` returns the request as a runnable `curl` command without sending; `WithCurl(func(curl string))` hands it to a callback just before `Do` sends; `RequestCurl(*http.Request)` renders any request. Headers are sorted and shell-quoted; the body is read without being consumed.
- `WithBasicAuth(user, password)` for HTTP Basic authentication.
- `WithFormBody(url.Values)` for `application/x-www-form-urlencoded` bodies (OAuth token endpoints, classic form posts).
- `WithResponseBytes(*[]byte)` captures the raw response body for any status and any content type — the way to read non-JSON responses.
- `WithErrorInto(any)` decodes a structured error payload from a non-2xx body while still returning the `HTTPError`.
- `WithUserAgent(string)` and a default `User-Agent` of `httpreq/<version>` (sent only when the caller sets none; pass `""` to suppress). Exposed as `Version` and `DefaultUserAgent`.
- `SlogObserver(*slog.Logger, slog.Level)` adapter for structured logging (stdlib `log/slog`); failures log at `ERROR`.
- Runnable `Example` functions (pkg.go.dev snippets, compiled and verified by `go test`) for `Do`, `HTTPError`, `WithObserver`, `SlogObserver`, and `WithConnTrace`.

## [v0.1.0] - 2026-05-01

### Added

- Initial release. Stdlib-only.
- `Do(ctx, url, opts...)` JSON-over-HTTP convenience.
- Options: `WithMethod`, `WithHeader`, `WithBearerToken`, `WithQueryParam`,
  `WithJSONBody`, `WithRawBody`, `WithTimeout`, `WithHTTPClient`,
  `WithResponseInto`.
- `HTTPError` for non-2xx responses, retains raw body.

[Unreleased]: https://github.com/ubgo/httpreq/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/ubgo/httpreq/releases/tag/v0.1.0
