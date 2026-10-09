# Authentication

## Provenance

- Using the `X-Plex-Token` header and query parameter as the token carrier
  follows publicly documented client behaviour.
- Everything else on this page is **(sloplex)** design. There is no plex.tv
  account linking and no remote token validation.

## Tokens

- The server holds a set of valid access tokens. They come from:
  - the server's own generated token, which is created on first start and
    stored in `<dataDir>/identity.json` with mode `0600`;
  - any extra tokens listed in the config file under `tokens`.
- A client presents a token in either of these places:
  - the `X-Plex-Token` request header (preferred);
  - the `X-Plex-Token` query parameter, for players that cannot set headers
    on media URLs.
- Tokens are compared in constant time.

## Unauthenticated access

- `GET /identity` never requires a token.
- `OPTIONS` (CORS preflight) requests never require a token.
- Requests whose remote address falls in a CIDR from the config list
  `allowedNetworks` skip the token check. Only the TCP peer address counts;
  forwarding headers are **not** trusted.

## Failure

- A missing or invalid token gets `401 Unauthorized` with a plain-text body.
