# Sloplex protocol specification

This folder describes Sloplex's implemented behavior: HTTP endpoints,
response shapes, headers and LAN discovery. Keep the contract, implementation
and regression tests consistent.

## Sources

The owner authorized REA analysis of the supplied Plex Linux release on
2026-10-12, replacing the previous clean-room source restriction. Specifications
may use public documentation, lawful runtime observations, static findings
from supplied artifacts, and original design. Every page must record provenance;
static analysis and inferred behavior must not be presented as runtime proof.
Artifact-based research records its digest, version, source location and REA
Evidence ID. Implementation code is original; leaked source and copied
proprietary implementation code are excluded. Historical review records retain
the policy that applied when they were written.

Sloplex does not talk to plex.tv. Identity, accounts and tokens remain local.
The optional GitHub update check and configured TrueNAS integration keep their
existing contracts; see [updates.md](updates.md).

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
| [providers.md](providers.md) | Local media-provider capabilities and section discovery |
| [home-hubs.md](home-hubs.md) | Home/section hubs, resume items and detail routes |
| [playstate.md](playstate.md) | Watched/resume state, scrobbling, On Deck                |
| [artwork.md](artwork.md)   | Posters, backgrounds, frame grabs, image resizing          |
| [search.md](search.md)     | Search, hubs, matching and ranking                         |
| [web.md](web.md)           | The built-in web app at `/web/`                            |
| [transcode.md](transcode.md) | HLS, progressive MP4 and WebM transcoding                |
| [streams.md](streams.md)   | Audio tracks, subtitles, stream selection, burn-in         |
| [metadata.md](metadata.md) | Local `.nfo` metadata: summaries, ratings, genres, cast    |
| [collections.md](collections.md) | Collections: automatic from `.nfo` sets, and manual  |
| [playqueues.md](playqueues.md) | Local video queue lifecycle, windows, ordering and ownership |
| [playlists.md](playlists.md) | Per-account playlists of movies and episodes           |
| [updates.md](updates.md)   | Update check and one-click updates through TrueNAS         |
| [preferences.md](preferences.md) | Server preferences registry and `/:/prefs`           |

## Conventions

- "MUST", "SHOULD", and "MAY" are used as in RFC 2119.
- Timestamps are Unix seconds. Durations are milliseconds.
- Behaviour marked **(sloplex)** is our own design choice. It is not
  compatibility behaviour.
