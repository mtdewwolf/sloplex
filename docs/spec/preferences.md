# Server preferences

## Provenance

- **S1** — Plex Media Server API reference, official OpenAPI document,
  `info.version` **1.2.3** ("Supported in PMS >= 1.43.4" in its own
  changelog), Apache-2.0, <https://developer.plex.tv/pms/>. Copy retrieved
  2026-10-11. From it:
  - the paths and methods `GET /:/prefs`, `PUT /:/prefs` and
    `GET /:/prefs/get` (operation IDs `preferencesGetSlash`,
    `preferencesPutSlash`, `preferencesGetGet`);
  - the response schema `mediaContainerWithSettings`: a `MediaContainer`
    with a `Setting` array whose entries have `id`, `label`, `summary`,
    `type` (`bool`, `int`, `text` or `double`), `default`, `value`,
    `hidden`, `advanced`, `group` and `enumValues` (`|`-separated
    `value:name` entries, present "if restricted");
  - `PUT /:/prefs` takes the preferences as query parameters named by
    preference ID (the `prefs` query object, example
    `FriendlyName=My Server&sendCrashReports=1&ScheduledLibraryUpdateInterval=3600`),
    answers `200` with an empty body, `400` "Attempt to set a preferences
    that doesn't exist" with the example text
    `cannot set preference value for unknown preference foo`, and `403`;
  - `GET /:/prefs/get?id=` returns the single setting, or `404` when no
    preference has that name;
  - the IDs `FriendlyName` (`text`, `group="general"`, not hidden, not
    advanced, `default=""`, summary: a blank name uses the computer's
    name) and `ScheduledLibraryUpdateInterval` (`int`, `group="library"`,
    not hidden, not advanced, enumValues
    `900:every 15 minutes|1800:every 30 minutes|3600:hourly|7200:every 2 hours|21600:every 6 hours|43200:every 12 hours|86400:daily`);
  - all `X-Plex-*` headers may also be sent as query parameters, and
    JSON is returned only when `Accept: application/json` is sent.
- **S2** — Plex support article "Advanced, Hidden Server Settings",
  <https://support.plex.tv/articles/201105343-advanced-hidden-server-settings/>,
  page `dateModified` 2026-09-30, retrieved 2026-10-11. It lists the
  preference IDs and value kinds `FSEventLibraryUpdatesEnabled` (1/0),
  `GdmEnabled` (1/0), `logDebug` (1/0), `ScheduledLibraryUpdatesEnabled`
  (1/0) and `ScheduledLibraryUpdateInterval` (integer seconds, "requires
  ScheduledLibraryUpdatesEnabled"), and shows boolean preferences stored
  as `1`/`0`. Its last column holds examples, not defaults.
- **S3** — Plex support articles "General"
  (<https://support.plex.tv/articles/200289496-general/>), "Library"
  (<https://support.plex.tv/articles/200289526-library/>) and "Network"
  (<https://support.plex.tv/articles/200430283-network/>), retrieved
  2026-10-11. They place debug logging under General's advanced
  settings, automatic and periodic scans in Library's basic settings, and
  GDM discovery in Network's basic settings. Labels below paraphrase
  these pages.
- Everything else is **(sloplex)**: defaults (they come from Sloplex's own
  configuration, not from PMS), validation limits, authorization, XML value
  encoding, persistence, restart handling and the choice of supported IDs.
  Nothing here claims verified compatibility with a live PMS or any
  particular client.

## Model (sloplex)

The server keeps a typed registry. Each entry has an ID, type, label,
summary, group, `hidden` and `advanced` flags, optional choices, a
validation rule, a consumer, and whether a change applies at runtime or
after a restart.

Only entries with a real consumer are in the registry. An ID that is not
in the registry is unknown: it is never listed and can never be set.
Secrets, tokens, file paths, network allowances, library definitions and
other configuration are not preferences and are never returned.

### Precedence

1. A value stored through `PUT /:/prefs` (in `<dataDir>/preferences.json`).
2. Otherwise the value derived from the config file
   (see [README](../../README.md#configuration)) and command line.
3. Otherwise Sloplex's built-in configuration default.

The `default` attribute reports what 2–3 give; `value` reports 1 if set,
else `default`. A stored value stays stored even when it equals the
default; editing the config file then has no effect on that ID.

### Supported preferences

| ID | Type | Default (sloplex) | Choices / validation | Consumer | Applies |
|---|---|---|---|---|---|
| `FriendlyName` | `text` | `""` (S1) | 0–255 bytes of UTF-8 without control characters | Server name in `GET /` `friendlyName`, GDM search responses and `HELLO` announcements, startup log | Immediately |
| `ScheduledLibraryUpdatesEnabled` | `bool` | `true` unless config `scanInterval` is `"0"` | `1`, `0`, `true`, `false` | Interval scans of every section | Immediately |
| `ScheduledLibraryUpdateInterval` | `int` | config `scanInterval` in whole seconds; `3600` when it is `"0"` | One of S1's enumValues, or the default | Interval between scans | Immediately |
| `FSEventLibraryUpdatesEnabled` | `bool` | config `watch` | `1`, `0`, `true`, `false` | Folder watching (see "Watching" in [http-api.md](http-api.md)) | Next start |
| `GdmEnabled` | `bool` | config `discovery` | `1`, `0`, `true`, `false` | GDM listener and announcements ([discovery.md](discovery.md)) | Next start |
| `logDebug` | `bool` | `true` with the `-debug` flag, else `false` | `1`, `0`, `true`, `false` | Server log level: debug or info | Immediately |

Display attributes:

| ID | `group` | `hidden` | `advanced` | Source |
|---|---|---|---|---|
| `FriendlyName` | `general` | false | false | S1 |
| `ScheduledLibraryUpdatesEnabled` | `library` | false | false | S3 placement; group name from S1 |
| `ScheduledLibraryUpdateInterval` | `library` | false | false | S1 |
| `FSEventLibraryUpdatesEnabled` | `library` | false | false | S3 placement |
| `GdmEnabled` | `network` | false | false | S3 placement; lower-case group name is (sloplex) |
| `logDebug` | `general` | false | true | S3 placement |

`FriendlyName` semantics follow S1: a blank value means "use the computer's
name". In Sloplex that name is the config file's `friendlyName`, whose own
default is the host name. The effective name is therefore the stored
`FriendlyName` if it is non-empty, else config `friendlyName`.

A config `scanInterval` outside S1's choices (for example `"45m"`) is
reported as its number of whole seconds. That one value is also accepted
on `PUT`, so an unchanged value read with `GET` can be written back.

Interval scans start one interval after startup, or after the effective
interval last changed (turning periodic scans on or off, or a new
interval), then repeat. Writing the same values again does not restart the
schedule. Disabling interval scans stops the schedule; a scan already
running is not interrupted. Startup scans, watching and refresh requests
are unaffected.

`ScheduledLibraryUpdateInterval` written with a value outside S1's choices
(only possible for the config-derived default) is not stored: the
preference keeps following the config file.

Restart-required preferences are stored and returned as set, take effect
at the next start, and the server logs that a restart is needed.

## `GET /:/prefs`

Admin only, like other server administration: requests without
credentials get `401`, non-admin accounts `403` (sloplex). Returns a `MediaContainer` with `size`
and one `Setting` per registry entry, in the order of the table above.

JSON (S1 shape):

```json
{"MediaContainer": {"size": 6, "Setting": [
  {"id": "FriendlyName", "label": "Friendly name", "summary": "…",
   "type": "text", "default": "", "value": "Den",
   "hidden": false, "advanced": false, "group": "general"},
  {"id": "ScheduledLibraryUpdateInterval", "label": "Library scan interval",
   "summary": "…", "type": "int", "default": 3600, "value": 900,
   "hidden": false, "advanced": false, "group": "library",
   "enumValues": "900:every 15 minutes|…|86400:daily"}
]}}
```

`default` and `value` are JSON booleans for `bool`, numbers for `int`, and
strings for `text`. `enumValues` is omitted when a preference has no
choices.

XML **(sloplex)** uses the same attribute names on `<Setting>` elements
inside `<MediaContainer size="…">`. `default` and `value` use `1`/`0` for
booleans (as in S1's PUT example and S2), decimal for integers and the
string itself for text. `hidden` and `advanced` use `1`/`0`, like other
Sloplex boolean attributes.

## `GET /:/prefs/get?id={id}`

Admin only. Returns the same container with the single matching `Setting`.
An unknown or missing `id` gets `404`.

## `PUT /:/prefs`

Admin only. Every query parameter is a preference ID and its new value,
except parameters whose names start with `X-Plex-`, which carry client
headers and are ignored here. The request body is not read.

- **(sloplex)** A malformed query (including invalid percent escapes or
  unescaped semicolon separators) gets `400` before any update. Valid
  parameters from a partly malformed query MUST NOT be applied.
- All values are validated before anything changes. If any parameter is
  unknown, repeated, or invalid for its type or choices, the response is
  `400` with a plain-text reason and **no** preference changes.
  - Unknown ID: `cannot set preference value for unknown preference {id}`
    (S1 wording).
  - No preference parameters: `400`.
- `bool` accepts `1`, `0`, `true` or `false`. `int` accepts a base-10
  integer. `text` accepts the string as sent.
- All values are written to `<dataDir>/preferences.json` in one file
  replacement, then become visible together to readers and consumers. A
  failure before replacement returns `500` and leaves values unchanged.
  If replacement succeeds but directory durability cannot be confirmed,
  all new values become visible and consumers are notified, while the
  response is `500`. This outcome does not imply rollback; callers can read
  the current values to resolve what was installed. The limits in
  [storage-durability.md](storage-durability.md) apply.
- Success is `200` with an empty body.
- Concurrent updates are serialized; each request's values are published
  together. Validation and failures before replacement publish none.
- On a successful response, the server logs the changed IDs and the account
  that changed them, not the values, and logs each changed restart-required ID.

## Storage (sloplex)

`<dataDir>/preferences.json`, mode `0600`:

```json
{"version": 1, "values": {"FriendlyName": "Den", "ScheduledLibraryUpdateInterval": 900}}
```

- Only stored values are written. Values keep their JSON type.
- The store returns the original persistence error. An installed replacement
  whose directory durability is unconfirmed returns an inspectable
  `*atomicfile.CommittedError`, including through error wrapping, and keeps
  memory aligned with the installed file. Subscribers receive the usual
  coalesced notification and must reread current values. A validation error
  or failure before replacement changes neither memory nor the file and
  sends no notification.
- At startup an unreadable or malformed file, an unsupported `version`, or
  a stored value that is invalid for its registry entry stops the server
  with an error naming the file and ID. It is never silently reset.
- IDs not in this version's registry are kept in the file untouched, so a
  downgrade followed by an upgrade does not lose them, but they are not
  listed or applied.

## Not implemented

Section preferences (`/library/sections/{id}/prefs`,
`/library/sections/prefs`), item preferences
(`/library/metadata/{ids}/prefs`), DVR and device preferences, and every
other PMS preference ID are not implemented. Their requests get the
existing `404`/`405` behavior. Preferences that need a new feature
(for example partial scans, trash, log verbosity or file rotation,
transcoder quality) are added only together with that feature.
