# Contributing to ubgo/httpreq

Thanks for your interest in `ubgo/httpreq`. This repository is licensed under the **Apache License 2.0**. Pull requests are welcome.

## Workflow

1. Open an issue first for anything beyond a tiny fix.
2. Fork + branch named after the issue: `fix/123-redirect-loop`, `feat/456-form-body`.
3. Run local checks: `task ci`.
4. Use Conventional Commits for the PR title.

## Scope

This package is deliberately small. PRs in scope:

- Bug fixes in the `Do` flow.
- New options that compose cleanly with the existing surface.
- Better error types for distinguishing transport / decode / status.

PRs *out of scope*:

- Retry, circuit-breaking, rate-limiting (build a transport instead).
- Protocol-specific helpers (GraphQL, gRPC). Those go in their own packages.
- Auto-discovery of client config from environment variables.

## Code conventions

- **Zero third-party deps.** `go.mod` must stay stdlib-only.
- **Race detector mandatory.** Every test must pass under `-race`.
- **Coverage target.** ≥ 90% line coverage.
- **Public API stability.** Once the module reaches v1.0.0, breaking changes require a major version bump.
- **No comments explaining what the code does.** Reserve comments for non-obvious invariants.

## Testing locally

```sh
task test
task test:race
task test:coverage
task lint
task ci
```

## License of contributions

By submitting a pull request, you agree that your contribution is provided under the same Apache License 2.0 as the rest of the repository.
