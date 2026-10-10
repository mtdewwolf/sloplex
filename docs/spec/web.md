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
- Search uses `/hubs/search`.
- Selecting a movie or episode plays its first part with the browser's
  `<video>` element:
  - The resume point is the item's `viewOffset`.
  - Progress is reported with `/:/timeline` every 10 s and on pause or end.
  - Only formats the browser can decode play, since there is no transcoding
    yet.

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
