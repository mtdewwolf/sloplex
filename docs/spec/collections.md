# Collections

## Provenance

- These come from publicly available community documentation of the
  media-server HTTP API and the public docs of open-source client
  libraries:
  - the endpoints `GET /library/sections/{id}/collections`,
    `GET /library/collections/{id}/children`, `POST /library/collections`
    (with `title`, `sectionId`, `type`, and `uri`),
    `PUT /library/collections/{id}/items`,
    `DELETE /library/collections/{id}/items/{itemId}`, and
    `DELETE /library/collections/{id}`;
  - the `uri` form
    `server://<machineIdentifier>/com.plexapp.plugins.library/library/metadata/<ids>`;
  - the `Directory` attributes `type="collection"`, `subtype`, and
    `childCount`;
  - the `Collection` tag element on items.
- These are **(sloplex)**:
  - collections built from `.nfo` sets, and their being read-only;
  - the `ids` parameter;
  - renaming with `PUT /library/collections/{id}?title=`;
  - the `collectionSource` attribute;
  - ordering, thumbnails, and limits.

## Model

A collection groups items of one library section: movies in a movie
section, or shows in a show section. There are two kinds:

- **Automatic** collections come from the `<set>` of movie `.nfo` files
  (see [metadata.md](metadata.md)).
  - There is one collection per set name. Names are compared without
    regard to case, and the collection is titled as the name was first
    seen.
  - They are rebuilt after every scan of the section. A collection whose
    name no longer appears is removed.
  - They cannot be changed through the API. Edit the `.nfo` files instead.
- **Manual** collections are created and edited by admins through the API.
  - Members are kept by item ID.
  - Members that leave the library are dropped at the next scan of the
    section.

Both kinds:

- **(sloplex)** Collection IDs come from the same counter as item IDs, so a
  collection's `ratingKey` never equals an item's.
- An automatic collection keeps its ID while its name stays the same,
  ignoring case.
- Collections are shared by every account that can see the section.

## The collection element

```xml
<Directory ratingKey="501" key="/library/collections/501/children" type="collection"
           subtype="movie" title="Michael Mann Films" titleSort="michael mann films"
           childCount="3" collectionSource="nfo" thumb="/library/metadata/12/thumb/1700000000"
           librarySectionID="1" librarySectionKey="/library/sections/1"
           librarySectionTitle="Movies" addedAt="…" updatedAt="…"/>
```

| Attribute | Meaning |
|---|---|
| `subtype` | `movie` or `show`, the type of its members |
| `childCount` | Members the requesting account can see |
| `collectionSource` | **(sloplex)** `nfo` (automatic) or `manual` |
| `thumb` | **(sloplex)** The thumbnail of the first member that has one, in member order. Omitted if none has one |
| `titleSort` | The title without a leading article, as for items |

In JSON, the element is an object in `Metadata`, as other directories are.

Items carry a `Collection` tag element for every collection they belong to
(see "Output" in [metadata.md](metadata.md)).

## Endpoints

The same library access rules apply as for `/library/sections`. A
collection in a hidden section, or in no section, returns `404`.

### `GET /library/sections/{id}/collections`

Returns the section's collections, ordered by `titleSort`. The container
sets `title1` to the section title, `viewGroup="collection"`, and
`librarySectionID`. Paging is supported.

### `GET /library/collections/{id}`

Returns a container holding the collection. `GET /library/metadata/{id}`
also returns it when `{id}` is a collection.

### `GET /library/collections/{id}/children`

Returns the members, rendered as in lists. They are ordered by `year`, then
`titleSort`, then ID, so a film series runs in release order.
`GET /library/metadata/{id}/children` also works for a collection. Paging is
supported.

### Changing collections (admins only)

Items are given in one of two ways:

- `uri`: the form above. Only the comma-separated IDs after the last
  `/library/metadata/` are used.
- `ids`: **(sloplex)** comma-separated item IDs.

| Request | Effect |
|---|---|
| `POST /library/collections?sectionId=&title=&uri=` | Creates a manual collection; `type` is accepted and ignored. Items may be empty. Returns `200` and a container holding the new collection |
| `PUT /library/collections/{id}/items?uri=` | Adds items. Members already present are skipped. Returns `200` and the collection |
| `DELETE /library/collections/{id}/items/{itemId}` | Removes one member. `404` if it is not a member |
| `PUT /library/collections/{id}?title=` | **(sloplex)** Renames the collection |
| `DELETE /library/collections/{id}` | Deletes the collection. Its items are not touched |

| Problem | Response |
|---|---|
| Not an admin | `403` |
| Missing or blank `title`, or a title over 200 characters | `400` |
| An item that is not a movie (movie section) or show (show section) of the collection's section | `400` |
| More than 1000 members | `400` |
| Changing an automatic collection | `400` |
| Unknown section or collection | `404` |
