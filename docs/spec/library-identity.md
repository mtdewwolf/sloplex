# Configured library identity

## Provenance

Everything on this page is **(sloplex)**: our own configuration and local
storage design for preserving library references. It is not a Plex
preference, observed PMS matching rule, metadata GUID or cloud identifier.

## Explicit identities

A configured library may contain an optional string `id`:

```json
{"id":"movies-main","title":"Movies","type":"movie","paths":["/media/movies"]}
```

- The string is case-sensitive and contains 1–64 ASCII characters. Its
  first character is a letter or digit; subsequent characters may also be
  `.`, `_` or `-`. An omitted/empty ID selects legacy matching below.
- Explicit IDs MUST be unique across all configured libraries, including
  different media types. Libraries with distinct explicit IDs may have
  identical display titles. A title/type group containing an ID-less
  definition MUST have only one definition.
- The ID is persisted as a section's internal `configId`, separately from
  its numeric section ID and UUID. Reusing the same explicit identity
  preserves both existing IDs, catalog entries, collection membership and
  references held by account restrictions, playstate and playlists.
- An exact existing identity may change title and paths. Title/path edits
  update the section modification timestamp without resetting creation or
  scan timestamps. Changing its media type MUST fail; create a different
  library deliberately instead. Changing paths does not promise identity
  for moved media files: later filesystem scans still follow their own
  matching and [scan-safety.md](scan-safety.md) rules.
- Removing a library definition deliberately removes the section, its
  catalog and collections under the existing removal semantics. This does
  not erase external account/playstate/playlist records, nor transfer them
  to a future library. Recreating a removed configuration ID creates a new
  numeric section ID/UUID; do not recycle identities to restore history.

## Legacy matching and staged adoption

- An ID-less definition matches only an unambiguous exact media type and
  display title. Case changes are display changes, not fuzzy matches. List
  order, similar names and filesystem roots MUST NOT select identity.
- An unchanged legacy definition preserves its established section ID and
  UUID across startup/reopen, including ordinary path edits. If the uniquely
  matched section already has a configuration ID, omitting it does not
  remove that stored binding. Keep the ID in the file for future renames.
- An unknown explicit ID can adopt an unbound legacy section with exactly
  the same type/title only when there is one candidate and one unresolved
  definition for that title/type. Existing explicitly bound sections cannot
  be rebound to a different identity through this fallback.
- Safe migration is staged: first add an ID while leaving the legacy
  type/title unchanged, start successfully to persist the binding, then
  change the title while retaining that ID. Configuration is read-only to
  the application; it is not rewritten automatically.
- A new unmatched explicit ID combined with an omitted unbound legacy
  section of the same type is ambiguous and MUST fail. Split an intentional
  removal/addition into separate successful configurations. An ID-less
  possible rename or a new-ID replacement sharing a configured root with
  an omitted section MUST also fail. Shared roots are a rejection safeguard,
  never an identity match: comparison uses absolute lexical paths, without
  resolving symlink aliases. This is not mount or file-move detection.
- An unmatched definition that has the same title but a different type as
  an omitted section MUST fail as an ambiguous type change. A separate
  different-type library with that title is allowed while the original
  section remains explicitly represented.
- Definitions that reference one stored section twice, ambiguous legacy
  title/type matches, duplicate stored configuration identities, or invalid
  identities MUST fail before changing catalog state or ID counters.

## Synchronization and limitations

Synchronization validates and resolves the complete configuration before
applying it. Section records are staged; a failed save before replacement
restores the prior in-memory library state and indexes. If replacement was
installed but directory durability could not be confirmed, the new state and
indexes remain published and the save's `*atomicfile.CommittedError` is returned.
See [storage-durability.md](storage-durability.md) for filesystem and platform
limits. This does not add cross-store transactions or backup restoration.

Legacy remove/add operations with distinct title/type and non-shared roots
retain their existing behavior. Without an explicit ID, changing both a
title and unrelated paths can still appear to be deliberate removal plus
addition. Automatic recovery of an arbitrary legacy rename is not claimed.
Neither adoption nor renaming recovers references already lost by an earlier
destructive startup/scan. This increment does not implement HTTP library CRUD
or runtime configuration reload.
