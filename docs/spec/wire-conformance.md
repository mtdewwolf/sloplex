# Wire conformance: documented request forms

This page adds the request forms that the official PMS API reference uses
for features Sloplex already has. Each one is an alias of an existing
Sloplex endpoint. The legacy forms on the feature pages keep working
unchanged. Everything here is a **documented-form** alignment. It is not a
claim that a real Plex client works with Sloplex: no client or live PMS
traffic was tested.

The machine-readable ledger for these operations is
[`docs/parity/wire-operations.json`](../parity/wire-operations.json).

## Provenance

- **S1**: *Plex Media Server* API reference, OpenAPI 3.1.0 document,
  `info.version` 1.2.3, which the document says is supported in PMS 1.43.4
  and later. Published at <https://developer.plex.tv/pms/> under the
  Apache 2.0 licence. A copy was retrieved on 2026-10-11 (SHA-256
  `e1ea0ead7722693de15e807a7d9e1e70885464dae47aa32a8f4ef1844a746138`) and
  read as data. Only operation paths, methods, parameter names, documented
  status codes and the shape of the timeline response were used. No schema
  or prose was copied into the repository. The operations used are listed
  by `operationId` below.
- S1 says it is not a complete description of PMS. Something missing from
  S1 is therefore not evidence that PMS lacks it, and S1 says nothing about
  what any particular client sends.
- Everything marked **(sloplex)** is our own design. That includes every
  status code that differs from S1 and every parameter that is not
  honoured.

| Operation | S1 `operationId` | Sloplex legacy form |
|---|---|---|
| `POST /:/timeline` | `timelinePostSlash` | `GET\|PUT /:/timeline` ([playstate.md](playstate.md)) |
| `POST /library/sections/{sectionId}/refresh` | `librarySectionPostRefresh` | `GET\|PUT /library/sections/{id}/refresh` ([http-api.md](http-api.md)) |
| `DELETE /library/sections/{sectionId}/refresh` | `librarySectionDeleteRefresh` | none: **not implemented** |
| `GET /library/collections/{collectionId}/items` | `libraryCollectionCollectionGetItems` | `GET /library/collections/{id}/children` ([collections.md](collections.md)) |
| `DELETE /library/sections/{sectionId}/collection/{collectionId}` | `librarySectionDeleteCollectionCollection` | `DELETE /library/collections/{id}` ([collections.md](collections.md)) |
| `GET /library/streams/{streamId}.{ext}` | `libraryGetStreamsStream` | `GET /library/streams/{id}` ([streams.md](streams.md)) |

Authentication and library access follow [auth.md](auth.md) and
[accounts.md](accounts.md), as for every other endpoint. Sloplex never
contacts plex.tv to check a token.

## `POST /:/timeline`

S1 documents `POST` as the method a client uses to report playback. It
lists these query parameters: `key`, `ratingKey`, `state` (`stopped`,
`buffering`, `playing` or `paused`), `playQueueItemID`, `time`,
`duration`, `continuing`, `updated`, `offline`, `timeToFirstFrame`,
`timeStalled`, `bandwidth`, `bufferedTime` and `bufferedSize`. It lists a
**required** `X-Plex-Client-Identifier` header and an optional
`X-Plex-Session-Identifier` header. It documents `200` with a
`MediaContainer` and `400` for a bad request.

Sloplex applies the request exactly as `GET|PUT /:/timeline` does
([playstate.md](playstate.md)). It identifies the item the same way, uses
the same `time` and `duration` handling and the same completion rule, and
saves the result to the requesting account's play state. The differences:

- **Client identifier.** `X-Plex-Client-Identifier` is required. A
  non-empty value in the header or, **(sloplex)**, in a query parameter of
  the same name is accepted. Without one the response is `400`. The legacy
  methods still do not require it.
- **State.** `state` is optional. If present it MUST be one of the four
  S1 values, otherwise `400`. As on the legacy methods, the value does not
  change how the position is applied.
- **Response.** On success the response is `200` with an empty
  `MediaContainer` (`size="0"`), as XML by default or JSON when the client
  asks for it ([http-api.md](http-api.md)). The legacy methods still return
  an empty body.
- **Errors.** Errors are plain text, as on the legacy methods.

| Problem | Response |
|---|---|
| No client identifier | `400` |
| `state` present but not a listed value | `400` |
| Item missing or not numeric | `400` |
| Item unknown, or in a library the account cannot see | `404` |
| Item is a show or season | `400` |
| `time` present but not an integer | `400` |
| Play state could not be saved | `500` |

**Not implemented.** The remaining S1 parameters are accepted, not
validated and not used: `playQueueItemID`, `continuing`, `updated`,
`offline`, `timeToFirstFrame`, `timeStalled`, `bandwidth`, `bufferedTime`,
`bufferedSize` and `X-Plex-Session-Identifier`. In particular an offline
sync's `updated` time is not applied: `lastViewedAt` is the time the
request arrives. Sloplex has no play queues, sessions dashboard,
bandwidth reporting or termination messages, so the response never carries
`terminationCode`, `terminationText` or `Bandwidths`.

## `POST /library/sections/{sectionId}/refresh`

S1 documents `POST` to start a refresh of a section, admin only, with
optional `force` (`0` or `1`) and `path` query parameters, and a `200`
response.

Sloplex treats it as `GET|PUT /library/sections/{id}/refresh`
([http-api.md](http-api.md)): admins only, `403` for other accounts,
`404` for an unknown section, an asynchronous scan, the same queueing, and
`200` with an empty body.

- **(sloplex)** `force`, if present, MUST be `0` or `1`, otherwise `400`.
  The legacy methods do not check it.
- **Not implemented: `force=1` or a nonempty `path`.** The POST form returns
  `501` without starting a scan. This is **(sloplex)** defensive behavior:
  silently performing an ordinary full scan would not satisfy the requested
  forced or path-limited operation. `force=0` and an omitted/empty path use
  the normal whole-section scan. Legacy methods retain their old behavior.

### `DELETE /library/sections/{sectionId}/refresh`: not implemented

S1 documents `DELETE` to cancel a section refresh. Sloplex does not have
it, and the method gets `405 Method Not Allowed`. A scan can be started by
the server itself, by the folder watcher, on an interval or by a request.
Cancelling only the scans that requests started would not be the
documented behaviour. Real cancellation needs support in the scanner. A
cancelled scan already leaves the catalog unchanged
([scan-safety.md](scan-safety.md)), so adding cancellation later is safe.

## `GET /library/collections/{collectionId}/items`

S1 documents this as the way to list a collection's items. It notes that
large collections need paging.

Sloplex serves it exactly as `GET /library/collections/{id}/children`
([collections.md](collections.md)). It uses the same order, paging with
`X-Plex-Container-Start` and `X-Plex-Container-Size`, the same container
attributes, XML or JSON, and the same library access rules. A collection
in a library the account cannot see is `404`.

## `DELETE /library/sections/{sectionId}/collection/{collectionId}`

S1 documents this, admin only, to delete a collection. The singular
`collection` in the path is as S1 gives it. It documents `200`, and `404`
for an unknown collection.

Sloplex deletes the collection as `DELETE /library/collections/{id}` does
([collections.md](collections.md)). Its items are not touched. The
response is `200` with an empty body.

| Problem | Response |
|---|---|
| Not an admin | `403` |
| Unknown section, or a section ID that is not numeric | `404` |
| Unknown collection, or one that is not numeric | `404` |
| **(sloplex)** The collection belongs to a different section | `404`; nothing is deleted |
| The collection is automatic (from `.nfo` sets) | `400` |

The section check means a request can never delete a collection of
another section by naming the wrong section.

## `GET /library/streams/{streamId}.{ext}`

S1 documents this for fetching a stream such as a sidecar subtitle. It
says `ext` is the stream's extension. It lists optional `encoding`,
`format` (the format to convert a text subtitle to) and
`autoAdjustSubtitle` query parameters. It documents `200`, `403` when the
media is not accessible, `404` when the stream does not exist or has no
data, and `501` when the stream is not a sidecar subtitle.

Sloplex serves the same text subtitles as `GET /library/streams/{id}`
([streams.md](streams.md)). That includes sidecars and the text
subtitles embedded in media files.

**Path.** The last path segment MUST be a stream ID, then `.`, then an
extension:

- The ID is decimal digits with no sign or leading zero.
- The extension is one of `srt`, `vtt`, `ass` or `ssa`, matched without
  regard to case.
- Anything else is `404`. That includes an empty ID or extension, more
  than one `.`, and other extensions such as `sub`, `idx` or `sup`.
  Sloplex cannot serve image-based subtitles as files.

**Output format (sloplex).** `format` wins if it is given. It is checked
as on the legacy route. Otherwise the extension picks the format: `srt`
gives SRT, `vtt` gives WebVTT, and `ass` or `ssa` give ASS. Converting to
a format other than the stream's own needs ffmpeg, as on the legacy route.
S1 only says the extension is the stream's own. Sloplex can serve any
text subtitle in any of these formats, so it uses the extension as the
requested format instead of rejecting a mismatch.

`offset` works as on the legacy route, and needs the output format to be
WebVTT. So `/library/streams/7.vtt?offset=2` works.

| Problem | Response |
|---|---|
| Malformed segment or unsupported extension | `404` |
| Unknown stream, or one in a library the account cannot see | `404` |
| **(sloplex)** The stream is not a subtitle, or is an image subtitle | `501` |
| `format` not `srt`, `vtt` or `ass`, or `offset` without WebVTT output | `400` |
| Conversion or extraction needs ffmpeg and it is missing | `503` |

A hidden library is `404`, not S1's `403`, so the response does not reveal
that the stream exists ([accounts.md](accounts.md)). The legacy route
keeps `400` for streams that are not text subtitles.

**Not implemented.** On the extension form, a nonempty `encoding` or
`autoAdjustSubtitle` other than omitted, empty or `0` returns `501` before
serving data. This is **(sloplex)** explicit rejection of unsupported
conversion/adjustment behavior. Sidecars are otherwise served as stored,
and converted files are UTF-8. Legacy extensionless behavior is unchanged.
`PUT` (stream offset) and `DELETE` on this path are not routed, and get
`405`. Sloplex has no stored per-stream offset and does not delete media
files.

## Other documented differences not addressed here

These remain open. Each needs its own spec work before code:

- `uri` on `/:/scrobble` and `/:/unscrobble`, and S1's required
  `identifier`. S1 points to a URI grammar it does not define for these
  parameters.
- `PUT /library/collections/{collectionId}/items/{itemId}` (S1's form for
  removing an item) and `.../move`.
- Transcoder `{transcodeType}` paths, protocols and client profiles
  ([transcode.md](transcode.md)).
- The `X-Plex-Pms-Api-Version` header. Sloplex ignores it and does not
  vary any response by API version.
