<!-- Keep PRs small and in scope — see CONTRIBUTING.md. -->

## What & why

<!-- One or two sentences: what does this change and why is it needed? Link the issue: Closes #123 -->

## Type of change

- [ ] Bug fix (non-breaking)
- [ ] New option / feature (non-breaking)
- [ ] Breaking change (requires a major version bump post-v1.0.0)
- [ ] Docs / tests / tooling only

## Checklist

- [ ] `task ci` passes locally (vet + race tests)
- [ ] New behaviour is covered by tests, passing under `-race`
- [ ] `go.mod` is still stdlib-only (zero third-party deps)
- [ ] Public API changes are documented (doc comments + README)
- [ ] PR title follows Conventional Commits
