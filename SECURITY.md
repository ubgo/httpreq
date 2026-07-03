# Security Policy

## Supported versions

`ubgo/httpreq` is pre-1.0 and stdlib-only (zero third-party dependencies). Security fixes land on the latest tagged release and `main`. There is no back-porting to older tags before v1.0.0.

| Version | Supported |
|---------|-----------|
| latest `v0.x` + `main` | ✅ |
| older `v0.x` tags | ❌ |

## Reporting a vulnerability

Please report suspected vulnerabilities privately — do **not** open a public issue for a security problem.

- Preferred: open a private advisory via GitHub → the repository's **Security** tab → **Report a vulnerability** ([GitHub private vulnerability reporting](https://github.com/ubgo/httpreq/security/advisories/new)).
- Alternative: email the maintainer at **khanakia@gmail.com** with a description, affected version, and a minimal reproduction.

You can expect an acknowledgement within a few days. Once a fix is ready we will coordinate a disclosure timeline with you and credit you in the release notes unless you prefer to remain anonymous.

## Scope

Because this package is a thin convenience layer over `net/http`, most transport-level behaviour (TLS, redirects, connection pooling) is delegated to the Go standard library — report those upstream to the Go project. In-scope for this repo: issues in request building, header/body handling, the observability `Trace` (e.g. accidental leakage of sensitive data), and error surfacing.
