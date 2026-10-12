# Local video play queues

## Provenance

The method/path names, queue identifiers, version field, entry IDs, selected
item fields, and GET window parameter meanings follow the official PMS API
1.2.3 at <https://developer.plex.tv/pms/>, retrieved 2026-10-12 (PMS floor 1.43.4).
The nine operations below are the locally supported subset.

REA 6.4.0 also inspected the shipped Plex Web 4.160.0 queue model factories
from the user-supplied PMS 1.43.4.10903 Linux RPM. Artifact hashes, factory
locations and Evidence IDs are in
[the research record](../evidence/plex-1.43.4-playqueues.json). Those static
observations establish client field reads and URL construction, not runtime
server behavior. The implementation is original Go code.

Account ownership, expiration, capacity, missing-media handling, natural-order
edits and error choices below are **(sloplex)**. Controlled PMS HTTP queue probes are recorded below. Official-client playback
has not been completed.

## Operations

All operations require existing local token authentication. Responses use the
normal MediaContainer XML/JSON negotiation.

- `POST /playQueues` creates a queue.
- `GET /playQueues/{id}` reads it.
- `PUT /playQueues/{id}` adds entries.
- `DELETE /playQueues/{id}/items` clears it.
- `DELETE /playQueues/{id}/items/{entry}` removes one occurrence.
- `PUT /playQueues/{id}/items/{entry}/move` moves an occurrence.
- `PUT /playQueues/{id}/reset` selects the first entry.
- `PUT /playQueues/{id}/shuffle` randomizes the order.
- `PUT /playQueues/{id}/unshuffle` restores natural order.

Creation requires `type=video` and exactly one of `uri` or `playlistID`.
Addition also requires exactly one source. A source URI can be a local
`/library/metadata/<ids>` path, a
`server://<machineIdentifier>/com.plexapp.plugins.library/library/metadata/<ids>`
URI, or a `library://<sectionUUID>/item/<encoded-metadata-path>` URI. The
server and section identities must belong to this Sloplex instance. Metadata
IDs may be comma-separated. URLs are decoded once at each enclosing URL layer.
Remote URLs, userinfo, query-bearing content URIs and fragments are rejected;
Sloplex never fetches a queue source from another server.

Movies and episodes are added directly. Shows and seasons expand to episodes
using the playlist hierarchy order (specials last when expanding a show).
Playlist sources resolve only the caller's playlists and preserve duplicate
occurrences. Missing, inaccessible or unavailable direct metadata sources
return 404. Inaccessible, removed and unavailable playlist entries or expanded
episodes are omitted. An empty playable source returns 400.

`key=/library/metadata/<id>` on creation selects the first occurrence of that
metadata item; it must be a member of the expanded queue. Otherwise the first
entry is selected. `shuffle=1` on creation shuffles while retaining selection.
Additions form a manual Up Next region after the selected entry, ahead of the
original remaining entries. Ordinary additions append to that region; `next=1`
inserts immediately after selection. The region ends at
`playQueueLastAddedItemID`: ordinary additions update this marker, and `next=1`
preserves an existing marker or establishes one for a new region. When playback
advances past the marker, the marker is retired without a version change.
Clearing retires it too. Removing its occurrence moves the boundary to the
preceding occurrence when that occurrence remains after selection. Empty queues
select the first new occurrence. Ordering, initial marker creation and
retirement on playback are observed in [region evidence](../evidence/plex-1.43.4-queue-regions.json).
The immediate consistent delete response and natural-order edits are **(sloplex)**.

Move's `after` is another **queue entry ID**, not a metadata ratingKey. Omitted
or zero `after` means the beginning. Moving after oneself or after an unknown
entry returns 400. Removing an unknown occurrence returns 404. Removing the
selected occurrence selects its successor, or the preceding last occurrence
if there is no successor. Clearing removes other occurrences and retains the
selected occurrence; deleting that occurrence separately can empty the queue. Moving, shuffling
and unshuffling preserve selected entry identity. Add/remove/move edits also
update natural order; unshuffle restores that order.

Each occurrence has a distinct `playQueueItemID`, even for repeated metadata.
Each response includes `playQueueID`, `playQueueVersion`, `playQueueTotalCount`,
`playQueueSelectedItemID`, `playQueueSelectedMetadataItemID`,
`playQueueSelectedItemOffset` and `playQueueShuffled`. The selected offset is
zero-based within the full currently visible queue. A URI-created queue also
returns `playQueueSourceURI`. Shuffled is a JSON boolean and XML 1/0. Zero-valued
counts, offsets and selection fields are included in both encodings. Ordinary
non-queue containers do not acquire queue fields.

New queues start at version 1; each successful mutation advances the version
once. GET leaves it unchanged. Failed mutations leave the queue unchanged.
Queues are protected against concurrent lost updates and duplicate entry IDs.

## Windows and visibility

GET's optional `center` identifies the queue entry at the window center and
does not change selection. `window=N` returns up to N entries on each side
plus the center for positive N. `window=0` returns no entries while retaining
queue totals and selection. Sizes 0 through 4 and boundary centers were checked
in [window evidence](../evidence/plex-1.43.4-queue-windows.json) and
[region evidence](../evidence/plex-1.43.4-queue-regions.json). With no window the whole visible queue is returned.
`includeBefore=0` excludes the center and earlier entries; `includeAfter=0`
excludes the center and later entries. Both default to 1; disabling both returns 400. A nonexistent or
inaccessible center returns 404. Window must be a nonnegative integer.
Existing X-Plex-Container-Start/Size paging applies within that window, with
`offset` relative to the full visible queue and `totalSize` equal to its count.

Queues belong to an account, not to every administrator or every authenticated
client. Another account receives 404 for reads and mutations, including an
admin. Device ownership transfer (`own=1`) is not supported. Every response
checks current library access and catalog availability; hidden metadata,
selected IDs, hidden manual-region markers and hidden item counts are not returned. If selection is hidden,
the response selects the first visible entry without deleting retained entries.
This is a response projection; it does not change stored selection or version.
Library access restoration makes retained entries visible again.

## Limits and unsupported behavior

Queues are held in memory and disappear on restart. They expire after one hour
without access. Limits are 10,000 entries per queue, 32 queues per account and
256 across the server; creation evicts the oldest-accessed queue at capacity.
An over-capacity item mutation returns 400 without changing the queue.

Malformed query encoding, duplicate parameters and invalid boolean values
return 400. Recognized queue parameters used on another operation are rejected
before mutation. Audio/photo queues, repeat wrapping, continuous generation,
On Deck starting points, trailers and ownership transfer are unsupported;
requesting those features returns 400. Explicit disabled forms such as
`repeat=0` are accepted on creation. Recursive is only relevant to photo
queues; video hierarchy expansion follows the rule above.

Device handoff, session
coordination, queue events and the web app's player integration remain open.
Presence of these routes does not establish complete Plex client compatibility.

## Playback timelines

Shipped Plex Web factory 63224 constructs playback timelines with an occurrence
ID (`playQueueItemID`) without a queue ID. REA's focused syntax graph and source
hash are recorded in [timeline evidence](../evidence/plex-1.43.4-timeline.json).

POST, GET and PUT timelines resolve that ID only in the caller's active queues.
An optional `playQueueID` narrows the lookup. Supplying a queue ID without an
entry ID, malformed IDs, duplicate query parameters or invalid states returns
400. A missing, expired or foreign queue/entry returns 404. The reported
metadata key/ratingKey must match the occurrence; a mismatch returns 400 before
watch history is saved. Unavailable or inaccessible metadata returns 404.

Selection is published after the existing playback-position operation reports
success. If it reports an error, selection and queue version are unchanged.
Timeline selection changes and repeated progress reports do not advance the
queue version, matching the controlled PMS 1.43.4 observation. Valid timelines without time still select the
occurrence. A retained unavailable item cannot become the selected playable
entry. Timelines without queue parameters retain their existing contract.
Queue owner checks and failure atomicity are **(sloplex)**. Occurrence-only
timeline selection and version stability were also observed in the controlled
server probe; see [runtime evidence](../evidence/plex-1.43.4-queue-runtime.json).
