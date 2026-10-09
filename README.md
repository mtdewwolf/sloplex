# Sloplex

Sloplex is a self-hosted media server whose HTTP API and LAN discovery
follow the Plex Media Server protocol. It is a clean-room implementation in
Go.

Sloplex is a standalone product:

- It never contacts plex.tv or any other hosted service.
- Accounts and tokens are local to your server.

> Sloplex is not affiliated with, endorsed by, or connected to Plex, Inc.
> "Plex" is a trademark of its owner. The name is used here only to describe
> protocol compatibility.

## Status

| Area                               | State                                    |
|------------------------------------|------------------------------------------|
| Server root, `/identity`           | ✅                                       |
| GDM LAN discovery (UDP 32414)      | ✅ answers `M-SEARCH`                    |
| Local token auth, trusted CIDRs    | ✅                                       |
| Movie libraries: scan and browse   | ✅ title/year parsing, versions, ffprobe |
| TV libraries: show → season → episode | ✅ `S01E02`, `1x02`, season folders, specials |
| Paging and sorting                 | ✅                                       |
| Direct play (byte-range streaming) | ✅                                       |
| Watched/resume state, On Deck      | ✅ scrobble, timeline, unwatched filter  |
| Artwork                            | ✅ local posters/fanart/banners, episode frame grabs, resizing |
| Date-based TV episodes             | ⏳                                       |
| Music                              | ⏳                                       |
| External metadata agents           | ⏳                                       |
| Transcoding (HLS/DASH)             | ⏳                                       |
| Multiple users                     | ⏳ play state is already per account     |

## Quick start

```sh
go build -o sloplex ./cmd/sloplex
cp sloplex.example.json sloplex.json   # then edit the library paths
./sloplex -config sloplex.json
```

On first start, Sloplex generates:

- a machine identifier;
- an access token, which it prints to the log and stores in
  `<dataDir>/identity.json`.

Pass the token as the `X-Plex-Token` header or query parameter:

```sh
TOKEN=$(jq -r .token data/identity.json)
curl -H "X-Plex-Token: $TOKEN" localhost:32400/library/sections
curl -H "X-Plex-Token: $TOKEN" -H "Accept: application/json" localhost:32400/library/sections/1/all
```

TV libraries expect `<root>/<Show Name (Year)>/Season NN/<file with S01E02>`.
See "Show sections" in [docs/spec/http-api.md](docs/spec/http-api.md) for the
full naming rules.

Artwork comes from image files next to your media, such as `poster.jpg`,
`fanart.jpg`, `banner.jpg`, `season01-poster.jpg`, and `<episode>-thumb.jpg`.
See [docs/spec/artwork.md](docs/spec/artwork.md) for the names that are
recognised.

Install ffmpeg (which includes `ffprobe`) to get two extras:

- duration, codec, and resolution details. Without `ffprobe`, Sloplex
  reports only the container.
- generated thumbnails for episodes that have no image.

## Configuration

| Key               | Default        | Meaning                                                   |
|-------------------|----------------|-----------------------------------------------------------|
| `friendlyName`    | hostname       | The name shown to clients                                 |
| `listen`          | `:32400`       | HTTP listen address                                       |
| `dataDir`         | `data`         | Holds `identity.json`, `library.json`, `playstate.json`, `cache/` |
| `discovery`       | `true`         | Answer GDM searches on UDP 32414                          |
| `tokens`          | `[]`           | Extra accepted tokens                                     |
| `allowedNetworks` | `[]`           | CIDRs that skip token auth                                |
| `ffprobe`         | `""`           | ffprobe path; `""` searches `PATH`, `"-"` disables probing |
| `ffmpeg`          | `""`           | ffmpeg path for episode thumbnails; `""` searches `PATH`, `"-"` disables |
| `libraries`       | `[]`           | `{title, type: "movie" or "show", paths: [...]}`          |

## Layout

```
cmd/sloplex/        entrypoint
internal/config/    config file loading and validation
internal/identity/  machine identifier and token persistence
internal/library/   section/item store, filename parsing, scanner
internal/playstate/ per-account watched/resume state
internal/probe/     ffprobe wrapper
internal/artwork/   frame grabs and cached image resizing
internal/server/    HTTP API (MediaContainer XML/JSON)
internal/gdm/       LAN discovery responder
docs/spec/          the clean-room protocol specification
```

## Development

```sh
go vet ./...
go test -race ./...
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) before you contribute. The
clean-room rules there are mandatory.
