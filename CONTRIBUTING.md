# Contributing to Sloplex

## Sources and implementation

The project owner revised the clean-room source restriction on 2026-10-12
and authorized REA analysis of the supplied Plex Linux release. Public
API documentation, lawful runtime observations, static analysis of supplied
artifacts, and original design may inform compatibility research.

1. Record new wire behavior in `docs/spec/` before or alongside code changes.
   Every claim must identify its source, target version and limitations.
2. Distinguish static findings, inferred behavior and observed runtime behavior.
   Record artifact SHA-256, REA Evidence IDs and source locations for binary or
   shipped-client research. Do not call an analysis timeout a successful result.
3. Write original implementation code. Do not import leaked source or copy
   proprietary implementation code into this repository. Keep vendor binaries
   and raw analysis output outside versioned changes; retain an evidence index.
4. Preserve local regression tests and separately record live-server/client
   validation. A passing internal suite does not establish ecosystem parity.
5. Sloplex does not contact plex.tv. Existing local accounts, authentication and
   hosted-service boundaries remain in effect.

## Workflow

- Go 1.24 or later. Use only the standard library unless a dependency is
  clearly justified.
- Run `gofmt`, `go vet ./...`, and `go test -race ./...` before you push.
  CI runs the same checks.
- Keep pull requests focused. A pull request that changes wire behaviour
  updates the spec, the code, and the tests together.
