# Playback sessions

## Provenance

Two controlled REA captures of PMS 1.43.4.10903, running from a copied local
profile in a loopback-only namespace with synthetic movies, observed how
`/:/timeline` reports appear in `GET /status/sessions`.
[Evidence and limitations](../evidence/plex-1.43.4-sessions.json) record the
sequence. Expiry, limits, account scoping and admin-only listing are
**(sloplex)**. The Go implementation is original.

## Timeline reports

A `GET` or `POST /:/timeline` report creates or updates a playback session when
it has a client identifier (`X-Plex-Client-Identifier` header or query) and a
`state` of `playing`, `paused`, `buffering` or `stopped`. Any other report keeps
the playstate behavior in [playstate.md](playstate.md); in particular, the
legacy `GET` still ignores an unknown `state`.

Session identity is the caller's account plus `X-Plex-Session-Identifier` when
that header is present, otherwise plus the client identifier. The query
parameters `session` and `sessionIdentifier` do not affect identity. A report
under an existing identity replaces its item, media/part, player fields and
state and keeps its `sessionKey`. If `time` is omitted for the same item, the
last position is kept.

`stopped` removes the session when it names the session's current item. It
needs no `time`.

Session reports reject duplicate parameters, negative `time`, `duration`,
`mediaIndex` or `partIndex`, out-of-range media/part indexes, and non-leaf
items with 400. Unavailable items and parts return 404. A report changes
nothing if the playstate or queue save fails.

## Listing

`GET /status/sessions` requires an admin token **(sloplex)** and returns one
`Video` per live session, ordered by `sessionKey`. Each entry is the item's
normal metadata with `sessionKey`, `viewOffset` and `duration` taken from the
report, `playQueueItemID` when one was reported, a single `Media` and `Part`
for the reported indexes, a `User` (`id`, `title`) and a `Player`
(`machineIdentifier`, `state`, `product`, `platform`, `version`, `title`
from `X-Plex-Device-Name`, `userID`).

Sessions expire 2 minutes after their last report. Each account keeps at
most 32 and the server at most 256; the oldest is evicted **(sloplex)**.
Sessions whose account, token, library access, item or part is no longer
valid are dropped when listed.

## Limits

Sessions are held in memory and do not survive restart. Transcode session
details, bandwidth, player address/local/relayed/secure fields, session
termination and event notifications are not implemented.
