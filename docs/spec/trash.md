# Retained unavailable media and library trash

## Provenance

The behavioral foundation is public Plex support documentation: missing files
are retained in library trash and a file restored at its expected location can
restore the item until trash is emptied. See [Emptying Library Trash](https://support.plex.tv/articles/200289326-emptying-library-trash/)
(last modified March 25, 2025) and [Unavailable Content](https://support.plex.tv/articles/201806463-why-does-plex-media-server-say-my-content-is-unavailable/),
accessed October 11, 2026. The public [PMS API](https://developer.plex.tv/pms/)
OpenAPI document, documentation version **1.2.3**, accessed October 11, 2026,
defines `PUT /library/sections/{sectionId}/emptyTrash`, an admin operation,
with HTTP 200 success, to permanently remove missing media/metadata. That
version identifies the documentation, not a tested PMS binary version.

Everything below about persistence, aggregation, extensions, errors and
ordering is **(sloplex)** original design. No proprietary code, binaries or
undocumented wire captures informed it. The cited documents do not establish
an exact availability wire representation; we do not claim a standard Plex
availability attribute or client trash-icon compatibility.

## Scan and restore

A complete scan retains absent parts with `unavailable: true` in the local
version-1 catalog (additive; older catalogs default to available). It retains
the leaf, parent season/show, metadata, artwork references, item/media/part/
stream IDs, collection membership, playlist entries and watch/stream state.
There is no automatic purge. Scanner completeness failures still leave the
whole section unchanged; see [scan safety](scan-safety.md).

An existing, traversable empty root is authoritative for marking records
unavailable, including the last file and an empty mountpoint. This never
removes catalog records. A missing/unreadable root still fails the scan.
A silently absent mounted subtree can mark its items unavailable, but cannot
purge them. This is not mount detection or an atomic filesystem snapshot.

Each media version is playable only when it has parts and all its parts are
available. A leaf is unavailable only when no version is playable; hierarchy
containers are unavailable only when none of their descendant leaves is
available. Healthy versions precede unavailable versions in stored and API
order; relative order follows the existing scan path order within the healthy group
and the retained order within the unavailable group. Version array indices can
change with availability; IDs do not. One missing version cannot hide a
healthy version. Known missing versions retain their technical information
and streams. Return at the same path reuses those IDs; the normal probe cache
and safe probe-failure rules still apply. File moves/path-based identity
changes and recovery after an explicit purge are outside this guarantee.

`sloplexUnavailable` appears on metadata, media and parts only when true:
JSON boolean `true`, XML attribute `1`. False is omitted. These are Sloplex
extensions, not verified PMS field names. Listings, searches, playlists and
collections continue to expose retained items within the account's normal
library visibility. Part downloads, subtitle delivery, new playback/decisions/
transcodes and HLS delivery reject unavailable sources with 404. HLS source
authority is rechecked at delivery and revoked by exact part, preserving healthy
versions; progressive transcode output rechecks authority on each write and ends
if it is withdrawn. An already running transcode is not proactively cancelled
by the scanner: it is stopped at its next output/delivery check or normal idle
cleanup. Generated
artwork does not probe missing sources. Existing local artwork references
remain retained; missing artwork files can still yield 404.

## Explicit empty trash

`PUT /library/sections/{id}/emptyTrash` requires an administrator; a regular
account receives 403 before section lookup, anonymous access 401 under the
normal authentication policy, and an unknown section 404 for an admin.
Success returns HTTP 200 with no body. Other methods are not aliases.

The operation removes unavailable media versions in the selected section,
then leaves with no versions and empty hierarchy ancestors. It prunes removed
items from manual/automatic collections, retaining manual collection identity
and healthy versions. It never deletes, modifies or stats media files on disk.
It purges the last successful scan's availability snapshot: restoring a file
requires scanning before emptying trash to avoid purging its retained record.
A subsequent scan of a purged path creates fresh IDs. Other sections are
unchanged. Purging twice is harmless.

Scan and empty-trash mutations are staged. A persistence failure before file
replacement leaves the published catalog, indexes and availability unchanged.
If replacement succeeds but directory durability cannot be confirmed, the
installed catalog MUST be published and its indexes rebuilt, including any
availability changes or purged IDs. The original, inspectable
`*atomicfile.CommittedError` MUST still be returned; this is an error outcome,
not a rollback. Empty trash reports HTTP 500 for this outcome even though its
catalog change is installed. A reopened store and the running store therefore
agree about the installed snapshot. The filesystem and platform limits in
[storage durability](storage-durability.md) apply; this does not establish a
transaction across stores or unconditional power-loss survival.

External playlist/watch-state stores are not rewritten by empty trash. Their
entries for removed IDs no longer resolve and are not reused for new items;
retained unavailable entries remain resolvable until purge. This preserves
history through outages but does not implement garbage collection across
separate stores. There is no settings registry/auto-empty option in this
increment, filesystem deletion endpoint, or complete official-client parity.

Scan statistics count newly unavailable leaves separately in `Unavailable`;
`Removed` is zero for absence-only scans and counts leaf removal on purge.
