# Artwork

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the attribute names `thumb`, `art`, `banner`, `parentThumb`,
    `grandparentThumb`, and `grandparentArt`;
  - the image URL shape `/library/metadata/{id}/{kind}/{timestamp}`;
  - the endpoint `/photo/:/transcode` and its `url`, `width`, `height`,
    `minSize`, and `upscale` parameters.
- The local artwork file names (`poster.jpg`, `fanart.jpg`, `banner.jpg`,
  `seasonNN-poster.jpg`, `<video name>-thumb.jpg`, and so on) follow publicly
  documented "local media assets" naming conventions.
- Lookup order, fallbacks, generated images, the resize algorithm, and
  caching are **(sloplex)**.

## Kinds

| Kind     | Meaning                              | Items that may have it       |
|----------|--------------------------------------|------------------------------|
| `thumb`  | Poster, or still image for episodes  | all                          |
| `art`    | Background, fan art                  | movies, shows                |
| `banner` | Wide banner                          | shows                        |

## Local artwork discovery

Artwork is re-detected on every scan, so adding or replacing an image takes
effect after the next refresh.

- Image files are `.jpg`, `.jpeg`, or `.png`. Names are matched without regard
  to case.
- In each list below, the first existing name wins.
- `<stem>` is the video file name without its extension.

### Movies

Movies use the first file of their first version.

| Kind    | Candidates                                                          |
|---------|---------------------------------------------------------------------|
| `thumb` | `<stem>-poster`, `<stem>`, then folder-wide: `poster`, `folder`, `cover`, `movie` |
| `art`   | `<stem>-fanart`, then folder-wide: `fanart`, `background`, `art`, `backdrop` |

Folder-wide names only apply when the movie's folder is not a library root.
Otherwise one `poster.jpg` would match every movie stored directly in the
root.

### Shows

The show folder is decided from the path of the show's first episode,
relative to its library root:

- If the episode is under a non-season folder, the show folder is that
  top-level folder.
- If the episode is under a season folder directly beneath the root, the show
  folder is the root itself. This covers a library pointed at a single show.
- If the episode sits directly in the root, there is no show folder and the
  show gets no local artwork.

| Kind     | Candidates in the show folder                     |
|----------|---------------------------------------------------|
| `thumb`  | `poster`, `folder`, `show`, `cover`               |
| `art`    | `fanart`, `background`, `art`, `backdrop`         |
| `banner` | `banner`                                          |

### Seasons

Season number `N` is used two-digit padded (`NN`) and unpadded.

`thumb` candidates are checked in this order:

1. In the show folder:
   - `seasonNN-poster`, `seasonNN`, `seasonN-poster`, `seasonN`;
   - for season 0, also `season-specials-poster` and `season-specials`.
2. Then, if the season's episodes sit in a season folder: `poster`,
   `folder`, `cover`, or `season` in that folder.

### Episodes

| Kind    | Candidates next to the video file                    |
|---------|------------------------------------------------------|
| `thumb` | `<stem>-thumb`, `<stem>`                             |

## Generated images (sloplex)

When `ffmpeg` is available, items without a local image get one made from
their first video file:

| Item    | Kind    | Generated image                                         |
|---------|---------|---------------------------------------------------------|
| episode | `thumb` | A frame at 10% of the duration, 640 px wide             |
| movie   | `thumb` | A 600×900 poster: a frame at 20% of the duration, fitted inside it and centred over a blurred, darkened copy that fills it |
| movie   | `art`   | A frame at 33% of the duration, 1280 px wide            |

Frames:

- For movies, the frame is the most representative of 24 frames from that
  point, using ffmpeg's `thumbnail` filter. This avoids landing on a single
  transition frame.
- If the duration is unknown, the point is 5 seconds in.
- If no frame can be read there, as with very short files, the start is
  used.
- Non-square pixels (anamorphic video) are corrected first.
- HDR video (PQ or HLG) is tone mapped to SDR the same way as CPU
  transcodes; see "HDR tone mapping" in [transcode.md](transcode.md).

Generation and storage:

- Images are JPEG, stored under `<dataDir>/cache/thumbs/` (episodes),
  `posters/`, and `backgrounds/`. The cache key includes the part ID and the
  file's modification time.
- Generation happens on the first request, never during a scan.
- At most half as many images as there are CPU cores, and at least one, are
  generated at once. Other requests wait their turn.
- A local image always wins. Adding `poster.jpg` replaces a generated
  poster at the next scan.

## Attributes on items

Each attribute is a URL path, omitted when there is no image:

```
/library/metadata/{id}/{kind}/{timestamp}
```

- `{timestamp}` is the image file's modification time, or the video's for a
  generated image.
- `{id}` may be an ancestor's ID when the attribute falls back to an
  ancestor's image.

| Item    | Attribute          | Source                                         |
|---------|--------------------|------------------------------------------------|
| movie   | `thumb`, `art`     | own local image, else a generated one          |
| show    | `thumb`, `art`, `banner` | own                                      |
| season  | `thumb`            | own, else the show's `thumb`                   |
| season  | `art`              | the show's `art`                               |
| season  | `parentThumb`      | the show's `thumb`                             |
| episode | `thumb`            | own local image, else a generated one          |
| episode | `art`, `grandparentArt` | the show's `art`                          |
| episode | `parentThumb`      | the season's `thumb`, with its fallback        |
| episode | `grandparentThumb` | the show's `thumb`                             |

## Endpoints

### `GET /library/metadata/{id}/{kind}[/{timestamp}]`

`{kind}` is `thumb`, `art`, or `banner`. `{timestamp}` is ignored and exists
only to bust caches.

The endpoint returns the item's own image of that kind. It never returns an
ancestor's image; attributes already point to the ancestor's URL.

- Episode `thumb`, and movie `thumb` and `art`, fall back to generated
  images.
- An unknown item, or an item without that image, returns 404.
- `Content-Type` comes from the file extension.
- `Cache-Control: max-age=86400`.

### `GET /photo/:/transcode`

Returns a resized JPEG of a server image. Parameters:

| Name      | Meaning                                                      |
|-----------|--------------------------------------------------------------|
| `url`     | Required. An image URL as above, absolute or relative. Only its path is used. |
| `width`   | Required. Target box width, 1 to 4096.                       |
| `height`  | Required. Target box height, 1 to 4096.                      |
| `minSize` | `1`: cover the box, then centre-crop to it. Otherwise fit inside it. |
| `upscale` | `1`: allow enlarging. Otherwise images are never enlarged.   |

Rules:

- The server **never fetches remote URLs**. A `url` whose path is not
  `/library/metadata/{id}/{kind}[/{timestamp}]` returns 400.
- An image that cannot be resolved returns 404.
- Sources larger than 50 megapixels return 422 **(sloplex)**.
- Output is JPEG at quality 85, resampled with an area-averaging filter
  **(sloplex)**.
- Results are cached under `<dataDir>/cache/photo/`. The cache key covers the
  source path, the source modification time, and the parameters.
