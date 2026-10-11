# Sloplex protocol specification

This folder is the **clean-room boundary** of the project. It describes the
externally observable behaviour Sloplex implements: HTTP endpoints, response
shapes, headers, and the LAN discovery protocol. Implementation code in
`internal/` and `cmd/` is written **from these documents**, not from any other
source.

## Rules

1. **Allowed sources** for spec content:
   - publicly available, freely published documentation of the media-server
     HTTP API and GDM discovery protocol (community wikis, open-source *client*
     libraries' docs, blog posts);
   - black-box observation of network traffic between software you lawfully
     own and run, e.g. packet captures or HTTP logs;
   - our own design decisions. Sloplex is its own product and may deviate.
2. **Forbidden sources**, for both spec and code:
   - decompiled, disassembled, or otherwise reverse-engineered binaries;
   - leaked or proprietary source code;
   - copying text, schemas, or code from projects whose licence does not
     allow it.
3. Every spec page has a **Provenance** section that says where its facts came
   from. When you add a fact, add its source.
4. Any change to wire behaviour updates the spec in the same pull request.
   Reviewers check that the code matches the spec, not the other way round.
5. Sloplex does **not** talk to plex.tv. Identity, accounts, and tokens are
   local to the server. The only outbound connections are the update check
   to GitHub, which can be turned off, and the owner's own TrueNAS server
   when they set one up; see [updates.md](updates.md).

## Pages

| Page                       | Covers                                                     |
|----------------------------|------------------------------------------------------------|
| [http-api.md](http-api.md) | Transport, content negotiation, MediaContainer, endpoints  |
| [auth.md](auth.md)         | Local token authentication                                 |
| [session-authorization.md](session-authorization.md) | Revocable HLS and progressive transcode authority |
| [scan-safety.md](scan-safety.md) | Incomplete scan protection and probe retry behavior |
| [trash.md](trash.md) | Retained unavailable media, same-path revival and explicit empty trash |
| [library-identity.md](library-identity.md) | Stable configured-library IDs and legacy adoption |
| [wire-conformance.md](wire-conformance.md) | Source-linked methods and paths for existing operations |
| [accounts.md](accounts.md) | Accounts, passwords, tokens, permissions, management API   |
| [discovery.md](discovery.md) | GDM LAN discovery                                        |
| [playstate.md](playstate.md) | Watched/resume state, scrobbling, On Deck                |
| [artwork.md](artwork.md)   | Posters, backgrounds, frame grabs, image resizing          |
| [search.md](search.md)     | Search, hubs, matching and ranking                         |
| [web.md](web.md)           | The built-in web app at `/web/`                            |
| [transcode.md](transcode.md) | HLS, progressive MP4 and WebM transcoding                |
| [streams.md](streams.md)   | Audio tracks, subtitles, stream selection, burn-in         |
| [metadata.md](metadata.md) | Local `.nfo` metadata: summaries, ratings, genres, cast    |
| [collections.md](collections.md) | Collections: automatic from `.nfo` sets, and manual  |
| [playlists.md](playlists.md) | Per-account playlists of movies and episodes           |
| [updates.md](updates.md)   | Update check and one-click updates through TrueNAS         |
| [preferences.md](preferences.md) | Server preferences registry and `/:/prefs`           |

## Conventions

- "MUST", "SHOULD", and "MAY" are used as in RFC 2119.
- Timestamps are Unix seconds. Durations are milliseconds.
- Behaviour marked **(sloplex)** is our own design choice. It is not
  compatibility behaviour.
