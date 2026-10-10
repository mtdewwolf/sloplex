# Local metadata (.nfo files)

## Provenance

- The `.nfo` file format comes from Kodi's public wiki documentation of NFO
  files:
  - the file names `<video>.nfo`, `movie.nfo`, and `tvshow.nfo`;
  - the root elements `movie`, `tvshow`, and `episodedetails`;
  - the child elements listed below.
- These attribute and element names come from publicly available community
  documentation of the media-server HTTP API:
  - `summary`, `tagline`, `contentRating`, `rating`, `studio`,
    `originalTitle`, `titleSort`, `originallyAvailableAt`;
  - the `Genre`, `Director`, `Writer`, `Country`, `Role`, and `Collection`
    tag elements.
- These are **(sloplex)**:
  - which files win when several apply;
  - how ratings are scaled;
  - the handling of broken files.

Sloplex never fetches metadata from the internet. Everything below comes
from files next to the media.

## Which file applies

| Item    | Files tried, first found wins |
|---------|-------------------------------|
| Movie   | `<video name>.nfo` next to the video, then `movie.nfo` in the same folder |
| Show    | `tvshow.nfo` in the episode's folder, then in each parent folder up to the section root |
| Episode | `<video name>.nfo` next to the video |

- `<video name>` is the video's file name without its extension.
- File names are matched without regard to case.
- An item with several versions takes the first version, by path, that has a
  `.nfo`.
- Seasons have no `.nfo`.

## Format

A `.nfo` file is XML whose root element is `movie`, `tvshow`, or
`episodedetails`, matching the item.

- A root element of another kind, a file that is not XML, and a file that
  holds only a URL are ignored, with a debug log line.
- Text after the root element, often a URL, is ignored.
- The XML may declare UTF-8, US-ASCII, or ISO-8859-1. Other encodings are
  ignored.
- Empty elements count as missing.
- Values are trimmed.

## Fields

| `.nfo` element | Item attribute | Notes |
|---|---|---|
| `title` | `title` | Replaces the title taken from the file or folder name |
| `originaltitle` | `originalTitle` | Emitted only when it differs from `title` |
| `sorttitle` | `titleSort` | Used for sorting instead of the title without its leading article |
| `year` | `year` | Movies and shows only. Replaces the year from the name |
| `premiered`, or `aired` for episodes | `originallyAvailableAt` | `YYYY-MM-DD`; anything else is ignored. For movies and shows without a `year`, the year is taken from it |
| `plot`, else `outline` | `summary` | |
| `tagline` | `tagline` | Movies |
| `mpaa` | `contentRating` | For example `PG-13`; a `Rated ` prefix is removed |
| `ratings`/`rating`, or `rating` | `rating` | See **Ratings** |
| `studio` | `studio` | The first one |
| `genre` | `Genre` | Repeated elements; a value containing ` / ` is split there |
| `director` | `Director` | Repeated |
| `credits` | `Writer` | Repeated |
| `country` | `Country` | Repeated |
| `actor` | `Role` | `name` becomes `tag` and `role` becomes `role`, in `order` (then file) order. Actors without a name are skipped |
| `set`, or `set/name` | `Collection` | Movies: the name of the movie's set |

- **(sloplex)** Each list keeps at most 50 values, without duplicates
  (compared without regard to case).
- For an episode, `season` and `episode` in the file are ignored. Numbering
  comes from the file name, so the `.nfo` cannot move an episode.
- Actor thumbnails and other image URLs are ignored, because Sloplex never
  fetches from the internet. Artwork comes from local files; see
  [artwork.md](artwork.md).

### Ratings

- `<ratings>` holds `<rating name=… max=… default=…><value>…</value></rating>`.
  The rating marked `default="true"` is used, else the first one.
- `max` defaults to 10. The value is scaled to 0–10 and rounded to one
  decimal place.
- A plain `<rating>7.4</rating>` is read on a 0–10 scale.
- Values that are not numbers, are negative, or are above `max` are ignored.

## Output

The fields appear on items in every response that lists them:

```xml
<Video ratingKey="12" type="movie" title="Heat" year="1995"
       summary="A group of professional bank robbers…" tagline="A Los Angeles crime saga"
       contentRating="R" rating="8.3" studio="Warner Bros." originallyAvailableAt="1995-12-15">
  <Media …/>
  <Genre tag="Crime"/>
  <Genre tag="Thriller"/>
  <Director tag="Michael Mann"/>
  <Writer tag="Michael Mann"/>
  <Country tag="United States of America"/>
  <Role tag="Al Pacino" role="Lt. Vincent Hanna"/>
  <Collection tag="Michael Mann Films"/>
</Video>
```

In JSON these are the arrays `Genre`, `Director`, `Writer`, `Country`,
`Role`, and `Collection`, made of `{"tag": …}` objects; `Role` objects also
have `role`. **(sloplex)** `Role` is emitted only by
`GET /library/metadata/{id}`, to keep lists small.

## Scanning

- **(sloplex)** `.nfo` files are read on every scan, so editing one takes
  effect at the next scan. With watching on (see "Watching" in
  [http-api.md](http-api.md)), saving the file triggers that scan.
- A changed `.nfo` updates the item's `updatedAt`, like a changed video.
- Search also matches the original title.
