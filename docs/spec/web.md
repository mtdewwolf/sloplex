# Web app

## Provenance

Everything on this page is **(sloplex)**.

The web app is an ordinary client of the documented API, served by the server
itself. It uses no endpoint or behaviour beyond what the other spec pages
describe.

## Serving

- `GET /web/` serves the app. `GET /web` redirects to it.
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

### Account

- Change the password.
- List and revoke your own device tokens.

### Admin (admins only)

- List, create, edit, and delete accounts:
  - name, password, admin flag, and library access;
  - issue a device token, whose value is shown once.
- Refresh library sections.

### Text handling

All text from the server, such as titles and names, is inserted as text and
never parsed as HTML.
