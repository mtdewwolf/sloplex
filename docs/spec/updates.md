# Updates

## Provenance

- Everything on this page is **(sloplex)**, except as noted below.
- The release lookup uses GitHub's public REST API, as documented by
  GitHub:
  - `GET /repos/{owner}/{repo}/releases/latest`, which returns the newest
    release that is neither a draft nor a prerelease;
  - its fields `tag_name`, `name`, `html_url` and `published_at`;
  - the `Accept: application/vnd.github+json` and `User-Agent` headers.
- The TrueNAS calls come from TrueNAS's published API documentation for
  25.10:
  - JSON-RPC 2.0 over a WebSocket at `/api/current`;
  - `auth.login_with_api_key`, `app.query`, `app.pull_images` (a job that
    needs the `APPS_WRITE` role, with option `redeploy`), and
    `core.get_jobs`;
  - the rule that TrueNAS revokes an API key sent over plain HTTP.
- WebSocket framing follows RFC 6455.

## Update check

The server can check whether a newer Sloplex release exists.

- It reads the latest release of `mtdewwolf/sloplex` from
  `https://api.github.com/repos/mtdewwolf/sloplex/releases/latest`.
  `updateURL` (config) replaces that address, to follow a fork or for
  testing. It must be an `http` or `https` URL that answers the same way.
- The release's version is its `tag_name` without a leading `v`.
- A newer release exists when both versions follow `X.Y.Z` (with an
  optional `-prerelease`) and the release's is higher, compared as
  semantic versions.
  - A build whose version is not of that form, such as `0.1.0-dev` from
    source or a commit hash, never reports an update. Its status says
    the version cannot be compared.
- When `updateCheck` (config, default `true`) is on, the server checks 1
  minute after starting and then every 24 hours. When it is off, it checks
  only when an admin asks.
- A check asked for within 60 seconds of the previous one returns the
  previous result instead of calling GitHub again.
- The request times out after 15 seconds. A failed check keeps the last
  good result and records the error.

This and the TrueNAS calls below are the server's only outbound
connections.

## One-click update through TrueNAS

When Sloplex runs as a TrueNAS app with a moving image tag (`latest` or
`X.Y`), the owner can let it ask TrueNAS to pull the new image and restart
the app.

### Settings

- The owner saves the TrueNAS address, the app's name, and an API key.
- Settings are stored in `<dataDir>/truenas.json` with mode `0600`.
- The API key is never returned by any endpoint.
- The address must be `https://host` or `https://host:port`, with no path.
  Plain HTTP is refused, because TrueNAS revokes a key sent over it.
- The app name is 1 to 40 characters: lower-case letters, digits and `-`,
  starting with a letter. It is the name the app was installed with.
- An optional certificate fingerprint pins the server's certificate. It
  is the SHA-256 of the leaf certificate's DER bytes, as 64 hex digits.
  Colons and case are ignored.
  - With a fingerprint, only a certificate with that fingerprint is
    accepted, and the host name is not checked. This suits TrueNAS's
    default self-signed certificate.
  - Without one, the certificate must be valid for the host under the
    system's trusted authorities.
- Changing the address or the fingerprint requires sending the API key
  again in the same request. A request that changes either without a key
  is refused with 400. This keeps a stored key from ever being sent to a
  server it was not entered for.

### Talking to TrueNAS

1. The server opens a TLS connection, then a WebSocket at `/api/current`.
2. It calls `auth.login_with_api_key` with the key. A result other than
   `true` is a failed login.
3. A connection test then calls `app.query` with the filter
   `[["name", "=", <app>]]` and reports the app's `state`, or that no such
   app exists.
4. An update calls `app.pull_images` with `[<app>, {"redeploy": true}]`. The
   result is a job ID. The server reads the job once with `core.get_jobs`
   after about 2 seconds. A `FAILED` job is reported with its `error`;
   otherwise the update has started.

- A whole exchange times out after 30 seconds.
- Messages without the expected `id`, such as event notifications, are
  skipped.
- A TLS handshake that fails because the certificate is not trusted
  reports that certificate's fingerprint, so the owner can compare it with
  the one TrueNAS shows and pin it.

TrueNAS then pulls the image and recreates the app's containers, which
restarts Sloplex. Playback stops until it is back.

## JSON API

All responses are JSON.

### `GET /sloplex/updates` (admin)

Returns the update status:

```json
{
  "current": "0.2.0",
  "comparable": true,
  "latest": "0.3.0",
  "available": true,
  "releaseName": "Sloplex 0.3.0",
  "releaseUrl": "https://github.com/mtdewwolf/sloplex/releases/tag/v0.3.0",
  "publishedAt": 1791660000,
  "checkedAt": 1791661234,
  "error": "",
  "autoCheck": true,
  "oneClick": true
}
```

- `latest`, `releaseName`, `releaseUrl` and `publishedAt` are omitted
  before the first successful check.
- `checkedAt` is the time of the last attempt, successful or not, and is
  omitted before the first one. `error` describes a failed last attempt.
- `oneClick` is `true` when TrueNAS settings with a key are saved.

### `POST /sloplex/updates/check` (admin)

Checks now, subject to the 60-second limit, and returns the status as
above.

### `POST /sloplex/updates/install` (admin)

Asks TrueNAS to update the app.

- 409 when TrueNAS is not set up, or when no newer release is known.
  Checking first is the caller's job.
- 502 when TrueNAS cannot be reached, refuses the login, or reports a
  failed job. The body's `error` says which.
- Otherwise 202 with `{"job": <id>}`. The server restarts soon after.

### `GET /sloplex/truenas` (owner)

Returns the settings without the key:

```json
{"url": "https://192.168.1.10", "app": "sloplex", "certSHA256": "ab12…", "keySet": true}
```

Before any settings are saved, `url`, `app` and `certSHA256` are empty and
`keySet` is `false`.

### `PUT /sloplex/truenas` (owner)

Body: `{"url", "app", "apiKey", "certSHA256"}`. `apiKey` may be omitted to
keep the stored key, unless the address or fingerprint changes (see
**Settings**). Returns the settings as for `GET`. 400 for invalid values.

### `DELETE /sloplex/truenas` (owner)

Removes the settings and the key. Returns 204.

### `POST /sloplex/truenas/test` (owner)

Body as for `PUT`, which is tested without being saved. A missing `apiKey`
uses the stored key, under the same rule as `PUT`. Returns:

```json
{"ok": true, "state": "RUNNING"}
{"ok": false, "error": "certificate not trusted", "fingerprint": "ab12…"}
```

- `ok` is `true` when the login works and the app exists. `state` is the
  app's state in TrueNAS.
- On failure, `error` says why. `fingerprint` is set when the certificate
  was not trusted.

"Owner" endpoints return 403 for every other account, admins included.
