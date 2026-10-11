# Scan application safety

## Provenance

All behavior on this page is **(sloplex)**: our own conservative design for
protecting the local catalog when filesystem observations are incomplete.
It is not an observed Plex wire contract. Retained unavailable records and
explicit trash purging are specified separately in [trash](trash.md). File walking and cancellation
use the public Go standard-library contracts for
[`io/fs.WalkDirFunc`](https://pkg.go.dev/io/fs#WalkDirFunc),
[`path/filepath.WalkDir`](https://pkg.go.dev/path/filepath#WalkDir),
[`io/fs.DirEntry.Info`](https://pkg.go.dev/io/fs#DirEntry), and
[`context.Context`](https://pkg.go.dev/context#Context).

## Authoritative scan boundary

- The filesystem scanner collects every configured root before applying
  any section changes. A missing/non-directory/unreadable root, any nested
  traversal/read-directory error, or an eligible video's stat error MUST
  abort the whole section scan. An intentionally excluded directory or file
  is still excluded; exclusion is not a filesystem failure.
- Cancellation detected during traversal, probing or immediately before
  application MUST abort. A cancelled scan is not a successful empty scan.
- A completely traversed empty root is authoritative for unavailable marking,
  including an empty mountpoint. It never deletes records; see [trash](trash.md).
- These failures leave item IDs, hierarchy, media metadata, images,
  collections, section scan time and the persisted catalog unchanged. The
  synchronous scanner returns an error; a background scan logs the failure
  and can be retried by later requests/watch events/intervals.
- A fully traversed root is authoritative for absent files: they are retained
  as unavailable. `Store.ApplyScan` requires an authoritative file list; callers
  must not supply incomplete observations. Purge requires explicit empty trash.

## Probe failure

- A metadata probe error alone is not evidence that a media file vanished.
  Unless the context was cancelled, the scanner stats the file again. A
  missing/non-regular/changed file or a failed stat aborts the whole section;
  the scanner cannot safely apply that raced observation.
- When the same regular file still exists with the observed size/time, the
  scan keeps it. Previously known technical information and embedded
  streams are retained when available; a new file gets only its
  extension-derived container. Failed probe output is not treated as
  authoritative metadata. The failure is logged and its probe-version
  marker is reset so a subsequent scan retries the probe.
- Explicitly disabled probing still uses the extension-only prober and is
  not an error. Local NFO, subtitle and artwork handling retains its existing
  optional-file semantics; this increment does not make their separate
  reads transactional with the media walk.

## Limits and deliberate deletion

Complete scans do not purge absent media. Explicit empty trash purges catalog
records as specified in [trash](trash.md). Removing a configured library still
removes its catalog under the separate configuration identity rules. Removing
one configured root retains its absent media until explicit trash purging.

A partially populated root can hide a missing mounted subtree without an OS
error; its media will be retained as unavailable. Scanning cannot provide an
atomic filesystem snapshot against changes after its final checks. Mount
identity, stable file-move IDs and transactions across artwork/playstate/
playlist stores remain outside this safety contract.
