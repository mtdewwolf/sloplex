# Web app

## Provenance

Everything on this page is **(sloplex)**.

The web app is an ordinary client of the documented API, served by the server
itself. It uses no endpoint or behaviour beyond what the other spec pages
describe.

## Serving

- `GET /web/` serves the app. `GET /web` redirects to it, and so does a
  browser's `GET /` without a token (see [http-api.md](http-api.md#get-)).
- The app is static HTML, CSS, and JavaScript embedded in the server binary.
  It has no external dependencies and makes no requests to other hosts.
- `/web/` and everything under it needs no token, because the files contain
  no data. All data comes from authenticated API calls.

Responses under `/web/` carry these headers:

| Header                    | Value                                              |
|---------------------------|----------------------------------------------------|
| `Content-Security-Policy` | `default-src 'self'; img-src 'self' data:; media-src 'self'; script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'` |
| `X-Content-Type-Options`  | `nosniff`                                          |
| `Referrer-Policy`         | `no-referrer`                                      |
| `Cache-Control`           | `no-cache`                                         |

`Referrer-Policy: no-referrer` matters because image and video URLs carry the
token as a query parameter.

## Behaviour

### Sign-in

- The app signs in with `POST /sloplex/auth/login`.
- It keeps the token in the browser's `localStorage` and sends it as
  `X-Plex-Token`. Media and image URLs, which cannot carry headers, get it
  as a query parameter instead.
- Signing out calls `POST /sloplex/auth/logout` and forgets the token.

### Library

- Sections are listed, then their items, as a poster grid.
  - Posters come from `/photo/:/transcode`.
  - Shows open into seasons, and seasons into episodes.
  - A section page lists its collections (see
    [collections.md](collections.md)) above its items, when it has any. A
    collection opens into its members. Collections are managed through the
    API only.
  - Item pages show `.nfo` metadata when present (see
    [metadata.md](metadata.md)): original title, tagline, summary, content
    rating, rating, genres, directors, writers, studio, collections, and up
    to 12 cast members.
  - Episodes are labelled `S1E2`, or by air date (`originallyAvailableAt`)
    for date-based episodes, whose season numbers are years.
- Search uses `/hubs/search`.
- Selecting a movie or episode plays its first part with the browser's
  `<video>` element:
  - The resume point is the item's `viewOffset`.
  - Progress is reported with `/:/timeline` every 10 s and on pause or end.
  - If the browser cannot decode the file, the app converts it at the
    current position (see [transcode.md](transcode.md)). See **Converted
    playback**.

### Converted playback (sloplex)

- **Format.** The app uses `start.mp4` when `canPlayType` accepts H.264/AAC
  in MP4. Otherwise it uses `start.webm` with `videoResolution=1280x720`
  when VP9/Opus in WebM is accepted. If neither is accepted, it shows a
  message and does not start a conversion.
- **Position.** A converted stream starts at `offset` seconds of the source.
  The source position is `offset` plus the video's `currentTime`, capped at
  the item's duration. Timeline reports and the subtitle `offset` use it.
- **Controls.** The browser's own controls would show only the converted
  part, so the app replaces them:
  - a play/pause button; clicking the video also toggles;
  - a seek bar from 0 to the part's duration (or the item's), with a
    `position / duration` clock;
  - mute and full-screen buttons. Full screen applies to the player, so the
    controls stay visible.
- **Seeking.** Moving the seek bar updates the clock only. Letting go seeks:
  - if the target has already arrived and the browser reports it in
    `seekable`, the video seeks within the stream;
  - otherwise the stream restarts at the target, after 350 ms without
    another seek, so dragging or repeated key presses start a single
    conversion. A "Converting…" overlay shows until the video can play.
  - Chromium reports nothing as seekable for these streams, so there every
    seek restarts.
  - A paused player stays paused after a restart.
  - Targets are clamped to between 0 and 1 s before the end.
- **Keys**, when the player has focus: Space or `k` play/pause, ←/→ seek
  10 s back or forward from the latest target, Home and End seek to the
  start and the end, `m` mutes, `f` toggles full screen.

### Playlists

See [playlists.md](playlists.md).

- **Playlists** in the navigation lists the account's playlists, with their
  item count and length. It can create and delete playlists.
- Movie and episode pages have an **Add to playlist** menu: one of the
  account's playlists, or a new one, named in a prompt.
- A playlist page lists its entries. Each can be moved up or down, or
  removed. The playlist can be renamed.
- **Play all** plays the entries in order, each from its start, and moves
  on when one ends. The current entry is highlighted. Playback follows the
  same rules as playing an item directly, including converting.

### Account

- Change the password.
- List and revoke your own device tokens.

### Admin (admins only)

- List, create, edit, and delete accounts:
  - name, password, admin flag, and library access;
  - issue a device token, whose value is shown once.
- Refresh library sections.
- Show how the server transcodes, and its version from `/identity`, so an
  admin can confirm which release is running.
- Check for updates, and link to a newer release; see
  [updates.md](updates.md). When one-click updates are set up, an
  **Update** button asks TrueNAS to install it. The page then reloads once
  the server answers with a new version, giving up after ten minutes.
- The owner, and no other admin, can set up one-click updates: the TrueNAS
  address, app name, API key, and certificate fingerprint. A saved key is
  never shown again. When a test finds an untrusted certificate, its
  fingerprint is shown with a button to trust it.

### Text handling

All text from the server, such as titles and names, is inserted as text and
never parsed as HTML.
