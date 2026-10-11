# Sloplex consolidated parity review

Reviewed baseline: `5808f4e5dcfc324714dd5cda1b7064610543b56d`  
Date: 2026-10-11  
Reviewers: GPT 6.1 High through Codex and Claude Opus 5.5 High

Sloplex implements a useful local movie/TV server, but it is not a complete Plex Media Server reimplementation. Both independent reviews identify substantial missing settings and client APIs, unsafe catalog lifecycle behavior, incomplete access revocation, and insufficient compatibility evidence. Correct existing data and authorization guarantees before adding more stateful features.

## Original evidence

- [Codex independent review](2026-10-11-gpt61-review.md): 27 findings plus individual settings and feature inventories.
- [Claude independent review](2026-10-11-opus55-review.md): 35 numbered findings, including a supplemental settings review in section 9.
- [Presentation](2026-10-11-presentation.html): self-contained, keyboard-navigable and printable slides.
- [Comparison data](2026-10-11-findings.json): grouped findings with both original reference IDs.

The tables below group overlapping findings; they are not an arithmetic total of independent defects. “Both” means both reports identify the stated issue. It does not imply agreement on severity, every subclaim, or a reproduced failure. “Claude specific finding” preserves the distinction between Codex's broader concern and Claude's narrower finding.

## Consolidated priorities and attribution

| ID | Finding | Attribution | Codex reference | Claude reference | Priority |
|---|---|---|---|---|---|
| G01 | Scope and cloud boundary | Both | F01 | §3 | Scope |
| G02 | Preferences and library management | Both | F02 | F-13 | P1 |
| G03 | Music photos and other videos | Both | F03 | F-14 | P1 |
| G04 | Library and file identity | Both | F04, F06 | F-02 | P1 |
| G05 | TV identity after NFO edits | Codex | F05 | — | P1 |
| G06 | Incomplete scans and trash | Both | F06 | F-01 | P0 |
| G07 | Multi episode files editions and parts | Both | F07 | F-15, F-32 | P1 |
| G08 | Metadata editing and providers | Both | F08 | F-17 | P1 |
| G09 | Queues sessions hubs and events | Both | F09 | F-12 | P1 |
| G10 | Concrete wire method and path mismatches | Claude | — | F-10 | P1 |
| G11 | Client capability negotiation | Both | F10 | F-10 | P1 |
| G12 | Transcoder settings and modes | Both | F11 | F-19 | P2 |
| G13 | Automatic audio and subtitle language | Both | F12 | F-20; §5.2 | P1 |
| G14 | Fine grained user restrictions | Both | F13 | F-20, F-34 | P1 |
| G15 | Trusted networks and browser origins | Codex | F14 | — | P1 |
| G16 | Invalid token becomes owner | Codex | F15 | — | P1 |
| G17 | Public and all address trusted ranges | Claude | — | F-04 | P1 |
| G18 | HLS capability revocation | Both | F16 | F-08 | P1 |
| G19 | TLS remote access and discovery | Both | F17 | F-07, F-22, F-23 | P1 |
| G20 | Analysis extras and local pre rolls | Both | F18 | F-16, F-18, F-35 | P2 |
| G21 | Live TV and DVR | Both | F19 | F-24 | P1 |
| G22 | Collections playlists and search | Both | F20 | F-10; §5.3 | P2 |
| G23 | Downloads and optimized versions | Both | F21 | F-25 | P2 |
| G24 | Durability backup and migrations | Both | F22 | F-03 | P1 |
| G25 | Scale and resource lifecycle | Both | F23 | F-28 | P2 |
| G26 | Maintenance diagnostics and activities | Both | F23; settings inventory | F-21 | P2 |
| G27 | Pagination overflow | Codex | F24 | — | P2 |
| G28 | Interoperability and hardware evidence | Both | F25 | F-11, F-30 | P1 |
| G29 | Clean room provenance | Both | F26 | F-09 | P1 |
| G30 | Outbound policy consistency | Both | F27 | F-27 | P2 |
| G31 | Proxy login throttling | Claude | — | F-05 | P2 |
| G32 | Owner token in logs | Claude | — | F-06 | P2 |
| G33 | Local artwork and subtitle conventions | Claude | — | F-31 | P2 |
| G34 | Extras directory exclusion depth | Claude specific finding | F07 broad exclusion gap | F-33 specific defect | P3 |
| G35 | Webhooks push and CI supply chain | Claude specific finding | F17/F27 adjacent only | F-26, F-29 | P3 |

## Evidence and severity reconciliation

1. **Scanning is the first data-safety priority.** Both reviewers trace skipped nested errors into a replacement catalog (`internal/library/scanner.go:151`, `internal/library/apply.go:44`). Claude additionally describes an existing but empty mountpoint. Codex rates the family P1; Claude P0. Use P0 for implementation ordering because identity and collection associations can be lost. This is catalog/association loss, not deletion of the media files themselves. Reproduce before fixing.
2. **HLS revocation is a shared finding with different severity.** Codex F16 rates it P1; Claude F-08 rates it P3 because bearer-session access is intentional. Randomness prevents guessing but does not revoke known capabilities. Treat account deletion, token revocation and library removal as P1 authorization boundaries; preserve tokenless session playback only while its recorded authority remains valid.
3. **Trusted-network findings are related but distinct.** Codex's wildcard-origin issue and invalid-token fallback are not the same as Claude's public-CIDR issue. All depend on configured trusted networks; do not describe the empty default network list as exposed. Browser protections affect exploitability, not the server's authorization rule.
4. **Protocol mismatches need version-specific tests.** Claude compares exact verbs/paths against the published API; Codex identifies wider client-orchestration and profile gaps. Keep working legacy aliases. Missing documentation is not evidence that Plex never supports a legacy form. A documented route is not a complete implementation until behavior and response shape are verified.
5. **Durability is narrower than “all writes are unsafe.”** Temporary-file rename protects against partial replacement. Missing file/directory synchronization leaves a power-loss guarantee gap; multi-store rollback, backup and restore remain separate work.
6. **Provenance gaps are not evidence of copied code.** Both identify broad, unauditable citations. Original-design features are valid but must not be reported as measured Plex compatibility.
7. **Do not turn endpoint counts into a parity percentage.** Claude inventories approximately 75 local routes versus 207 paths in its downloaded official reference. Routes, paths, verbs, response schemas and end-to-end features are different units.
8. **Avoid overclaiming client failure.** No tested official-client matrix exists. Some discovery/account flows depend on Plex services; whether a specific client accepts a direct URL/token remains unverified.

## Settings and feature coverage

The individual inventories remain in Codex's “Comprehensive public-documentation coverage inventory” and Claude sections 5.2 and 9. They cover General, Library, Network, Remote Access, Transcoder, Languages, DLNA, Scheduled Tasks, Extras, hidden preferences, agents, per-library behavior, DVR, downloads and diagnostics. No reviewed page establishes a complete version/platform-specific preference catalog. Plugin behavior, some photo rules, hidden preference wire representation, matching-hint grammar and several private algorithms remain evidence gaps.

Implementation must give each supported setting an ID, type, default, choices, scope, authorization rule, persistence behavior, actual consumer and acceptance test. An inert checkbox or accepted-but-ignored parameter is not parity. Public support documentation describes user behavior; undocumented wire details require a lawful observation or an explicitly original design.

## First implementation wave

The user authorized implementation after this report and presentation. Proceed with a bounded foundation wave, then use its passing integration checks to unlock the next workstreams.

| Owner | Workstream | Required acceptance |
|---|---|---|
| GPT 6.1 High | Library scan safety and TV hierarchy identity (G05/G06) | Reproduce nested scan failure and NFO identity churn; preserve existing IDs and associations through failure, repeated scans and reopen; document safe missing-media behavior. |
| Opus 5.5 High | Existing authentication/session boundaries (G15/G16/G18) | Explicit invalid credentials never become owner; tokenless trust has a defined origin boundary; revoked account/token/library access cannot continue through a known HLS capability. |
| Coordinator | Consolidation, provenance/coverage ledger and bounded pagination (G27) | Trace reviewer IDs to work; add an overflow regression and safe pagination; integrate both agents and run vet/race tests. |

Agents own separate files/behaviors. New wire behavior must be specified with public-source or original-design provenance before or with implementation. Start with regression fixtures; leave broader migrations, preferences and missing families visibly open rather than folding them into unrelated patches.

## Subsequent milestones

1. Stable configured library identity, unavailable/trash lifecycle and durability/backup/recovery.
2. Versioned public API conformance matrix, exact existing-route fixes and a settings registry with real consumers; library management and metadata edits/locks.
3. Queues, sessions, hubs, activities, client capability negotiation and target-client fixtures.
4. Music, photos and other-video families; multipart/editions/multi-episode organization; local artwork/subtitles; finer restrictions and track-language preferences.
5. Analysis/extras, maintenance and durable jobs; downloads/optimization; operational limits and hardware validation.
6. Local tuners/XMLTV, live playback, recording schedules and failure recovery; DLNA and network/deployment coverage.
7. Separately scoped supported external integrations if the repository policy is explicitly revised. No hosted Plex integration is being silently added by this wave.

These are dependencies and completion gates, not a claimed timeline or promise that one patch delivers full parity.

## Current scope and evidence gates

Use the existing standalone/local policy while improving locally implementable behavior. The reviewers used public Plex documentation as a reference; no proprietary code or binary reverse engineering was used. The published API reference is a provisional source, not a chosen target binary/client matrix. A complete compatibility claim needs exact PMS build, platform and client versions, subscription/account conditions, lawfully captured sanitized exchanges, media fixtures and per-feature outcomes.

The clean-room boundary remains `docs/spec/`. Public documentation and lawful black-box observations may inform it; original product decisions must be labeled. No fabricated cloud identity, entitlement or compatibility result counts as coverage.

## Baseline validation

- Coordinator: Go 1.27.2; `go vet ./...` and `go test ./...` passed.
- User: `CGO_ENABLED=1 go test -race ./...` passed for all tested packages after compiler installation.
- No official-client, live PMS differential, physical GPU or tuner certification was performed. Some media integration tests can skip without their external tools/fixtures.
- These baseline results precede implementation. Post-change validation must be recorded separately.

## Public reference entry points

- [Official PMS API reference](https://developer.plex.tv/pms/) for documented operations and versioned schema context.
- [Library settings](https://support.plex.tv/articles/200289526-library/) and [trash behavior](https://support.plex.tv/articles/200289326-emptying-library-trash/) for retention controls.
- [Network settings](https://support.plex.tv/articles/200430283-network/) for trust and network controls.
- [Advanced hidden settings](https://support.plex.tv/articles/201105343-advanced-hidden-server-settings/) for the documented hidden-setting inventory.

The independent reports attach the detailed public URLs and baseline repository line references to each substantive finding.
