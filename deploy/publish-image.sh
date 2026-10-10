#!/bin/sh
# Build, smoke-test and push a release image; see docs/releases.md.
# usage: deploy/publish-image.sh REPOSITORY VERSION TAG...
# The first TAG must be VERSION itself, which is never overwritten; the
# rest (such as 0.1 and latest) move. Log in to the registry first.
# Prints the pushed image as REPOSITORY@sha256:... on the last line.
set -eu

[ $# -ge 3 ] || { echo "usage: $0 REPOSITORY VERSION TAG..." >&2; exit 2; }
repo=$1 version=$2
shift 2
[ "$1" = "$version" ] || { echo "the first tag must be the version, $version" >&2; exit 2; }
cd "$(dirname "$0")/.."

if [ -n "$(git status --porcelain)" ]; then
	echo "the checkout has changes or untracked files; publish a commit, not a work tree" >&2
	exit 1
fi
# The git tag is the main guard against reuse; this catches a tag that was
# deleted and pushed again. A package that was never published can answer
# "denied" rather than "not found", so other errors only warn.
if found=$(docker buildx imagetools inspect "$repo:$version" 2>&1); then
	echo "$repo:$version already exists; version tags are never reused" >&2
	exit 1
fi
case $found in
*": not found"*) ;;
*) echo "warning: could not check for an existing $repo:$version: $found" >&2 ;;
esac

revision=$(git rev-parse HEAD)
created=$(git log -1 --format=%cI HEAD)
build="$repo:$version"
docker build --platform linux/amd64 --build-arg VERSION="$version" \
	--build-arg REVISION="$revision" --build-arg CREATED="$created" -t "$build" .

got=$(docker run --rm --network none "$build" -version)
[ "$got" = "$version" ] || { echo "the image reports version $got, want $version" >&2; exit 1; }
python3 test/container/smoke.py "$build"

for tag in "$@"; do
	[ "$tag" = "$version" ] || docker tag "$build" "$repo:$tag"
	docker push --quiet "$repo:$tag" >/dev/null
done
digest=$(docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$build" | grep -m1 "^$repo@")
echo "$digest"
