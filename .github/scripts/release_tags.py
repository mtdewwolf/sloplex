#!/usr/bin/env python3
"""Work out the image tags for a release; see docs/releases.md.

usage: release_tags.py VERSION [EXISTING_GIT_TAG...]

VERSION is X.Y.Z or X.Y.Z-PRERELEASE, with or without a leading "v".
Prints GitHub Actions outputs: version, tag (the git tag), prerelease,
latest, and tags (space-separated image tags, the exact version first).
A stable release also moves X.Y and latest when nothing newer exists.
"""
import re
import sys

SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?$")


def parse(version):
    """Returns ((major, minor, patch), prerelease) or None."""
    m = SEMVER.match(version.removeprefix("v"))
    if not m:
        return None
    return tuple(int(m.group(i)) for i in (1, 2, 3)), m.group(4)


def release(version, existing):
    parsed = parse(version)
    if parsed is None:
        raise ValueError(f"{version!r} is not a version like 1.2.3 or 1.2.3-rc.1")
    number, pre = parsed
    version = version.removeprefix("v")
    if len(version) > 120:
        raise ValueError("version is too long for an image tag")
    tags = [version]
    latest = False
    if pre is None:
        # Only other stable releases can be newer; prereleases never move tags.
        stable = [p[0] for p in map(parse, existing) if p and p[1] is None]
        if all(number >= s for s in stable if s[:2] == number[:2]):
            tags.append(f"{number[0]}.{number[1]}")
        if all(number >= s for s in stable):
            tags.append("latest")
            latest = True
    return {
        "version": version,
        "tag": "v" + version,
        "prerelease": str(pre is not None).lower(),
        "latest": str(latest).lower(),
        "tags": " ".join(tags),
    }


def main(argv):
    if len(argv) < 2:
        raise SystemExit(__doc__.strip().splitlines()[2])
    try:
        out = release(argv[1], argv[2:])
    except ValueError as e:
        raise SystemExit(f"release_tags: {e}")
    for key, value in out.items():
        print(f"{key}={value}")


if __name__ == "__main__":
    main(sys.argv)
