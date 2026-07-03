# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Observability, dependency-free. `WithObserver(func(ctx, Trace))` fires once per request attempt on every path (success, non-2xx, network error, decode error) with metadata only — no bodies or headers.
- `Trace` type carrying method, URL, request/response byte counts, status, duration, typed error, attempt number, and optional connection-phase timings.
- `WithConnTrace()` populates DNS/Connect/TLS/TTFB timings via `net/http/httptrace`.
- `SlogObserver(*slog.Logger, slog.Level)` adapter for structured logging (stdlib `log/slog`); failures log at `ERROR`.

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
