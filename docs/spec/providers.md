# Local media provider discovery

## Provenance

REA inspected the supplied PMS 1.43.4.10903 Linux release and its Plex Web
4.160.0 factories. A controlled server capture observed `/media/providers`
with populated synthetic movie libraries in an isolated loopback-only network.
Hash-verified static factories establish the client's provider/feature field
reads. [Evidence and limitations](../evidence/plex-1.43.4-providers.json) record
both sources separately. The Go implementation is original.

## Contract

`GET /media/providers` requires normal local authentication and returns one
`MediaProvider` in the usual XML/JSON `MediaContainer`, with `size=1`.
The provider identifier is `com.plexapp.plugins.library`, title `Library`,
types `video`, and protocols `stream,download`.

Its `Feature` entries advertise the implemented local subset:

| Type | Key | Additional fields |
| --- | --- | --- |
| content | /library/sections | Directory entries for visible sections |
| search | /hubs/search | |
| continuewatching | /hubs/continueWatching | |
| metadata | /library/metadata | |
| imagetranscoder | /photo/:/transcode | |
| playlist | /playlists | flavor=universal |
| playqueue | /playQueues | flavor=universal |
| timeline | /:/timeline | scrobbleKey=/:/scrobble, unscrobbleKey=/:/unscrobble |

Home links to `/hubs`. Each section directory has its stable numeric `id`, absolute `key`, title,
type, local scanner/agent identity, `hubKey`, and existing section timestamps/UUID.
Sections follow the caller's library access. Filesystem `Location` entries
are omitted. Directory ordering and access projection are **(sloplex)**.

`ignore_opt_outs=true` is accepted and has no effect: this instance has no
cloud providers or provider opt-out mechanism. It does not bypass local
library access. Discovery is read-only.

## Limits

Music/photo media providers, matching, rating, advanced recommendation hubs,
continue-watching removal actions, cloud claiming and provider management
are not advertised. Provider discovery does not establish complete client
startup or playback compatibility. The shipped client can infer additional
legacy capabilities; those inferences do not make unsupported routes exist.
