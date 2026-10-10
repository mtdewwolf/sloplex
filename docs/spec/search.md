# Search

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the endpoint paths `/hubs/search`, `/search`, and
    `/library/sections/{id}/search`;
  - the `query`, `limit`, `sectionId`, and `type` parameters;
  - the `Hub` element and its `hubIdentifier`, `title`, `type`, `size`, and
    `more` attributes.
- Matching, folding, scoring, and ordering are **(sloplex)**.

## Matching (sloplex)

### Folding

Text is folded before it is compared:

1. Convert to lower case.
2. Replace common Latin accented letters with their base letters, for
   example `é`→`e`, `ñ`→`n`, `ß`→`ss`, `æ`→`ae`.
3. Turn every character that is not a letter or digit into a space.
4. Collapse repeated spaces.

The folded query is split into **tokens**. A query of more than 200
characters, or one that folds to nothing, returns `400`.

### Searchable text

| Item    | Title text     | Context text                        |
|---------|----------------|-------------------------------------|
| movie   | title          | –                                   |
| show    | title          | –                                   |
| season  | title          | show title                          |
| episode | title          | show title and season title         |

**(sloplex)** An item with an `originalTitle` (from a `.nfo` file; see
[metadata.md](metadata.md)) is also scored with that as its title text. It
keeps the higher of the two scores.

A token **matches** a word when it is a prefix of that word: `mat` matches
`matrix`. A token made only of digits also matches the item's `year`.

### Score

The first matching rule gives the score. An item scoring 0 is left out.

| Score | Rule                                                                  |
|------:|-----------------------------------------------------------------------|
| 100   | The folded title equals the folded query, with or without one leading article ("the", "a", "an") |
| 80    | The folded title starts with the folded query, with or without the article |
| 60    | Every token matches a word of the title (or the year)                 |
| 40    | Every token matches a word of the title, the context, or the year     |
| 0     | Otherwise                                                             |

### Order

Results are ordered by:

1. score, highest first;
2. `titleSort`;
3. ID.

### Access

Search only covers libraries the account may see; see "Library access" in
[accounts.md](accounts.md). Hidden items are never returned and never
counted.

## Endpoints

### `GET /hubs/search`

Searches grouped by type. Parameters:

| Name        | Meaning                                                    |
|-------------|------------------------------------------------------------|
| `query`     | Required                                                   |
| `limit`     | Maximum items per hub. Default 10, range 1–100             |
| `sectionId` | Optional. Search only that section; an unknown or hidden section returns `404` |

The response is a `MediaContainer` of `Hub` elements in this order:
`movie`, `show`, `episode`. Hubs with no results are left out.

| Hub attribute   | Meaning                                         |
|-----------------|-------------------------------------------------|
| `hubIdentifier` | `movie`, `show`, or `episode`                   |
| `type`          | The same as `hubIdentifier`                     |
| `title`         | `Movies`, `Shows`, or `Episodes`                |
| `size`          | The number of items in the hub                  |
| `more`          | `1` if results were cut off by `limit`          |

Each hub contains its items as usual: `Video` or `Directory` in XML,
`Metadata` in JSON.

### `GET /search`

Returns a flat list of results in the order above.

- `type` selects one item type, by number or name. Without it, movies, shows,
  and episodes are searched. Seasons are only searched with `type=3` or
  `type=season`.
- Supports paging.

### `GET /library/sections/{id}/search`

The same as `/search`, limited to one section. An unknown or hidden section
returns `404`.

Without `type`, a movie section searches movies, and a show section searches
shows and episodes. A `type` that the section cannot hold returns an empty
container.
