# Playlists

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and the public docs of open-source client
  libraries:
  - the endpoints `GET /playlists`, `POST /playlists` (with `type`,
    `title`, `smart`, and `uri`), `GET /playlists/{id}`,
    `GET /playlists/{id}/items`, `PUT /playlists/{id}/items`,
    `DELETE /playlists/{id}/items/{playlistItemID}`,
    `PUT /playlists/{id}/items/{playlistItemID}/move?after=`,
    `PUT /playlists/{id}` (with `title`), and `DELETE /playlists/{id}`;
  - the `Playlist` element and its `playlistType`, `smart`, `leafCount`,
    and `duration` attributes;
  - the `playlistItemID` attribute on items.
- These are **(sloplex)**:
  - playlists being private to their account;
  - how shows and seasons expand;
  - the handling of items that disappear;
  - limits and IDs.

## Model

- A playlist is an ordered list of movies and episodes. It belongs to one
  account and is visible only to that account.
- **(sloplex)** Admins cannot see other accounts' playlists either. A
  playlist's ID, used by any other account, gives `404`.
- Each entry has its own `playlistItemID`, so one item can appear more than
  once.
- Only `video` playlists exist, and smart playlists are not supported.
- **(sloplex)** At most 5000 entries per playlist, and 500 playlists per
  account.
- Playlist IDs and `playlistItemID`s are numbered separately from library
  items. They are unique across the server and never reused.
- Playlists are stored in `<dataDir>/playlists.json` and deleted with their
  account.

**Items that leave the library, or that the account can no longer see**
(see "Library access" in [accounts.md](accounts.md)), are left out of every
response and of `leafCount` and `duration`. **(sloplex)** Their entries
stay stored, so they come back if access is restored. An item deleted from
the library gets a new ID if its file returns, so its old entry stays
hidden.

## The playlist element

```xml
<Playlist ratingKey="7" key="/playlists/7/items" guid="sloplex://playlist/7" type="playlist"
          title="Friday Night" playlistType="video" smart="0" leafCount="3" duration="16200000"
          thumb="/library/metadata/12/thumb/1700000000" addedAt="…" updatedAt="…"/>
```

| Attribute | Meaning |
|---|---|
| `leafCount` | The entries shown, as described above |
| `duration` | Their total duration in milliseconds |
| `thumb` | **(sloplex)** The thumbnail of the first shown entry that has one. For an episode it is the episode's own image |

In JSON, playlists are objects in `Metadata`.

## Endpoints

All of these work for any signed-in account, on its own playlists only.

| Request | Response |
|---|---|
| `GET /playlists` | The account's playlists, ordered by title (ignoring a leading article), then ID. `playlistType=video` is accepted; any other `playlistType` gives an empty list |
| `GET /playlists/{id}` | A container holding the playlist |
| `GET /playlists/{id}/items` | The entries in order, rendered as in lists, each with `playlistItemID`. The container's `title1` is the playlist title. Paging is supported |
| `POST /playlists?type=video&title=&uri=` | Creates a playlist; `uri` is optional. `smart=1` gives `400`. Returns `200` and a container holding it |
| `PUT /playlists/{id}/items?uri=` | Appends items. Returns `200` and the playlist |
| `DELETE /playlists/{id}/items/{playlistItemID}` | Removes one entry. Returns `200` and the playlist |
| `PUT /playlists/{id}/items/{playlistItemID}/move?after={playlistItemID}` | Moves an entry to just after another; without `after`, or with `after=0`, to the top. Returns `200` and the playlist |
| `PUT /playlists/{id}?title=` | Renames the playlist. Returns `200` and the playlist |
| `DELETE /playlists/{id}` | Deletes the playlist and returns `200` |

**Items** are given as `uri` in the form used by collections (see
[collections.md](collections.md)), or as `ids`. **(sloplex)** Items expand
like this:

- a movie or episode is added as itself;
- a season adds its episodes in order;
- a show adds all its episodes, season by season, specials (season 0)
  last.

| Problem | Response |
|---|---|
| Missing or blank `title`, or over 200 characters | `400` |
| `type` other than `video`, or `smart=1` | `400` |
| An item that is unknown, hidden from the account, or not a movie, show, season, or episode | `400` |
| More than 5000 entries, or more than 500 playlists | `400` |
| A playlist or `playlistItemID` that is unknown or belongs to another account | `404` |
| An `after` entry not in this playlist | `400` |
