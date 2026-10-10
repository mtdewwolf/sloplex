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
| GDM LAN discovery (UDP 32414)      | ✅ answers `M-SEARCH`; announces `HELLO`/`BYE` on start and stop |
| Local token auth, trusted CIDRs    | ✅                                       |
| Multiple users                     | ✅ accounts, passwords, per-device tokens, per-user watch history |
| Per-user library restrictions      | ✅ e.g. a kids account that sees only the kids library |
| Movie libraries: scan and browse   | ✅ title/year parsing, versions, ffprobe |
| Automatic library updates          | ✅ folder watching (Linux inotify), hourly backup scan |
| TV libraries: show → season → episode | ✅ `S01E02`, `1x02`, season folders, specials |
| Paging and sorting                 | ✅                                       |
| Search                             | ✅ `/hubs/search`, `/search`, per-section; accent-insensitive, prefix matching |
| Web app                            | ✅ `/web/`: sign-in, browse, search, play, account and admin pages |
| Direct play (byte-range streaming) | ✅                                       |
| Watched/resume state, On Deck      | ✅ scrobble, timeline, unwatched filter  |
| Artwork                            | ✅ local posters/fanart/banners, episode frame grabs, resizing |
| Date-based TV episodes             | ✅ `Show - 2024-03-15.mkv`, seasons by year |
| Music                              | ⏳                                       |
| Local metadata (`.nfo`)            | ✅ summaries, ratings, genres, cast, sort titles; read from files, never fetched online |
| Transcoding                        | ✅ HLS and progressive MP4 to H.264/AAC; WebM (VP9/Opus) for browsers without H.264 |
| HDR to SDR                         | ✅ HDR10 (PQ) and HLG tone mapped when transcoding |
| Hardware transcoding               | ✅ VAAPI, Intel QSV, NVIDIA NVENC, Apple VideoToolbox; auto-detected, falls back to software |
| CPU-only transcoding               | ✅ tone mapping at output size, benchmark-driven adaptive x264 preset, HLS throttling |
| Audio tracks and subtitles         | ✅ per-user track choice, sidecar `.srt/.vtt/.ass`, embedded text and image subs, burn-in, WebVTT renditions in HLS |

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

### Containers and TrueNAS

For a CPU-only container build and a TrueNAS 25.10 (Goldeye) Custom App
recipe, see [the deployment guide](docs/deploy-truenas.md). Build the image
yourself; this repository does not publish a container image.

### Web app

Open `http://<server>:32400/web/` in a browser and sign in. The app lets you:

- browse libraries, search, and play;
- use On Deck and mark items watched or unwatched;
- change your password and sign out devices.

Admins also get an Admin page for managing accounts, library access, device
tokens, and library refreshes.

Files the browser can decode play directly. Anything else is converted on
the fly, which needs ffmpeg. The conversion is H.264/AAC, or VP9/Opus WebM
for browsers without H.264. While converting, the player's seek bar still
covers the whole file: seeking restarts the conversion at the new point.

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

New, changed, and deleted files are picked up on their own:

- On Linux, Sloplex watches the library folders and rescans a library about
  5 seconds after changes stop.
- Every library is also rescanned every hour (`scanInterval`). This catches
  changes that watching can't see, such as files changed by another machine
  on a network share, and covers macOS and Windows, where watching isn't
  available.

A very large library can exceed Linux's default watch limit; Sloplex logs a
warning if it does. Raise it with
`sysctl fs.inotify.max_user_watches=524288`.

TV libraries expect `<root>/<Show Name (Year)>/Season NN/<file with S01E02>`.
Daily shows can use the air date instead, as in
`The Daily Show - 2024-03-15 - Guest.mkv`; they get one season per year.
See "Show sections" in [docs/spec/http-api.md](docs/spec/http-api.md) for the
full naming rules.

Subtitle files next to a video are picked up automatically when named
`<video name>.<language>.srt`, with optional `.forced` or `.sdh` tags. For
example, `Heat (1995).en.srt` or `Heat (1995).fr.forced.srt`. `.vtt`, `.ass`,
and `.ssa` files work too.

Embedded audio and subtitle tracks are listed per file. Each person's track
choice is remembered, and the web app has audio and subtitle pickers.

When transcoding:

- image subtitles (PGS, VobSub) are burned into the picture;
- text subtitles are sent to the player as a separate track, unless a client
  asks for `subtitles=burn`. HLS streams list them as WebVTT subtitle
  renditions, so HLS players can switch subtitles on their own.

See [docs/spec/streams.md](docs/spec/streams.md).

Summaries, ratings, genres, cast, and better titles come from Kodi-style
`.nfo` files next to your media: `<video name>.nfo` or `movie.nfo` for a
movie, `tvshow.nfo` in a show's folder, and `<episode name>.nfo` for an
episode. Tools such as tinyMediaManager can write them for you. Sloplex
never looks anything up online. See
[docs/spec/metadata.md](docs/spec/metadata.md).

Artwork comes from image files next to your media, such as `poster.jpg`,
`fanart.jpg`, `banner.jpg`, `season01-poster.jpg`, and `<episode>-thumb.jpg`.
See [docs/spec/artwork.md](docs/spec/artwork.md) for the names that are
recognised.

Install ffmpeg (which includes `ffprobe`) to get these extras:

- duration, codec, and resolution details. Without `ffprobe`, Sloplex
  reports only the container.
- generated thumbnails for episodes that have no image;
- transcoding for clients that can't play a file as it is. Clients can ask
  `/video/:/transcode/universal/decision` first, describing what they play,
  to learn whether a file plays directly, needs only a remux, or needs
  converting. They then use `start.m3u8` (HLS), `start.mp4`, or
  `start.webm`; see
  [docs/spec/transcode.md](docs/spec/transcode.md).

### Hardware transcoding

With `"hwaccel": "auto"` (the default), Sloplex tests each GPU encoder at
startup with a short encode and uses the first one that works:

- Linux: NVENC, then QSV, then VAAPI;
- macOS: VideoToolbox.

The result is logged and shown on the web app's Admin page.

Each transcode tries the fastest path first:

1. decode, scale, and encode all on the GPU;
2. if that fails, for example because the GPU can't decode the source
   codec, software decode with GPU encode;
3. otherwise libx264 in software.

So a missing driver or an unsupported file slows things down but never
breaks playback.

GPU access in containers:

- Intel/AMD (VAAPI, QSV): pass the render node into the container, for
  example `--device /dev/dri/renderD128`, and make sure the server's user is
  in the `render` group.
- NVIDIA: use the NVIDIA container runtime (`--gpus all`).

Set `"hwaccel": "none"` to always use software.

### CPU-only transcoding

Without a GPU, Sloplex does three things to keep playback smooth:

- **HDR tone mapping at the output size.** When downscaling, the picture
  is resized before the expensive tone-mapping math. Savings depend on
  the source, output size, and machine.
- **Adaptive x264 preset.** With `"x264Preset": "auto"` (the default),
  Sloplex measures libx264 at startup. Each transcode then gets the
  best-quality preset predicted to fit the CPU budget, given everything
  else that is transcoding. This is an estimate, not a playback guarantee.
  - An idle server uses `faster` when the estimated load fits.
  - A busy server, or a heavy source such as 4K HEVC HDR, moves down
    through `veryfast` and `superfast` to `ultrafast`.
  - Set a preset name to use that preset always.
- **Throttling.** An HLS transcode that gets more than 90 s ahead of the
  player is paused until the lead falls to 45 s. This leaves the CPU free
  for other streams.

A preset-matched synthetic 4K-to-1080p benchmark on the integration
machine took 5.586 s before output-size tone mapping and 1.673 s after
(3.34× faster). This does not include HEVC decoding or establish real-media
playback speed. Reproduce it with `python3 test/bench/tonemap.py`; see
"CPU transcoding" in [docs/spec/transcode.md](docs/spec/transcode.md).

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
| `hwaccel`         | `"auto"`       | `auto`, `none`, `vaapi`, `qsv`, `nvenc`, or `videotoolbox` |
| `hwaccelDevice`   | `/dev/dri/renderD128` | GPU render node for `vaapi` and `qsv`              |
| `watch`           | `true`         | Rescan a library when its folders change (Linux)          |
| `scanInterval`    | `"1h"`         | Rescan every library this often (at least `1m`); `"0"` turns it off |
| `x264Preset`      | `"auto"`       | `auto`, or one of `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium` |
| `transcodeThrottle` | `true`       | Pause HLS transcodes that are far ahead of the player     |
| `libraries`       | `[]`           | `{title, type: "movie" or "show", paths: [...]}`          |

## Layout

```
cmd/sloplex/        entrypoint
internal/config/    config file loading and validation
internal/identity/  machine identifier and owner master token
internal/accounts/  user accounts, password hashes, issued tokens
internal/library/   section/item store, filename parsing, scanner, automatic scans
internal/fswatch/   folder watching (inotify)
internal/playstate/ per-account watched/resume state
internal/probe/     ffprobe wrapper
internal/artwork/   frame grabs and cached image resizing
internal/search/    query folding and scoring
internal/web/       built-in web app (embedded static files)
internal/transcode/ ffmpeg HLS/MP4 transcoding sessions
internal/subtitles/ subtitle extraction, conversion and shifting
internal/lang/      language code normalisation
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

Browser end-to-end suites for the web app live in `test/e2e/`. They need
Node and Playwright; see [test/e2e/README.md](test/e2e/README.md).

```sh
cd test/e2e && npm install && npx playwright install chromium && ./run.sh
```
