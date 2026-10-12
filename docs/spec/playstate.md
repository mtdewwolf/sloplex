# Play state (watched and resume)

## Provenance

- These names come from publicly available community documentation of the
  media-server HTTP API and from open-source client libraries' public docs:
  - the endpoint paths `/:/scrobble`, `/:/unscrobble`, `/:/progress`,
    `/:/timeline`, and `/library/onDeck`;
  - their `key`, `ratingKey`, `identifier`, `time`, `duration`, and `state`
    parameters;
  - the attributes `viewCount`, `viewOffset`, `lastViewedAt`, and
    `viewedLeafCount`;
  - the `unwatched=1` filter.
- Items marked **(sloplex)** are our own choices, including all thresholds.

## Model

- Play state is kept per **account** and per **leaf item** (movie or
  episode).
- The account is the one the request authenticated as; see
  [accounts.md](accounts.md). Each account's watch history is separate.
- Deleting an account deletes its play state.
- Each leaf item has three values:

| Value          | Meaning                                                   |
|----------------|-----------------------------------------------------------|
| `viewCount`    | How many times the item was watched to the end            |
| `viewOffset`   | Resume position in ms; 0 means not in progress            |
| `lastViewedAt` | Unix seconds of the last watch or progress update         |

- Show and season state is derived from their episodes. It is never stored.
- **(sloplex)** State is stored in `<dataDir>/playstate.json`, separate from
  the library. A rescan never changes it. State belongs to an item ID, so it
  goes away if a file is removed and later re-added under a new ID.

## Attributes on items

Leaf items (movies and episodes) carry these attributes, each omitted when it
is 0:

- `viewCount`
- `viewOffset`
- `lastViewedAt`

Shows and seasons carry these:

- `viewedLeafCount`: the number of episodes with `viewCount` > 0. It is always
  present.
- `lastViewedAt`: the latest `lastViewedAt` of any episode. It is omitted when
  0.

## Identifying the item

Every endpoint on this page takes the item from one of these query
parameters:

- `ratingKey`, a numeric item ID;
- `key`, either a numeric item ID or `/library/metadata/{id}`.

`ratingKey` wins if both are given.

The `identifier` parameter is accepted and ignored.

| Problem                           | Response        |
|-----------------------------------|-----------------|
| Item missing or not numeric       | 400 Bad Request |
| Item unknown                      | 404 Not Found   |

All of these endpoints accept both `GET` and `PUT`. On success they return
`200` with an empty body.

`/:/timeline` also accepts `POST`, the form in the official API reference.
Its requirements and response differ; see
[wire-conformance.md](wire-conformance.md).

## Endpoints

### `/:/scrobble`

Marks the item watched. For each target leaf, it increments `viewCount`,
clears `viewOffset`, and sets `lastViewedAt` to now.

- A show or season targets **all of its episodes** **(sloplex)**.

### `/:/unscrobble`

Marks the item unwatched. It resets all state of each target leaf.

- Shows and seasons cascade to their episodes, like `/:/scrobble`.

### `/:/progress`

Reports a resume position. Parameters:

- `time`, required: the position in ms.
- `state`, optional, ignored.

The item MUST be a leaf; otherwise the response is 400. The completion rule
below applies.

### `/:/timeline`

Reports a playback update. Parameters:

- `time`: the position in ms. Without it the request is a no-op and returns
  200.
- `duration`: the length in ms. When it is absent, the item's stored duration
  is used.
- `state`: `playing`, `paused`, `buffering`, or `stopped`. It does not affect
  playstate. With a client identifier it also drives the session list; see
  [playback-sessions.md](playback-sessions.md).

The item MUST be a leaf; otherwise the response is 400. The completion rule
below applies.

### Completion rule (sloplex)

Given position `t` and duration `d`, these rules apply in order:

1. If `d > 0` and `t ≥ 90%` of `d`, the item counts as finished:
   - If it was in progress (`viewOffset` > 0) or never watched
     (`viewCount` = 0), `viewCount` is incremented. Repeated updates near the
     end therefore count once.
   - `viewOffset` is cleared, and `lastViewedAt` is set to now.
2. Otherwise, if `t > 0`, `viewOffset` is set to `t` and `lastViewedAt` is set
   to now.
3. Otherwise (`t ≤ 0`), nothing changes.

### `GET /library/onDeck` and `GET /library/sections/{id}/onDeck`

Returns what to watch next for the requesting account. The section variant
returns 404 for an unknown section. Both support paging.

The list contains:

- every leaf item in progress (`viewOffset` > 0);
- for each show that has no episode in progress but has at least one watched
  episode, the first unwatched, non-special episode after the most recently
  watched one, in hierarchy order. Specials are season 0.

Entries are ordered by `lastViewedAt`, newest first. For a next-up episode,
the show's latest `lastViewedAt` is used.

## Filters

`GET /library/sections/{id}/all?unwatched=1` keeps only these items:

- leaf items with `viewCount` = 0;
- shows and seasons with `viewedLeafCount` < `leafCount`.
