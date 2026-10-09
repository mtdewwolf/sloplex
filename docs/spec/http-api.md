# HTTP API

## Provenance

- These shapes come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the `MediaContainer` envelope;
  - the `Directory`, `Video`, `Media`, and `Part` element names;
  - the attribute names `ratingKey`, `key`, `librarySectionID`, `addedAt`,
    and so on;
  - the `X-Plex-*` header names;
  - the endpoint paths.
- Items marked **(sloplex)** are our own choices.

## Transport

- HTTP/1.1 on TCP port **32400** by default. The port is configurable with
  `listen`.
- All endpoints except `/identity` require authentication; see
  [auth.md](auth.md).
- CORS: every response carries `Access-Control-Allow-Origin: *`. `OPTIONS`
  preflights return `204` and allow the `X-Plex-*`, `Accept`,
  `Content-Type`, and `Range` request headers.

## Content negotiation

- The default response format is XML, `Content-Type: text/xml; charset=utf-8`,
  starting with an XML declaration.
- If the `Accept` header contains `application/json`, the response is JSON:
  `application/json`. The JSON body is an object with one key,
  `MediaContainer`.
- In XML, the attributes of the container and of each element map to JSON
  object fields with the same names. Child elements map to arrays keyed by
  element name.
- One exception: movie and episode items are `<Video>` elements in XML but sit
  under the `Metadata` array in JSON.
- Booleans are encoded as `"1"`/`"0"` in XML and as `true`/`false` in JSON.

## Paging

List endpoints accept these parameters, as either query parameters or request
headers:

| Name                     | Meaning                         |
|--------------------------|---------------------------------|
| `X-Plex-Container-Start` | Zero-based offset (default 0)   |
| `X-Plex-Container-Size`  | Maximum items (default: all)    |

A paged response sets these on the container:

| Attribute   | Meaning                          |
|-------------|----------------------------------|
| `offset`    | The applied offset               |
| `totalSize` | The number of items before paging |
| `size`      | The number of items returned     |

## Sorting

- `GET /library/sections/{id}/all` accepts `sort=<field>[:asc|:desc]`.
- `<field>` is one of `titleSort` (default), `title`, `year`, `addedAt`, or
  `updatedAt`. Unknown fields fall back to `titleSort`.
- `titleSort` is the title in lowercase with one leading article ("the", "a",
  "an") removed.

## Element reference

### MediaContainer

These attributes are always present or are added when they apply:

- `size`: the number of child elements.
- `totalSize` and `offset`: when the response is paged.
- `title1`, `librarySectionID`, `librarySectionTitle`, `viewGroup`: in
  library listings.
- `friendlyName`, `machineIdentifier`, `version`, `platform`,
  `platformVersion`, `updatedAt`: on the root and identity endpoints.

### Directory

The attributes are `key`, `title`, `type`, `agent`, `scanner`, `language`,
`uuid`, `refreshing` (bool), `createdAt`, `updatedAt`, and `scannedAt`.

Library sections have `<Location id="" path=""/>` children.

### Video (JSON: an entry of `Metadata`)

| Attribute             | Notes                                                    |
|-----------------------|----------------------------------------------------------|
| `ratingKey`           | String item ID                                           |
| `key`                 | `/library/metadata/{ratingKey}`                          |
| `guid`                | **(sloplex)** `sloplex://{type}/{ratingKey}`             |
| `type`                | `movie`                                                  |
| `title`, `titleSort`, `year` |                                                   |
| `duration`            | ms, taken from the first Media                           |
| `addedAt`, `updatedAt` | Unix seconds                                            |
| `librarySectionID`, `librarySectionKey`, `librarySectionTitle` |                 |

Children: one or more `Media`.

### Media

The attributes are `id`, `duration`, `bitrate` (kbps), `width`, `height`,
`videoCodec`, `audioCodec`, `audioChannels`, `container`, and
`videoResolution`.

`videoResolution` is one of `4k`, `1080`, `720`, `480`, or `sd`. It is derived
from the frame size:

- `4k` if width ≥ 3200 or height ≥ 1800;
- otherwise `1080` if width ≥ 1700 or height ≥ 1000;
- otherwise `720` if width ≥ 1100 or height ≥ 700;
- otherwise `480` if height ≥ 400;
- otherwise `sd`.

The attribute is empty when the frame size is unknown.

Children: one or more `Part`.

### Part

| Attribute   | Notes                                                    |
|-------------|----------------------------------------------------------|
| `id`        |                                                          |
| `key`       | `/library/parts/{id}/{modTime}/file.{ext}`               |
| `duration`  | ms                                                       |
| `file`      | Absolute path on the server                              |
| `size`      | Bytes                                                    |
| `container` |                                                          |

## Endpoints

### `GET /identity` (no auth)

Returns a `MediaContainer` with `size="0"`, `machineIdentifier`, and
`version`. It has no children.

### `GET /`

Returns the server root. The container carries `friendlyName`,
`machineIdentifier`, `version`, `platform`, `platformVersion`, and
`updatedAt`. Its children are Directories with key `library`.

### `GET /library`

Returns Directories with keys `sections` and `recentlyAdded`, which are
relative to `/library/`.

### `GET /library/sections`

Returns one Directory per library section:

- `key` is the section ID.
- `type` is `movie`.
- `agent` is `sloplex.local` **(sloplex)**.
- `scanner` is `Sloplex Movie Scanner` **(sloplex)**.
- `refreshing` is true while a scan is running.
- It has `Location` children.

### `GET /library/sections/{id}`

Returns the section's views as Directories with keys `all` and
`recentlyAdded`. Returns 404 if the section is unknown.

### `GET /library/sections/{id}/all`

Returns all items in the section as Video elements. Supports paging and
sorting. The container sets `librarySectionID`, `librarySectionTitle`,
`title1`, and `viewGroup` (the section type).

### `GET /library/sections/{id}/recentlyAdded`

Returns the section's items ordered by `addedAt`, newest first. Supports
paging. The default size is 50 **(sloplex)**.

### `GET|PUT /library/sections/{id}/refresh`

Starts an asynchronous rescan of the section and returns `200` with an empty
body.

- **(sloplex)** If a scan of that section is already running, this is a no-op
  that still returns `200`.
- Returns 404 if the section is unknown.

### `GET /library/recentlyAdded`

Same as the section variant, but across all sections.

### `GET /library/metadata/{ratingKey}`

Returns a container with exactly one Video. Returns 404 if the item is
unknown.

### `GET|HEAD /library/parts/{id}/{ts}/{filename}`

Streams the original file bytes ("direct play").

- `{ts}` and `{filename}` are ignored. The file comes only from the stored
  part ID, never from the URL path.
- Supports `Range` requests: `206` responses and `Accept-Ranges: bytes`.
- `Content-Type` is chosen from the file extension.
- `?download=1` adds `Content-Disposition: attachment`.
- Returns 404 if the part is unknown or its file is missing.

## Scanning (movie sections)

**(sloplex)**

- The scanner walks every configured path of the section.
- Video files are recognised by extension: `mkv`, `mp4`, `m4v`, `avi`, `mov`,
  `wmv`, `ts`, `m2ts`, `webm`, `mpg`, `mpeg`, `flv`.
- It skips:
  - hidden files and directories;
  - files whose name contains the word `sample`;
  - files with an extras suffix (`-trailer`, `-featurette`,
    `-behindthescenes`, `-deleted`, `-interview`, `-scene`, `-short`,
    `-other`);
  - extras directories (`Extras`, `Featurettes`, `Trailers`,
    `Behind The Scenes`, `Deleted Scenes`, `Interviews`, `Scenes`, `Shorts`,
    `Other`, `Samples`).
- Title and year are parsed from the file name. Examples:
  - `Title (Year)`;
  - `Title.Year.1080p...`;
  - a parent directory named `Title (Year)` when the file name has no year.
- Files with the same title and year in one section become one item with
  several Media entries (versions).
- IDs (`ratingKey`, media `id`, part `id`) are kept across rescans for
  unchanged file paths.
- Files are probed with `ffprobe` when they are new, or when their size or
  modification time has changed.
- If a configured root path is missing or unreadable, the scan aborts and the
  section's existing items stay as they are. This keeps an unmounted drive
  from wiping the library.
