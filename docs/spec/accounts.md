# Accounts

## Provenance

- `GET /accounts`, with `Account` elements carrying `id`, `key`, and `name`,
  follows publicly available community documentation of the media-server
  HTTP API.
- Everything else on this page is **(sloplex)**. It covers:
  - the account model;
  - password storage;
  - the JSON management API under `/sloplex/`;
  - the permission rules.
- Sloplex has no hosted account service. Accounts exist only on the server.

## Model

### Account

| Field       | Notes                                                              |
|-------------|--------------------------------------------------------------------|
| `id`        | Integer, never reused                                              |
| `name`      | Unique, case-insensitive; 1–32 of `A–Z a–z 0–9 . _ -`              |
| `admin`     | Admins manage accounts and libraries                               |
| `createdAt` | Unix seconds                                                       |
| password    | Optional. Stored only as PBKDF2-HMAC-SHA256: 16-byte salt, 600,000 iterations, 32-byte key. The iteration count is stored with the hash. |

### Owner

- Account **1** is the **owner**. It is created on first start with the name
  `owner` and no password.
- The owner is always an admin and cannot be deleted or demoted. Because of
  this, the server always has at least one admin.

### Token

A token belongs to one account. It has:

- an `id`;
- a `device` label;
- `createdAt`.

Only the SHA-256 of the token value is stored. The value is shown once, when
the token is issued.

### Owner master tokens

These tokens always authenticate as the owner:

- the token in `<dataDir>/identity.json`;
- every token listed in the config under `tokens`.

They cannot be revoked through the API. The identity token is the recovery
path if all passwords are lost.

### Storage

Accounts and tokens are stored in `<dataDir>/accounts.json` with mode `0600`.
Each account's play state is kept apart from every other account's; see
[playstate.md](playstate.md).

## Permissions

| Action                                              | Who            |
|-----------------------------------------------------|----------------|
| Browse, stream, view images, change own play state  | any account    |
| Manage own password and tokens                      | any account    |
| Refresh library sections                            | admins         |
| List, create, update, or delete accounts            | admins         |
| Issue or revoke another account's tokens            | admins         |

A forbidden action returns `403 Forbidden`.

## JSON API

All bodies are JSON (`Content-Type: application/json`), limited to 64 KiB.
Errors return `{"error": "<message>"}` with these statuses:

| Status | Meaning                    |
|--------|----------------------------|
| 400    | Invalid input              |
| 401    | Not authenticated          |
| 403    | Not allowed                |
| 404    | Unknown account or token   |
| 409    | Name already taken         |
| 429    | Too many failed logins     |

An account object looks like:

```json
{"id": 2, "name": "alice", "admin": false, "hasPassword": true, "createdAt": 1700000000}
```

A token object looks like this. The `token` value appears only when the token
is issued:

```json
{"id": 5, "device": "Living Room TV", "createdAt": 1700000000, "token": "…"}
```

### `POST /sloplex/auth/login` (no auth)

Request:

```json
{"username": "alice", "password": "…", "device": "Phone"}
```

`device` is optional. It defaults to the `X-Plex-Device-Name` header, then to
`X-Plex-Product`, then to `unknown`.

- Success returns `200` with `{"account": {…}, "token": {…}}`. A new token is
  issued.
- A wrong name or password returns `401`.
- Accounts without a password cannot log in.
- After 10 failed logins from one remote address within 15 minutes, further
  attempts from that address get `429` with `Retry-After` until the window
  passes.
- Response time does not depend on whether the account exists.

### `POST /sloplex/auth/logout`

Revokes the token used for the request and returns `204`.

The request is refused with `400` when it was authenticated by:

- an owner master token;
- a trusted network, with no token.

### `GET /sloplex/account`

Returns the caller's account.

### `PUT /sloplex/account/password`

Request:

```json
{"current": "…", "new": "…"}
```

- `current` is required if the account already has a password.
- `new` must be at least 8 characters.
- Changing the password revokes all of the account's tokens **except** the
  one used for the request.
- Returns `204`.

### `GET /sloplex/tokens`

Lists the caller's tokens. The response is `{"tokens": [...]}`.

### `DELETE /sloplex/tokens/{id}`

Revokes one of the caller's tokens and returns `204`. Admins may revoke any
account's token.

### `GET /sloplex/accounts` (admin)

Returns `{"accounts": [...]}`, ordered by ID.

### `POST /sloplex/accounts` (admin)

Request:

```json
{"name": "bob", "password": "…", "admin": false}
```

- `password` is optional. If given, it must be at least 8 characters.
- Returns `201` with the account.

### `PATCH /sloplex/accounts/{id}` (admin)

Request: any of `{"name": "…", "password": "…", "admin": true}`.

- Setting `password` revokes all of that account's tokens.
- Changing `admin` on the owner returns `400`.
- Returns the updated account.

### `DELETE /sloplex/accounts/{id}` (admin)

Deletes the account, its tokens, and its play state, then returns `204`.
Deleting the owner returns `400`.

### `GET /sloplex/accounts/{id}/tokens` (admin)

Lists that account's tokens.

### `POST /sloplex/accounts/{id}/tokens` (admin)

Request:

```json
{"device": "Kids Tablet"}
```

Issues a token for that account without needing its password. This is meant
for setting up devices. Returns `201` with the token, including its value.

## Compatibility endpoint

### `GET /accounts`

Returns a `MediaContainer` of `Account` elements, each with:

- `id`;
- `key` (`/accounts/{id}`);
- `name`.

Admins see every account. Other accounts see only themselves.
