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
