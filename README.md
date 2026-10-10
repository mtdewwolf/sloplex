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
| Multiple users                     | ✅ accounts, passwords, per-device tokens, per-user watch history |
| Per-user library restrictions      | ✅ e.g. a kids account that sees only the kids library |
| Movie libraries: scan and browse   | ✅ title/year parsing, versions, ffprobe |
| TV libraries: show → season → episode | ✅ `S01E02`, `1x02`, season folders, specials |
| Paging and sorting                 | ✅                                       |
| Search                             | ✅ `/hubs/search`, `/search`, per-section; accent-insensitive, prefix matching |
| Web app                            | ✅ `/web/`: sign-in, browse, search, play, account and admin pages |
| Direct play (byte-range streaming) | ✅                                       |
| Watched/resume state, On Deck      | ✅ scrobble, timeline, unwatched filter  |
| Artwork                            | ✅ local posters/fanart/banners, episode frame grabs, resizing |
| Date-based TV episodes             | ⏳                                       |
| Music                              | ⏳                                       |
| External metadata agents           | ⏳                                       |
| Transcoding                        | ✅ HLS and progressive MP4 to H.264/AAC (software, libx264) |
| Hardware transcoding, subtitles in transcodes | ⏳                            |

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

### Web app

Open `http://<server>:32400/web/` in a browser and sign in. The app lets you:

- browse libraries, search, and play;
- use On Deck and mark items watched or unwatched;
- change your password and sign out devices.

Admins also get an Admin page for managing accounts, library access, device
tokens, and library refreshes.

Files the browser can decode play directly. Anything else is converted on
the fly to H.264/AAC, which needs ffmpeg.

### Users

The first start creates an **owner** account with no password. The token in
`data/identity.json` always acts as the owner, and it is also the recovery key
if every password is lost.

To give the owner a password, and to add someone else:

```sh
curl -X PUT -H "X-Plex-Token: $TOKEN" -d '{"new":"a-good-password"}' localhost:32400/sloplex/account/password
curl -X POST -H "X-Plex-Token: $TOKEN" -d '{"name":"alice","password":"wonderland"}' localhost:32400/sloplex/accounts
```

Each person logs in to get their own token:

```sh
curl -X POST -d '{"username":"alice","password":"wonderland","device":"Phone"}' localhost:32400/sloplex/auth/login
```

Watch history, On Deck, and the unwatched filter are tracked separately for
each account.

- Only admins can refresh libraries or manage accounts.
- For a device where typing a password is awkward, an admin can issue a token
  with `POST /sloplex/accounts/{id}/tokens`.
- To limit an account to certain libraries, set `libraries` to their section
  IDs, for example `"libraries": [3]`. Use `null` to allow every library.
  Hidden libraries disappear from that account's lists and return 404
  everywhere else, streaming and images included.

The full API is in [docs/spec/accounts.md](docs/spec/accounts.md).

### Libraries

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
- generated thumbnails for episodes that have no image;
- transcoding for clients that can't play a file as it is. Clients use
  `/video/:/transcode/universal/start.m3u8` (HLS) or `start.mp4`; see
  [docs/spec/transcode.md](docs/spec/transcode.md).

## Configuration

| Key               | Default        | Meaning                                                   |
|-------------------|----------------|-----------------------------------------------------------|
| `friendlyName`    | hostname       | The name shown to clients                                 |
| `listen`          | `:32400`       | HTTP listen address                                       |
| `dataDir`         | `data`         | Holds `identity.json`, `accounts.json`, `library.json`, `playstate.json`, `cache/` |
| `discovery`       | `true`         | Answer GDM searches on UDP 32414                          |
| `tokens`          | `[]`           | Extra owner master tokens                                 |
| `allowedNetworks` | `[]`           | CIDRs that may skip tokens; tokenless requests act as the owner |
| `ffprobe`         | `""`           | ffprobe path; `""` searches `PATH`, `"-"` disables probing |
| `ffmpeg`          | `""`           | ffmpeg path for transcoding and episode thumbnails; `""` searches `PATH`, `"-"` disables |
| `maxTranscodes`   | `2`            | Concurrent transcodes (HLS sessions plus MP4 streams)     |
| `libraries`       | `[]`           | `{title, type: "movie" or "show", paths: [...]}`          |

## Layout

```
cmd/sloplex/        entrypoint
internal/config/    config file loading and validation
internal/identity/  machine identifier and owner master token
internal/accounts/  user accounts, password hashes, issued tokens
internal/library/   section/item store, filename parsing, scanner
internal/playstate/ per-account watched/resume state
internal/probe/     ffprobe wrapper
internal/artwork/   frame grabs and cached image resizing
internal/search/    query folding and scoring
internal/web/       built-in web app (embedded static files)
internal/transcode/ ffmpeg HLS/MP4 transcoding sessions
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
