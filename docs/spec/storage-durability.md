# Local file replacement (sloplex)

## Provenance

This document specifies Sloplex's own storage design. It uses no proprietary
implementation or observed media-server storage format. The scope is one local
file replacement, not a multi-store transaction, backup, migration, or recovery
system.

## Replacement protocol

`atomicfile.Write` MUST create a unique temporary file in the destination's
parent directory. It MUST write all requested bytes, apply the requested file
permissions, synchronize the temporary file, close it, and then rename it over
the destination, in that order. A short write MUST fail. The temporary file MUST
be closed and its removal attempted on every failure before a successful
rename. Cleanup failures MUST remain inspectable in the returned error.

On Unix platforms, a successful rename MUST be followed by opening,
synchronizing, and closing the parent directory. Failure of any of these steps
MUST return a `*atomicfile.CommittedError`, including when the filesystem does
not support directory synchronization. The error's `Path` identifies the
destination and its `Unwrap` preserves the underlying failure for `errors.Is`
and `errors.As`. The replacement has been installed; callers MUST NOT treat
this error as evidence that the previous destination is still installed. They
MUST retain or publish the state that was written, while reporting the failure
to complete durability confirmation. No automatic rollback or repeat of a
non-idempotent mutation is safe merely because this error was returned.

Errors before successful rename are ordinary errors. Under the supported Unix
rename semantics, the old destination remains intact (or a missing destination
remains absent). This presumes no other writer changes the destination. A
failed cleanup can leave a temporary file; it is not a committed replacement.

## Guarantees and limits

On Unix, a successful call requests persistence of the new file's bytes and
permissions and of its directory entry using the filesystem's synchronization
primitives. Readers opening the destination across the rename see a complete
old or new file. Already-open handles can continue to read the old file.

On non-Unix platforms, including Windows, Sloplex synchronizes the temporary
file and uses the operating system's rename operation. Parent-directory
synchronization is unavailable in this implementation and is skipped. Success
on these platforms does not promise persistence of the renamed directory
entry or atomic visibility of replacement.

Even on Unix, success is not an unconditional power-loss guarantee: filesystem,
mount, network-storage, operating-system, and hardware behavior determine what
synchronization actually persists. Parent directories created by `MkdirAll`
are not synchronized in their ancestors. Durability of a newly created parent
directory therefore is outside this protocol. Concurrent writers are not
excluded, and there is no transaction across files, durable deletion, backup,
startup temporary-file recovery, or corruption recovery.

## Store integration

A store that publishes in-memory mutations before saving needs its own rollback
policy for failures before rename. A store that publishes only after saving
MUST also publish after a `CommittedError`, preserving memory/disk agreement.
Any rollback policy MUST inspect the error with `errors.As` before restoring
previous state. Propagating a `CommittedError` remains an error response; it
does not advertise confirmed durability. API retry and cross-store cleanup
policies require separate designs.

Library section configuration restores prior state and indexes after a failure
before rename, but retains the installed state after a `CommittedError`.
TrueNAS settings and server preferences publish after a successful save or a
`CommittedError`, and return the original save error. Preference subscribers
are notified whenever new values are published, including that uncertain
outcome; there is no notification on validation or pre-rename failure.

Library scans and explicit trash purging stage their catalog changes, preserve
the previous snapshot on pre-rename failure, and publish the installed snapshot
and indexes on a successful save or a `CommittedError`. They return the original
save error when durability confirmation fails; availability follows the
installed catalog. See [trash.md](trash.md).

Account, play-state, playlist, and library image mutations already publish
before persistence; their pre-rename failure behavior needs separate work.
Cached artwork and subtitle generation may report a write failure even when a
complete replacement is installed.

## Verification

Deterministic faults at writing (including short writes), permission changes,
file synchronization, file closing, rename, and directory finalization MUST
verify destination contents and temporary-file cleanup. Failures before rename
MUST preserve the old destination; faults after rename MUST expose a committed
error and leave the complete new destination installed. Tests MUST use
per-invocation dependencies rather than process-global mutable hooks. These
tests verify ordering and error handling, not actual power-loss survival.
