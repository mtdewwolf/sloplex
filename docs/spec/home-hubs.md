# Home and section hubs

## Provenance

REA captured the supplied PMS 1.43.4.10903 Linux server with synthetic movie
libraries in an isolated loopback-only network namespace. The capture observed
home/section hub envelopes, identifiers, detail keys, context and `hubKey`
fields, and `count=1` behavior. [Runtime evidence](../evidence/plex-1.43.4-hubs.json)
records the findings and limits. Resume hubs were empty on the native profile;
the selection rules below are original **(sloplex)** behavior. The implementation
is original Go code.

## Routes and response shape

All routes require normal local authentication and use XML/JSON negotiation.
`GET /hubs` returns four `Hub` entries: Continue Watching, On Deck, Recently
Added Movies and Recently Added TV. `GET /hubs/sections/{id}` returns Continue
Watching and Recently Added for that section; unknown or inaccessible sections
return 404. Empty hubs remain present. Music/photo and additional recommendation
hubs are omitted.

Each hub includes `hubIdentifier`, `title`, `type`, `context`, a working detail
`key`, returned item `size`, `more`, and metadata. Nonempty hubs also include
`hubKey=/library/metadata/<ids>` for the returned items. This URI can be used
as a local queue source. Section recent hubs link to the existing section
`recentlyAdded` route; their naming/selection is **(sloplex)**.

Inline hub `count` defaults to 10 and accepts integers 0 through 100. It limits
each hub separately; `more=true` means additional visible playable entries
exist. `count=0` returns empty metadata with truthful `more`. Invalid, repeated
or malformed count queries return 400. This bound is **(sloplex)**; only
`count=1` was compared with the native server.

Detail routes return direct metadata containers with existing
X-Plex-Container-Start/Size paging:

- `/hubs/home/continueWatching`
- `/hubs/continueWatching/items`
- `/hubs/sections/{id}/continueWatching/items`
- `/hubs/home/onDeck`
- `/hubs/home/recentlyAdded?type=1` (movies)
- `/hubs/home/recentlyAdded?type=2` (episodes)

`GET /hubs/continueWatching` returns one provider hub pointing to its `/items`
route. Media-provider discovery now advertises that feature, Home's `/hubs`
key, and each section's `/hubs/sections/{id}` key.

## Selection and isolation

Continue Watching selects available movies/episodes with a positive local
resume position, ordered by latest playback activity then stable item ID.
On Deck uses the existing local On Deck algorithm and returns episodes only.
Recent hubs select available movies or episodes by descending added time.
TV recent results remain episodes; Plex TV grouping was not investigated.

Every hub and detail response filters library access and unavailable media
before limits, counts and metadata source keys are produced. Playback state
belongs to the requesting account. Hidden entries do not affect `more`.
These selection and isolation choices are **(sloplex)**.

Audio/photo types and `personal=1` recent-video requests return 400. Advanced
recommendations, promotion, grouping, pinning, cloud state, hub identifiers
filtering and official-client startup/playback certification remain open.
