# Contributing to Sloplex

## Clean-room rules (mandatory)

Sloplex is a clean-room implementation. The protocol specification in
[`docs/spec/`](docs/spec/README.md) is the only source of truth for
implementation code.

1. **Never** use decompiled, disassembled, or otherwise reverse-engineered
   code or binaries of any proprietary media server. Do not paste such
   material into issues, pull requests, or commits, either.
2. **Never** use leaked or proprietary source code.
3. New wire behaviour goes into `docs/spec/` first, or in the same pull
   request. Every fact needs a **Provenance** entry that names an allowed
   source:
   - public documentation;
   - black-box observation of traffic from software you lawfully run;
   - our own design.
4. Implementation code in `cmd/` and `internal/` is written from the spec.
   Review checks that code and spec agree.
5. Sloplex does not contact plex.tv or any other hosted service. Pull
   requests that add such calls will not be accepted.

If you are unsure whether a source is allowed, open an issue and ask
**before** using it.

## Workflow

- Go 1.24 or later. Use only the standard library unless a dependency is
  clearly justified.
- Run `gofmt`, `go vet ./...`, and `go test -race ./...` before you push.
  CI runs the same checks.
- Keep pull requests focused. A pull request that changes wire behaviour
  updates the spec, the code, and the tests together.
