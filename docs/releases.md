# Releases

A release is a version number, a git tag `vX.Y.Z`, a GitHub release, and a
container image at `ghcr.io/mtdewwolf/sloplex`. Nothing is published until
someone starts a release.

## Versions and image tags

Versions follow [semantic versioning](https://semver.org): `0.2.0`, or
`0.3.0-rc.1` for a test build. Before 1.0, a new minor version (`0.2` to
`0.3`) may change how stored data is kept; its release notes say so.

Each release pushes these image tags:

| Tag | Points to | Changes |
|---|---|---|
| `0.2.0` | exactly that release | never |
| `0.2` | the newest `0.2.x` | with each fix release for 0.2 |
| `latest` | the newest release | with every release |

A test build (`-rc.1` and so on) gets only its own tag. A fix for an older
line, such as `0.1.5` after `0.2.0`, moves `0.1` but not `latest`. The
rules are in `.github/scripts/release_tags.py`.

The image is Linux amd64, CPU-only. Its labels record the version, the
commit (`org.opencontainers.image.revision`) and this repository, and
`sloplex -version` prints the version.

## Making a release

From the repository's **Actions** tab, choose **Release**, then **Run
workflow**, and enter the version without a `v`. Pushing a tag `vX.Y.Z`
does the same.

The workflow:

1. Runs every CI check: Go tests, browser suites, and the container test.
2. Refuses a malformed version or a commit that is not on the default
   branch. Started from the Actions tab, it also refuses a version that
   already has a tag, and creates the tag in step 5.
3. Builds the image with the version built in and checks that it reports
   it. Then it runs the container smoke test against the image.
4. Pushes the image tags. A version tag that already exists in the
   registry is never overwritten.
5. Creates the GitHub release with generated notes, the image's digest,
   and its tags. If the release was already written by hand, the image
   details are added to its notes.

`deploy/publish-image.sh` holds steps 3 and 4. Run it from a clean checkout
to publish to another registry.

If a run fails after pushing some tags, don't reuse the version: release
the next patch version.

## First release

GitHub makes a new package private. After the first release, the owner
opens the package from the repository's **Packages** list, then **Package
settings**, and changes its visibility to public. Until then, pulling it
needs a GitHub token with the `read:packages` scope. Making the package
public cannot be undone.

## Security updates

The image includes Debian's ffmpeg and system libraries as they were when
it was built. To pick up their fixes, release a new patch version. The base
image digests in the `Dockerfile` should be refreshed from time to time;
see [the deployment guide](deploy-truenas.md).
