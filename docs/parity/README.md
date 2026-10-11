# Sloplex compatibility coverage

[coverage.json](coverage.json) tracks the grouped findings from the
[independent review comparison](../reviews/2026-10-11-consolidated-review.md).
It is a backlog and evidence index, not a feature-completion percentage.
The original reports retain the per-setting inventory, source URLs,
baseline code locations and acceptance criteria.

## Scope

The current implementation work targets locally implementable server
behavior under the existing clean-room and standalone-service policy.
No target PMS binary build or official-client matrix has been established.
Cloud registration, Plex account behavior and entitlements remain explicitly
unverified and are not represented by local equivalents as compatible.

An implemented change can close a local regression without proving a Plex
contract. Record these separately. Keep a finding open or partial when
only some acceptance criteria are satisfied.

## Evidence for new behavior

Before or alongside implementation, describe observable behavior in
`docs/spec/` and label its provenance:

- Public documentation: exact URL, retrieval date, applicable document
  version, the particular operation or setting, and relevant platform limits.
- Lawful observation: capture identifier, server/client versions, platform,
  setup and account conditions, collection date, ownership/permission and
  redaction notes. Never store tokens, passwords or private media metadata.
- Original design: mark **(sloplex)** and state the intended contract and
  regression tests. Do not label it observed Plex behavior.

The [official PMS API reference](https://developer.plex.tv/pms/) and
[server settings overview](https://support.plex.tv/articles/201154176-overview/)
are source entry points, not blanket provenance for every existing fact.
Existing broad citations remain an open finding until reconciled individually.
Do not infer a wire format, default or unsupported legacy route from absence
in a public reference.

For every supported setting, track its ID, type, scope, default, validation,
choices, authorization, persistence/restart behavior, actual consumer and
test. Accepted-but-ignored parameters are not implementation coverage.

## Validation records

Record the exact command, environment, result and material skipped tests.
Distinguish internal regressions from live PMS differential tests, official
client flows, GPU/tuner tests and browser/deployment checks. A passed Go
suite does not certify those external integrations.

Review artifacts describe the baseline. Update this ledger and the
implementation log as changes land; preserve the independent findings as
historical evidence rather than rewriting them to describe the new code.
