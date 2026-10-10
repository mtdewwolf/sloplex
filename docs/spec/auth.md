# Authentication

## Provenance

- Using the `X-Plex-Token` header and query parameter as the token carrier
  follows publicly documented client behaviour.
- Everything else on this page is **(sloplex)** design. There is no plex.tv
  account linking and no remote token validation.

## Tokens

Every authenticated request resolves to exactly one **account**; see
[accounts.md](accounts.md). A client presents a token in one of two places:

- the `X-Plex-Token` request header (preferred);
- the `X-Plex-Token` query parameter, for players that cannot set headers on
  media URLs.

The token is resolved in this order:

1. **Owner master tokens:**
   - the server's own generated token, created on first start and stored in
     `<dataDir>/identity.json` with mode `0600`;
   - any extra tokens listed in the config under `tokens`.

   They are compared in constant time and authenticate as the owner
   (account 1).
2. **Issued tokens**, created by login or by an admin. They are looked up by
   their SHA-256 hash and authenticate as the account that owns them.

## Unauthenticated access

These requests never require a token:

- `GET /identity`;
- `POST /sloplex/auth/login`;
- `GET /web/` and everything under it (static app files; see
  [web.md](web.md));
- transcode session URLs under `/video/:/transcode/universal/session/`.
  They are authorised by their random session ID; see
  [transcode.md](transcode.md);
- `OPTIONS` (CORS preflight) requests.

Requests whose remote address falls in a CIDR from the config list
`allowedNetworks` work without a token:

- With no token, they authenticate as the **owner**.
- With a valid token, they authenticate as that token's account.
- Only the TCP peer address counts. Forwarding headers are **not** trusted.

## Failure

- A missing or invalid token gets `401 Unauthorized` with a plain-text body.
- For a trusted network, an invalid token is ignored and the request falls
  back to the owner.

## Strict token mode (sloplex)

Set `"strictTokenAuth": true` to reject an invalid or revoked supplied token
with `401` on protected routes, including on trusted networks. This is an
opt-in hardening option; it defaults to `false` for existing deployments.
Tokenless trusted requests still act as owner, valid tokens still select
their account, and public identity/login/session routes remain public.
Native clients should use this mode or an empty `allowedNetworks` list.
The public random HLS session URL remains a bearer capability until stopped
or expired; strict token mode does not revoke an already issued session URL.

Provenance: our own design, verified by `TestStrictTrustedTokens` and the
existing trusted-network compatibility tests.
