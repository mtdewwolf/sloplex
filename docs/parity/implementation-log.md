# Sloplex implementation progress

## 2026-10-11 foundation wave

The consolidated review and presentation were completed before delegation,
as requested. Work begins from `5808f4e5dcfc324714dd5cda1b7064610543b56d`.

### Coordinator

- Created the attributed report, 12-slide self-contained HTML presentation,
  35-group comparison data and compatibility coverage ledger.
- Reproduced Codex F24 / G27 with query and header requests containing a
  maximum integer page size: the handler panicked with negative slice bounds.
- Clamped the requested size to remaining items before addition, preserving
  existing normal pagination and invalid-input defaults.
- Specified the numeric behavior as original Sloplex defensive design.
- `go test ./internal/server -run 'TestPaginationLargeSize|FuzzPageBounds'`:
  passed after the fix; failed before it.
- `go test ./internal/server -run '^$' -fuzz '^FuzzPageBounds$' -fuzztime=3s -parallel=2`:
  passed, 50,091 fuzz executions.

### GPT 6.1 High

Implemented and integrated: NFO-independent show/season identity and fail-closed
scan behavior for nested errors and unexpectedly empty populated roots.
Current, legacy and reopened-store fixtures preserve hierarchy and collection
references. Probe failures retain known technical information and retry.
Full unavailable/trash lifecycle and configured-library renaming remain
separate work; conservative scan protection must not be labeled full parity.

### Opus 5.5 High

Implemented and integrated: explicit invalid-credential rejection on trusted
networks, browser-origin handling for tokenless network trust, and HLS plus
progressive revocation when account, token or library authority changes.
The coordinator tightened the Origin fallback to compare scheme and host
and reject malformed origin components. Old browsers without origin/fetch
metadata and DNS rebinding remain limitations of tokenless network trust.
In-flight direct-file responses are not terminated by revocation.

### Presentation validation

- HTML structure: 12 slides, 35 comparison rows and unique element IDs.
- Embedded JavaScript: `node --check` passed.
- T3 `html_render`: published successfully.
- T3 `html_preview`: blocked by missing system browser libraries (ATK,
  X11, GBM, ALSA and related dependencies). Visual rendering has not been
  validated. This does not block the implementation work.

### Integration gate

Completed after integration and coordinator origin correction:

- `gofmt -l cmd internal`: clean.
- `go vet ./...`: passed.
- `CGO_ENABLED=1 go test -race ./...`: passed.
- `git diff --check`: passed.

FFmpeg is absent on this host, so existing real-media tests can skip. New
revocation tests use a fake process fixture and execute without FFmpeg.
No official-client, live-PMS or hardware interoperability claim is made.

## Second wave implementation

### GPT 6.1 High

Added optional `libraries[].id` and persisted section `configId`, separate
from the API's numeric section ID and UUID. Exact identity preserves the
catalog and references through display-title/path edits and reopen.
Legacy adoption requires adding the ID before changing the title/type.
Ambiguous replacements, duplicate IDs and type changes fail before mutation.
Failed section persistence restores the prior in-memory state/indexes.

Focused config/library/command tests, race tests and vet passed. Regressions
cover preserved collection, account access, playstate and playlist references,
legacy/current stores, reordering, failure rollback and intentional removals.
File moves, already lost references and runtime library CRUD remain open.

### Opus 5.5 High

Added `POST /:/timeline`, `POST` refresh, collection `/items`, section-scoped
collection deletion and subtitle `/{id}.{ext}` request forms with functional
tests and precise public-reference provenance. Legacy aliases remain.
The operation ledger is `wire-operations.json`; partial operations are
explicitly marked partial.

Coordinator correction: POST refresh rejects `force=1` and nonempty `path`
with `501` instead of silently running a normal full scan. The extension
subtitle form likewise rejects nonempty encoding and automatic adjustment
other than disabled, rather than claiming to honor them. Focused refresh
and stream-path tests passed after this change. Timeline telemetry/queue
fields remain explicitly unused; no sessions dashboard or queue support
is claimed.

Refresh cancellation, forced/path-limited scans, stored subtitle offsets,
several collection mutations and other public API differences remain open.
No live PMS or official-client comparison was performed.

### Final integration

Final combined checks after coordinator corrections all passed:

- `go vet ./...`
- `CGO_ENABLED=1 go test -race -count=1 ./...`
- `gofmt -l cmd internal` (no output)
- `git diff --check`
- JSON parsing of both coverage and wire-operation ledgers

Real-media subtitle conversion remains unverified here because FFmpeg is
absent. Browser end-to-end verification remains blocked by missing host
browser libraries. No external-client or live-PMS parity claim is made.

No changes committed. The next large milestones remain typed preferences
with actual consumers, retained unavailable/trash records, durable storage
and recovery, queues/client contracts and additional media families.


## Continuous worktree round

The user requested continued management until an actual provider usage
limit. No limit has been reported. Validated foundation work was committed
as `960f909`; work now integrates isolated worktree commits.

- Storage: `79b5361` integrated as `b630809`; synchronized file replacement,
  Unix parent-directory sync and typed post-rename durability uncertainty.
- Preferences: `dc34cfe` integrated as `1eabd23`; six real preferences,
  admin-only typed XML/JSON APIs, persistence and runtime/restart consumers.
- Coordinator: `ef95076`; reproduced and fixed partial application of a
  malformed preferences query (bad percent escape or semicolon separator).
- Initial integration: `GOMAXPROCS=2 go test -race -p 2 ./...` and
  `GOMAXPROCS=2 go vet -p 2 ./...` passed. Preferences regression tests also
  passed after the coordinator correction.

Active work: retained unavailable/trash lifecycle; consistent consumer
publication after committed durability errors; local video play queues.
Broader preference inventory, backup/restore, media families and target-client
coverage remain open. External-tool limitations from earlier rounds remain.


## Stopped at requested usage limit

The Opus play-queue task failed with `Claude API rate limit reached. Try
again later.` No play-queue implementation was committed. Further task
assignment stops under the user's instruction.

Storage-consumer fixes are completed and tested in commit `01a85ee` on
`parity/storage-consumers`, but are not integrated. Trash work is checkpointed as `176e9ca` on `parity/trash`; targeted race
tests and vet passed (32 external media tests skipped). It remains
unmerged and requires the committed-error publication adjustment before
integration. The integrated branch remains at
the previously validated preferences/durability stage, plus coordination
records. Resume instructions and remaining integration checks are in
[coordination.md](coordination.md).


## Resumed: two GPT 6.1 High agents

User authorized resumption using only two GPT 6.1 agents. Integrated the
preserved storage-consumer commit as `69c4268` and trash as `1db1b15`.
The two active assignments are their staged-publication integration fix
and local video play queues. Combined validation is pending; the prior
Claude usage-limit stop is historical.


## GPT limit and recovered integration fix

After the user requested more agents, a third GPT 6.1 High agent was added
for account language preferences. All three agents reported a GPT usage
limit, retry at 11:33 AM. Queue and language-preference worktrees had no
implementation changes. The user requested continuation.

The coordinator reviewed and recovered the storage agent's saved changes,
integrated as `ba961e5`. Scan/trash stages now preserve the injected writer
and publish installed state after wrapped committed errors, while returning
the original uncertainty. Precommit errors retain the old catalog. New
regressions cover scan and purge, disk/reopen agreement, hierarchy and
stream indexes, collections and playback availability; targeted race tests
passed. Repository vet passed. The combined race run exposed the shutdown
issue below, then passed after its fix.


### Transcoder pipe shutdown found during combined validation

The initial full suite hung in `TestProgressiveRevocation`: a stack dump
showed `Cmd.Wait` waiting for the stderr-copy goroutine after cancellation.
The coordinator stopped the stuck test processes and bounded transcoder
I/O cleanup with a one-second `WaitDelay` for progressive and HLS commands.
This does not claim termination of all descendant processes.

`TestProgressiveInheritedStderr` deterministically verifies a wrapper that
exits while a descendant retains stderr produces the bounded-wait error.
That targeted race test passed, as did progressive revocation and the full
`GOMAXPROCS=2 go test -race -p 2 ./... -timeout=90s` suite. Existing limits
remain: absent FFmpeg/ffprobe and no live PMS/official-client validation.

## 2026-10-12: REA-backed video queue increment

The owner authorized static REA analysis of the supplied Plex Linux RPM and
replaced the earlier clean-room source restriction. CONTRIBUTING, the spec
index and README now describe source provenance and original implementation.
Standalone local account and no-plex.tv behavior remain in effect.

Added nine local video play-queue operations: creation/read/addition,
clear/remove/move, reset and shuffle/unshuffle. Sources resolve local metadata
or the caller's playlist; repeated metadata receives distinct entry IDs.
Mutations are synchronized and versioned, while response projection rechecks
library access and availability. Queue windows retain a full-queue selection
offset. Memory and idle limits are explicit in `docs/spec/playqueues.md`.

REA 6.4.0 recovered three shipped Plex Web 4.160.0 factories from PMS
1.43.4.10903; each extracted source span matched the REA-recorded SHA-256.
The focused application graph is complete within that subset. Dynamic and
external semantic dependencies remain unknown. Full-server Hopper and Ghidra
attempts timed out (120000/330000 ms); they produced no native behavior proof.
Raw vendor code/output stays outside versioned changes. The sanitized evidence
index is `docs/evidence/plex-1.43.4-playqueues.json`.

Validation on Go 1.27.2: targeted HTTP lifecycle, source, authorization and
concurrent-add race tests passed; full server race suite passed; repository
`GOMAXPROCS=2 go vet -p 2 ./...` and
`GOMAXPROCS=2 go test -race -p 2 ./... -timeout=90s` passed. The initial integrated
run exposed an XML reflection panic from a nil embedded queue pointer. Queue
attributes now serialize explicitly, and the full suite passes after that fix.
Additional account/server capacity and idle-expiration regression checks pass.
FFmpeg/ffprobe remain absent; external media tests can skip. No Plex server
runtime, official-client playback or browser playback was exercised.

Remaining queue work: timeline-driven selection, Up Next regions, trailers,
continuous generation, device handoff/events and player integration. G09 remains
partial alongside the still-open session/hub/event families. All changes are
local and uncommitted; the pre-existing web/branding edits and supplied RPM are
preserved.

### Timeline selection and first controlled PMS runtime

REA recovered and hash-verified shipped Plex Web factory 63224. Its timeline
request contains a playQueueItemID alongside the metadata key and position,
without a queue ID. Sloplex now resolves that occurrence within the caller's
active queues, optionally narrowed by playQueueID, validates its metadata,
saves playstate and then publishes selection. Repeat reports on the same
occurrence do not advance the queue version; rejected or failed-save requests
leave selection unchanged. Targeted race tests cover repeated metadata,
version stability, invalid states/IDs, metadata mismatches, cross-account
access and save failure. Repository vet and the full race suite pass.

REA process capture also ran PMS 1.43.4.10903 in a private network namespace
with only loopback. Root, empty library and preferences endpoints returned
200; 151 preference IDs were observed. This is controlled server runtime
evidence, not an official-client playback test. A synthetic-video queue probe
is in progress. The shipped-client/timeline Evidence index is
`docs/evidence/plex-1.43.4-timeline.json`.

### Controlled queue windows and Up Next regions

Resumed from the interrupted runtime investigation. Two complete REA captures
confirmed positive windows (radius N), zero windows (empty metadata), ordinary
additions into a manual Up Next region, next insertion at its front, and marker
retirement when playback moves past that region. Captures used isolated
loopback networking, synthetic videos and a copied initialized local profile.
The first retry failed during capture finalization; its partial startup output
is not treated as queue evidence.

Implemented playQueueLastAddedItemID in JSON/XML, manual-region ordering,
zero windows, both-disabled window rejection, timeline marker retirement and
consistent marker adjustment after deletion. Hidden markers are omitted. PMS
can return stale version/count/marker fields immediately after deletion;
Sloplex continues returning its committed state. Move/shuffle/reset marker
behavior remains an original bounded design, not proven full parity.

Targeted queue/timeline race regressions, repository vet, diff check and the
uncached full race suite pass. Evidence records: plex-1.43.4-queue-windows.json
and plex-1.43.4-queue-regions.json. No official-client or stream playback was
validated. Existing web/branding changes remain preserved and uncommitted.

### Provider discovery and home hubs

REA recovered five additional hash-verified shipped client model factories and
captured provider discovery on populated synthetic libraries. Added authenticated
/media/providers, original local video feature advertising, and access-filtered
section directories. A further native capture established home/section hub
shapes, working detail keys and per-hub count behavior.

Added four home hubs and two section hubs, detail routes, continue-watching
provider discovery and metadata-source hub keys usable for local queues. Resume
selection, TV recent episode projection and recommendation bounds are documented
as Sloplex design; the movie-only native profile had empty resume/On Deck hubs.
Hidden and unavailable media are filtered before counts and keys.

Provider/home-hub/search regressions pass under race, including advertised queue
route consumption, JSON/XML decoding, paging, account state and access changes.
Full provider integration race suite passed before the hub increment; final
hub integration checks follow. Evidence: plex-1.43.4-providers.json and
plex-1.43.4-hubs.json. No official-client or stream playback claim.

Final combined provider/home-hub gate: uncached full repository race suite,
repository vet, gofmt and diff check pass. Unavailable-media projection
regression passed alongside account visibility and state isolation. No provider
usage limit was reported. Work remains local and uncommitted.

### Playback sessions

Two REA process captures of PMS 1.43.4 established how timeline reports
populate `/status/sessions`: identity follows `X-Plex-Session-Identifier`,
falling back to the client identifier, and ignores query-string session
parameters. A report under an existing identity replaces its item and player,
and `stopped` removes the session without needing `time`. The identity
capture's terminal output truncated; the probe's sanitized observation file
carries the complete sequence.

Added admin-only `/status/sessions` and timeline session tracking with
account scoping, bounded in-memory lifetime and limits, save-before-publish
ordering and per-listing access checks. Fixed a regression where the new
path rejected unknown `state` values on the legacy `GET /:/timeline`, which
must ignore them. A regression replays the observed PMS identity sequence.
Evidence: plex-1.43.4-sessions.json. Transcode session details, expiry
timing parity and official-client playback remain unverified.
