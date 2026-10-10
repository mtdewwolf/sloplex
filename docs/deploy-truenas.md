# CPU-only Sloplex on TrueNAS 25.10 (Goldeye)

This is a staged deployment recipe for the 25.10 family, including 25.10.2.
It has not been installed or tested on a NAS as part of this change. There
is no published Sloplex image: build and load your own. No GPU is needed.
Do not paste the template into TrueNAS before filling and checking inputs.

## Before installation

Record these deployment decisions first:

- Exact existing media dataset path and the movie/show subdirectories to
  expose. Start with a small representative library. Never mount a pool
  root, Plex's state, or unrelated datasets.
- Whether Plex or another server is already running, and its ports.
  **32401 is only a suggested spare TCP port; verify it is free before use.**
  Internal Sloplex HTTP stays at 32400; publish only the chosen host port.
- NAS LAN IPv4 address; bind explicitly to that address, not all interfaces.
- A non-root numeric UID and GID chosen from the NAS's actual users/groups.
  The image default 65532 is not a recommendation for your NAS. Do not use
  UID/GID 0. Verify dataset ACLs and traversal permissions with the selected
  identity; do not recursively chown media or loosen ACLs to world-writable.
- Separate dedicated state and transcode-scratch dataset paths, with write
  access for that identity; a read-only config-file path; storage quotas,
  and available CPU/RAM. Keep state and scratch outside the media dataset.
  Scratch is disk-backed, not an unbounded RAM filesystem. Set a reasonable
  scratch dataset quota and monitor free space; one transcode can be large.
- Selected CPU and RAM budget alongside existing services. `maxTranscodes=1`
  limits sessions, not CPU threads or memory. Add Compose `cpus` and
  `mem_limit` only after choosing a budget for the actual NAS; a low limit
  may stall playback or trigger OOM. Start with direct play and 1080p;
  real-time 4K HEVC/HDR software transcoding is not guaranteed.

Check listening TCP ports in the NAS shell with `ss -ltn`, plus the Apps UI
for reserved/published ports, including stopped apps. Recheck immediately
before deployment. Do not stop or reconfigure Plex to free its port.

Create the dedicated datasets using TrueNAS's UI, then give the chosen
identity read/traverse-only media access and read/write state/scratch
access. Precreate a `transcode` directory inside state as the mount point
for scratch. The config file must exist and be readable by the app user;
all host paths must already exist. Bind mounts deliberately refuse to
create missing paths. Do not install Go, Docker packages, or developer mode
on the TrueNAS host.

## Build and retain an image on a separate Docker computer

Use a reviewed checkout and Docker with Compose v2. Run from the repository
root. The example builds Linux amd64 for an x86-64 NAS; verify the NAS
architecture before selecting the platform. Cross-platform builders need
working emulation. This does not publish anything:

```sh
REV=$(git rev-parse HEAD)
IMAGE="sloplex:cpu-$REV"
docker build --platform linux/amd64 --build-arg VERSION="$REV" -t "$IMAGE" .
python3 test/container/smoke.py "$IMAGE"
docker image inspect "$IMAGE" --format '{{.Id}} {{.Architecture}}'
docker save --output "sloplex-$REV.tar" "$IMAGE"
sha256sum "sloplex-$REV.tar" > "sloplex-$REV.tar.sha256"
```

The smoke test runs only on that Docker computer, uses generated temporary
state and loopback networking, and removes its test stack afterward. It
checks non-root operation, read-only media/root/config, separate writable
state/scratch, CPU H.264/AAC encode and probe, health, web serving, required
authentication, and identity persistence across restart. It does not prove
NAS ACLs or real-media performance. CI runs the same build and smoke test;
CI does not push images. The browser and Go suites remain separate checks.

The builder version and both base-image digests are pinned to official
images, verified against Docker Library metadata on 2026-10-10:
[Go](https://github.com/docker-library/repo-info/blob/master/repos/golang/remote/1.27.2-bookworm.md)
and [Debian](https://github.com/docker-library/repo-info/blob/master/repos/debian/remote/bookworm-slim.md).
Build arguments can override them with newly verified digests. Debian package repositories can change, so a fresh build
is not promised byte-for-byte identical. For repeatable deployment/rollback,
retain this exact image archive, checksum, source commit, and image ID.
Rebuild and retest periodically for base/FFmpeg security updates. Do not
reuse a tag for different image contents.

Transfer the archive and checksum to a chosen private NAS staging location
using your approved file-transfer method. In the NAS shell, verify the
checksum and load the archive into the existing Docker engine:

```sh
# Run in the directory containing the transferred archive and checksum.
sha256sum -c sloplex-REPLACE_REV.tar.sha256
sudo docker load --input sloplex-REPLACE_REV.tar
sudo docker image inspect sloplex:cpu-REPLACE_REV --format '{{.Id}} {{.Architecture}}'
```

Replace `REPLACE_REV` with the actual commit. Match the ID and architecture
against the build computer's record. A locally loaded image can be removed
by pruning or a host rebuild; keep the archive somewhere safe and reload
when needed. `pull_policy: never` intentionally fails if that exact image
is absent. Do not enter registry credentials or replace it with an
unverified public image.

## Prepare config and render Compose

Copy `deploy/sloplex.container.json` to your chosen NAS config path. It starts
with no libraries. Add only paths inside `/media`, matching your real media
layout, for example replacing these relative placeholders:

```json
"libraries": [
  {"title": "Movies", "type": "movie", "paths": ["/media/REPLACE_MOVIE_SUBDIRECTORY"]},
  {"title": "TV", "type": "show", "paths": ["/media/REPLACE_TV_SUBDIRECTORY"]}
]
```

These are container paths, not NAS host paths. Remove any unused section.
A single read-only `/media` mount exposes the selected dataset; if movies
and TV are separate datasets, use separate read-only bind mounts and align
the JSON paths. Preserve `allowedNetworks: []`, `tokens: []`,
`discovery: false`, `hwaccel: "none"`, and `maxTranscodes: 1` initially.
No host networking, UDP discovery, GPU devices, privileged mode, or Docker
socket is required. Discovery is intentionally disabled because bridge
port mapping would otherwise advertise the wrong external port.

Copy `deploy/env.example` outside the checkout and fill every value with
verified NAS paths, UID/GID, address, checked port, and exact loaded tag.
Do not put passwords or tokens in it. Render on the build computer:

```sh
docker compose --env-file /ABSOLUTE/PATH/TO/filled.env \
  -f deploy/compose.yaml config --no-path-resolution > /ABSOLUTE/PATH/TO/compose.rendered.yaml
```

Missing variables fail the render. Review the result carefully: all
placeholders must be gone, the user must be non-root, only the chosen LAN
address/port may be published, and all paths must be NAS paths. There is
no need for a `.env` file in TrueNAS. Rendering validates Compose syntax,
not path existence, ACLs, port availability, or workload sizing.

## Install through TrueNAS Apps, when ready

In Apps, select/configure the Apps pool if not already done. Open Discover
Apps, use the three-dot menu beside Custom App, then **Install via YAML**.
Choose an unused application name such as `sloplex`, paste the reviewed
rendered YAML, and save only when ready to start the server. Use the Apps
UI to stop/start/edit it afterward, rather than starting a second copy with
`docker compose up` in the shell. YAML installs receive basic syntax
validation, not semantic safety validation by TrueNAS.

The official [25.10 Custom App reference](https://www.truenas.com/docs/scale/25.10/scaleuireference/apps/installcustomappscreens/)
describes this flow and the limits of its validation. The
[Docker Compose service reference](https://docs.docker.com/reference/compose-file/services/)
documents the image, pull policy, user, bind mount, and security settings.

## Bootstrap, validate, and protect access

Only expose the app on a trusted LAN for initial setup. HTTP credentials
and media URLs are unencrypted; do not forward this port to the Internet.
Use an independently configured HTTPS reverse proxy or trusted VPN before
untrusted-network access. Empty `allowedNetworks` is essential: adding a
LAN CIDR there gives tokenless requests owner privileges, not just access.

First start writes the owner recovery token to `state/identity.json`
(mode 0600) **and prints it in startup logs**. Anyone who can view those
logs or read the state dataset can obtain owner access. Restrict TrueNAS
app/log administration and backups accordingly. Do not upload logs, paste
identity.json into tickets, or include tokens in Compose, screenshots,
URLs, or shell command history. Setting an owner password does not revoke
this master token. Keep a private backup for recovery; do not delete state
to reset a forgotten password.

The web app currently accepts username/password, not a master-token login.
Bootstrap the owner password from a private NAS shell using the following
Python snippet, after replacing both paths/address placeholders. Run it
with permission to read the dedicated state file. Enter the password only
at the hidden prompts; it is not placed in command history or arguments.
The request stays on the NAS to the address/port you just published.

```sh
sudo python3 - <<'PYTHON'
import getpass, json, urllib.request
identity_path = "/ABSOLUTE/NAS/STATE/PATH/identity.json"
base_url = "http://NAS_LAN_IP:CHECKED_PORT"
password = getpass.getpass("New owner password (at least 8 characters): ")
assert len(password) >= 8, "Password too short"
assert password == getpass.getpass("Confirm password: "), "Passwords differ"
with open(identity_path) as source:
    token = json.load(source)["token"]
request = urllib.request.Request(
    base_url + "/sloplex/accounts/1",
    data=json.dumps({"password": password}).encode(), method="PATCH",
    headers={"Content-Type": "application/json", "X-Plex-Token": token})
with urllib.request.urlopen(request, timeout=10) as response:
    print("Owner password set; sign in as owner.")
PYTHON
```

Open `http://NAS_LAN_IP:CHECKED_PORT/web/` and sign in as `owner` with the
password you set. Create per-person accounts and limit libraries as
appropriate. The same procedure is the recovery path if passwords are
lost; resetting this way revokes the owner’s issued device tokens but not
the master token. See [accounts](spec/accounts.md) and [authentication](spec/auth.md)
for recovery and token semantics.

Before adding the full library, verify:

1. App health becomes healthy; `/identity` and `/web/` return 200.
   Health checks only prove HTTP liveness, not successful scans/playback.
2. An unauthenticated `/library/sections` request returns 401.
3. Expected test items appear, and nothing outside the selected mounts does.
   Media files remain unchanged; verify dataset ACLs in addition to the
   read-only container mount.
4. Direct play, seeking, resume and one CPU transcode work with representative
   media; observe CPU, RAM, scratch space, and buffering. Check subtitles
   and audio-track changes for files that need them.
5. Restart through Apps; identity, accounts, library, and watch history
   survive. Confirm any existing Plex instance is still healthy.

If unhealthy, check path/type/ACL errors and the exact image tag first;
review logs privately. Do not weaken security options or make media
writable to work around a permission failure. Scanner permissions can
produce an empty library even while the health check is green.

## Update and rollback

Before changing image/config, stop Sloplex in Apps and take a consistent
snapshot/backup of its dedicated state dataset and config. Retain the old
image archive/ID and rendered Compose. Exclude transcode scratch from
backups; it is disposable. Do not remove identity.json or accounts.json.

Build and smoke-test the new image on the build computer, transfer/load it,
then edit the app YAML to the new immutable tag and start. Repeat the
checks above. No automatic image updates or registry publishing are set up.

To roll back, stop the app, restore the previous config and image tag
(reload its archive if needed), and start. If the newer application changed
stored-data formats, restore the matching state snapshot while stopped
before running the older image. Snapshot restoration discards changes made
after that snapshot; preserve a current backup first. Never run two Sloplex
instances against the same writable state. Stop/remove only this app if
abandoning the trial; preserve datasets until you deliberately decide to
remove them. Never delete or roll back the media dataset as part of this
procedure.
