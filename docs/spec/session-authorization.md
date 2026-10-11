# Session authorization (sloplex)

How long a transcode stays playable, and what ends it when access is
withdrawn. Request authentication is in [auth.md](auth.md); the transcode
endpoints themselves are in [transcode.md](transcode.md).

## Provenance

- Everything on this page is **(sloplex)**: our own design. No other
  server's behaviour was observed or copied for it.
- It answers the 2026-10-11 consolidated review, items G18 (Codex F16,
  Claude F-08): random session URLs prevent guessing but did not revoke a
  URL that was already known.
- Verified by `TestSessionRevocation` and `TestProgressiveRevocation` in
  `internal/server`, and `TestRevoke` in `internal/transcode`.

## Authority

Every transcode records the **authority** it was started under:

- the **account** of the request that started it;
- the **issued token** that request used, or none when it used an owner
  master token or came tokenless from an allowed network (see
  [auth.md](auth.md));
- the **item** being played.

The authority **holds** while all of these are true:

1. The account still exists.
2. If an issued token was recorded, that token still exists. Tokens are
   revoked by logout, `DELETE /sloplex/tokens/{id}`, a password change
   (which revokes the account's other tokens), and account deletion; see
   [accounts.md](accounts.md).
3. The item still exists and the account may see its library section
   under the current **Library access** rules in
   [accounts.md](accounts.md). Admins see every section.

Owner master tokens and `allowedNetworks` change only when the server
restarts, and a restart ends every transcode. Authority recorded without
an issued token therefore lasts as long as its account and item access.

Account and token IDs are never reused, so a new account or token cannot
inherit an old authority.

## HLS session URLs

The URLs under `/video/:/transcode/universal/session/{sid}/` need no token.
These are the media playlists, segments, and subtitle playlists and WebVTT
of every quality level. A player that cannot send headers can play a known
URL while the session's authority holds.

- Every request to a session URL checks the authority again before
  anything is served.
- If the authority no longer holds, the response is `404`, the same as for
  an unknown session. The session is stopped at once: ffmpeg is killed and
  the session's files are deleted.
- A response that was already being written when access was withdrawn is
  finished. Withdrawal applies to requests that start after the change
  that caused it.

## Stopping on changes

After each of these requests succeeds, the server stops **every** HLS
session and progressive stream whose authority no longer holds:

- `POST /sloplex/auth/logout`;
- `DELETE /sloplex/tokens/{id}`;
- `PUT /sloplex/account/password`;
- `PATCH /sloplex/accounts/{id}`, whatever it changed: libraries, admin
  flag, password, or name;
- `DELETE /sloplex/accounts/{id}`.

Access can also be lost without one of these requests, for example when a
library section is removed from the config or an item disappears in a
rescan. Such a change is caught at the session's next request. An idle
session is stopped by the 2-minute idle rule in
[transcode.md](transcode.md).

A stopped session stays stopped. Giving the access back later does not
bring it back: the player must start a new session.

## Labels and identity

- `stop` and `ping` act on the caller's account's sessions with the given
  label. Any valid credential of that account can use them.
- `ping` only marks a session as active. It never changes the session's
  recorded authority and cannot revive a revoked session.
- Starting a session with the label of an existing session of the same
  account stops the old session and starts a new one with a new ID, under
  the new request's authority. Session IDs are never reused.

## Progressive streams

`start.mp4` and `start.webm` need a valid credential on the request, like
other protected endpoints.

- While the stream runs, the server keeps its authority with it.
- When a change listed under **Stopping on changes** withdraws the
  authority, the stream's ffmpeg is killed and the response ends.
- The authority is checked again once the stream has a transcode slot,
  before any media is sent. This catches a change that lands between
  authentication and the stream starting. If it fails there, the response
  is `404`.

## Not covered yet

These are known gaps:

- Direct play and downloads of `/library/parts/...` and
  `/library/streams/...` are checked when each request starts. A response
  already being sent is not cut off when access is withdrawn.
- A session that stays in use has no maximum lifetime. It ends only by
  `stop`, by idling for 2 minutes, by revocation, or by a server restart.
- Access lost without one of the requests under **Stopping on changes**
  does not end a progressive stream that is already running.

## Retained unavailable sources

**(sloplex)** Source availability also gates playback; see [trash](trash.md).
HLS delivery revokes an unavailable source by its exact part ID, retaining
sessions using another healthy version of the same item. Progressive output
checks source availability on each write. This supplements account/token
authority without changing the public session URL format.


## Subprocess pipe shutdown

**(sloplex)** Transcoder commands bound the wait for inherited I/O pipes to
one second after cancellation or process exit. A descendant retaining stderr
must not indefinitely hold the request or transcode slot after the managed
process exits. This bounds pipe cleanup, not descendant process lifetime.
