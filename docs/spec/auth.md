# Authentication

## Provenance

- Using the `X-Plex-Token` header and query parameter as the token carrier
  follows publicly documented client behaviour.
- **Browser origins** relies on public web standards: the `Origin` request
  header ([RFC 6454](https://www.rfc-editor.org/rfc/rfc6454) and the
  [Fetch Standard](https://fetch.spec.whatwg.org/)) and the
  `Sec-Fetch-Site` header
  ([Fetch Metadata Request Headers](https://www.w3.org/TR/fetch-metadata/)).
  Browsers set both headers themselves, and page scripts cannot set them.
- Everything else on this page is **(sloplex)** design. There is no plex.tv
  account linking and no remote token validation.
- Refusing invalid tokens on allowed networks and **Browser origins**
  answer the 2026-10-11 consolidated review, items G15 and G16 (Codex F14
  and F15). They are verified by `TestInvalidTokensNeverOwner` and
  `TestTrustedNetworkOrigins` in `internal/server`.

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
- a browser's `GET /` without a token, which only redirects to `/web/`
  (see [http-api.md](http-api.md#get-));
- transcode session URLs under `/video/:/transcode/universal/session/`.
  They are authorised by their random session ID while the authority the
  session was started under holds; see
  [session-authorization.md](session-authorization.md);
- `OPTIONS` (CORS preflight) requests.

On these routes an invalid token is ignored. The request goes ahead without
an account.

Requests whose remote address falls in a CIDR from the config list
`allowedNetworks` may skip the token:

- With **no token**, they authenticate as the **owner**, unless a browser
  marks the request as coming from a page of another origin; see
  **Browser origins**.
- With a **valid token**, they authenticate as that token's account.
- With a token that is **not valid**, they fail like any other request; see
  **Failure**.
- Only the TCP peer address counts. Forwarding headers are **not** trusted.

## Failure

- A missing token gets `401 Unauthorized` with a plain-text body, except
  for tokenless requests from an allowed network as described above.
- A supplied token, in the header or the query string, that is not valid
  gets `401` on every protected route. This includes requests from an
  allowed network. Unknown, malformed, revoked, and deleted-account tokens
  are all not valid. A credential the client chose to send is never
  replaced by network trust, so a revoked device cannot become the owner.

## Browser origins (sloplex)

Tokenless access from an allowed network is an ambient credential: a
browser on that network has it on behalf of any page it shows. So the owner
grant is refused when the browser says a page of another origin sent the
request:

1. If the request has a `Sec-Fetch-Site` header:
   - `same-origin` or `none` (an address the user typed or bookmarked) may
     use tokenless access;
   - any other value (`same-site`, `cross-site`, or unknown) may not.
2. Otherwise, if the request has an `Origin` header, the request may use
   tokenless access only if the `Origin` scheme matches the direct request's
   transport (`https` for TLS, otherwise `http`) and its host (with its port,
   if any) equals the request's `Host`, compared without regard to case.
   User information, paths, queries, fragments, `Origin: null` and
   unparsable values do not match. Older browsers behind TLS-terminating
   proxies must supply a token when the origin scheme differs from the
   backend transport; forwarding headers do not relax this rule.
3. A request with neither header may use tokenless access. Native clients
   and command-line tools send neither header.

`X-Forwarded-Host`, `X-Forwarded-Proto`, `Forwarded`, and similar headers
are never consulted.

A refused request is treated as tokenless from an untrusted network: `401`
on protected routes, and unchanged on public ones. Requests that carry a
valid token are not affected, whatever their origin.

This applies to every method, so it covers:

- reads, so other origins cannot read the response;
- simple `GET` mutations such as `/:/scrobble` loaded as an image;
- form `POST`s that need no preflight;
- preflighted JSON requests.

Tokenless responses from an allowed network carry
`Vary: Origin, Sec-Fetch-Site`, because their result depends on these
headers.

### CORS

- Every response carries `Access-Control-Allow-Origin: *` and exposes
  `Content-Range`, `Content-Length`, and `Accept-Ranges`.
- An `OPTIONS` preflight gets `204` from any origin. It allows `GET`,
  `HEAD`, `PUT`, `POST`, `DELETE`, and `OPTIONS`, and the `X-Plex-*`, `Range`,
  `Accept`, and `Content-Type` request headers.
- Sloplex uses no cookies, and other origins get no tokenless access. So the
  wildcard only lets browser clients from any origin use the tokens they
  already hold.

### Limits

- A browser that sends neither `Sec-Fetch-Site` nor `Origin` on a no-cors
  `GET` (an `<img>` or `<script>` load) cannot be told apart from a native
  client. Older browsers without Fetch Metadata are like this. Such a
  request can still trigger a `GET` mutation, but the page cannot read the
  response.
- DNS rebinding is not prevented. If a page's own host name resolves to the
  server, the browser treats the page as the same origin, and the `Host`
  header carries that name.
- For both reasons, deployments that browse untrusted sites from an allowed
  network should use tokens and an empty `allowedNetworks` list.

## Strict token mode (obsolete)

Earlier versions let an invalid token on an allowed network fall back to
the owner, unless `"strictTokenAuth": true` was set. That fallback is
gone, and invalid tokens are always refused as described in **Failure**.

- The `strictTokenAuth` config key is still accepted, so existing config
  files load, but it has no effect.
- When it is `true`, the server logs at startup that it can be removed.
- No setting brings the fallback back. Tokenless access from
  `allowedNetworks` is unchanged apart from **Browser origins**.
