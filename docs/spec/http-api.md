# HTTP API

## Provenance

- These shapes come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the `MediaContainer` envelope;
  - the `Directory`, `Video`, `Media`, and `Part` element names;
  - the attribute names `ratingKey`, `key`, `librarySectionID`, `addedAt`,
    `parentRatingKey`, `grandparentTitle`, `index`, `leafCount`, and so on;
  - the numeric item types (1 movie, 2 show, 3 season, 4 episode);
  - the `X-Plex-*` header names;
  - the endpoint paths, including `/children` and `/allLeaves`.
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
- Exception: in XML, movie and episode items are `<Video>` elements, and show
  and season items are `<Directory>` elements. In JSON, all of them sit under
  the `Metadata` array.
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
- `<field>` is one of `titleSort`, `title`, `year`, `addedAt`, `updatedAt`, or
  `index`.
- The default is `titleSort` for movies and shows, and `index` for seasons and
  episodes. Unknown fields use the default.
- `titleSort` is the title in lowercase with one leading article ("the", "a",
  "an") removed.
- `index` orders hierarchically: by show `titleSort`, then season index, then
  episode index.

## Item hierarchy

| Type      | Number | XML element | Parent | Children             |
|-----------|--------|-------------|--------|----------------------|
| `movie`   | 1      | `Video`     | –      | –                    |
| `show`    | 2      | `Directory` | –      | seasons              |
| `season`  | 3      | `Directory` | show   | episodes             |
| `episode` | 4      | `Video`     | season | –                    |

Leaf items are movies and episodes. Only leaf items carry `Media`.

## Element reference

### MediaContainer

These attributes are always present or are added when they apply:

- `size`: the number of child elements.
- `totalSize` and `offset`: when the response is paged.
- `title1`, `title2`, `librarySectionID`, `librarySectionTitle`,
  `viewGroup`: in library listings.
- `friendlyName`, `machineIdentifier`, `version`, `platform`,
  `platformVersion`, `updatedAt`: on the root and identity endpoints.

### Directory

The attributes are `key`, `title`, `type`, `agent`, `scanner`, `language`,
`uuid`, `refreshing` (bool), `createdAt`, `updatedAt`, and `scannedAt`.

Library sections have `<Location id="" path=""/>` children.

### Item: Video or Directory (JSON: an entry of `Metadata`)

Attributes on all item types:

| Attribute              | Notes                                                    |
|------------------------|----------------------------------------------------------|
| `ratingKey`            | String item ID                                           |
| `key`                  | Leaves: `/library/metadata/{ratingKey}`. Shows and seasons: `/library/metadata/{ratingKey}/children` |
| `guid`                 | **(sloplex)** `sloplex://{type}/{ratingKey}`             |
| `type`                 | `movie`, `show`, `season`, or `episode`                  |
| `title`, `titleSort`   | `titleSort` is present only when it differs from the lowercased title |
| `year`                 | Movies and shows                                         |
| `addedAt`, `updatedAt` | Unix seconds                                             |
| `librarySectionID`, `librarySectionKey`, `librarySectionTitle` |                  |

Extra attributes by type:

| Type    | Attributes                                                         |
|---------|--------------------------------------------------------------------|
| movie   | `duration` (ms, from the first Media)                              |
| show    | `childCount` (seasons), `leafCount` (episodes)                     |
| season  | `index` (season number, 0 = specials), `leafCount`, `parentRatingKey`, `parentKey`, `parentTitle` |
| episode | `index` (episode number), `parentIndex` (season number), `duration`, `parentRatingKey`, `parentKey`, `parentTitle`, `grandparentRatingKey`, `grandparentKey`, `grandparentTitle` |

Notes:

- `parentKey` and `grandparentKey` are `/library/metadata/{id}`.
- `index` and `parentIndex` are always emitted for seasons and episodes, even
  when they are 0.
- A season's `title` is `Season N`, or `Specials` for index 0
  **(sloplex)**.

Play-state attributes (`viewCount`, `viewOffset`, `lastViewedAt`, and
`viewedLeafCount`) are described in [playstate.md](playstate.md).

Leaf children: one or more `Media`.

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
- `type` is `movie` or `show`.
- `agent` is `sloplex.local` **(sloplex)**.
- `scanner` is `Sloplex Movie Scanner` or `Sloplex TV Scanner`
  **(sloplex)**.
- `refreshing` is true while a scan is running.
- It has `Location` children.

### `GET /library/sections/{id}`

Returns the section's views as Directories with keys `all` and
`recentlyAdded`. Returns 404 if the section is unknown.

### `GET /library/sections/{id}/all`

Returns the section's top-level items: movies in a movie section, shows in a
show section. Supports paging and sorting.

- `type=<number|name>` selects another item type in the section. A show
  section accepts 2/`show`, 3/`season`, and 4/`episode`. A movie section
  accepts 1/`movie`. A type the section cannot hold returns an empty
  container.
- The container sets `librarySectionID`, `librarySectionTitle`, `title1`, and
  `viewGroup` (the listed item type).
- `unwatched=1` filters by play state; see [playstate.md](playstate.md).

### `GET /library/sections/{id}/recentlyAdded`

Returns the section's leaf items (movies or episodes) ordered by `addedAt`,
newest first. Supports paging. The default size is 50 **(sloplex)**.

### `GET|PUT /library/sections/{id}/refresh`

Starts an asynchronous rescan of the section and returns `200` with an empty
body.

- **(sloplex)** If a scan of that section is already running, this is a no-op
  that still returns `200`.
- Returns 404 if the section is unknown.

### `GET /library/recentlyAdded`

Same as the section variant, but across all sections.

### `GET /library/metadata/{ratingKey}`

Returns a container with exactly one item, of any type. Returns 404 if the
item is unknown.

### `GET /library/metadata/{ratingKey}/children`

Returns the item's direct children: a show's seasons or a season's episodes,
ordered by `index`. Supports paging.

- The container sets `title1` (the parent's title), `title2` (the item's
  title), `viewGroup` (the child type), and `librarySectionID`/`Title`.
- A leaf item returns an empty container. An unknown item returns 404.

### `GET /library/metadata/{ratingKey}/allLeaves`

Returns every episode under a show or a season, ordered by season index and
then episode index. Supports paging. A leaf item returns an empty container.
An unknown item returns 404.

### `GET|HEAD /library/parts/{id}/{ts}/{filename}`

Streams the original file bytes ("direct play").

- `{ts}` and `{filename}` are ignored. The file comes only from the stored
  part ID, never from the URL path.
- Supports `Range` requests: `206` responses and `Accept-Ranges: bytes`.
- `Content-Type` is chosen from the file extension.
- `?download=1` adds `Content-Disposition: attachment`.
- Returns 404 if the part is unknown or its file is missing.

## Scanning

**(sloplex)**

### Common rules

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
- IDs (`ratingKey`, media `id`, part `id`) are kept across rescans for
  unchanged file paths.
- Files are probed with `ffprobe` when they are new, or when their size or
  modification time has changed.
- If a configured root path is missing or unreadable, the scan aborts and the
  section's existing items stay as they are. This keeps an unmounted drive
  from wiping the library.

### Movie sections

- Title and year are parsed from the file name. Examples:
  - `Title (Year)`;
  - `Title.Year.1080p...`;
  - a parent directory named `Title (Year)` when the file name has no year.
- Files with the same title and year in one section become one item with
  several Media entries (versions).

### Show sections

The expected layout is `<root>/<Show Name (Year)>/<Season NN>/<file>`. The
season folder is optional.

Season and episode numbers come from the file name, tried in this order:

1. `S01E02`, case-insensitive, with optional separators between the parts.
   Multi-episode forms such as `S01E01E02` and `S01E01-E02` are accepted;
   only the first episode number is used.
2. `1x02`.
3. Only when the file sits in a season folder: an `E02`, `Ep02`, or
   `Episode 2` token, or a leading number such as `02 - Title`.

The folder rules are:

- A season folder is named `Season N`, `Series N`, or `SN`. A folder named
  `Specials` is season 0.
- The show title and year come from the top-level folder under the root,
  parsed like a movie name. If the file sits directly in the root, they come
  from the file name text before the episode marker.

Other rules:

- The episode title is the file name text after the episode marker, cut at
  the first release tag (`720p`, `WEB-DL`, and so on). It falls back to
  `Episode N`.
- Files with no recognisable episode number are skipped.
- Within a section, shows are matched by title and year. Seasons are matched
  by show and season number. Episodes are matched by show, season, and
  episode number.
- Files that resolve to the same episode become versions (several Media
  entries) of that episode.
- Show and season IDs are kept across rescans while their title, year, or
  index stays the same.
- Date-based episodes, such as `Show 2024-01-15`, are **not supported yet**.
