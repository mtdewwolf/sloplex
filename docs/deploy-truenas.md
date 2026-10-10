# CPU-only Sloplex on TrueNAS 25.10 (Goldeye)

This is a staged deployment recipe for the 25.10 family, including 25.10.2.
It has not been installed or tested on a NAS as part of this change. Each
release publishes a CPU-only image, `ghcr.io/mtdewwolf/sloplex`; TrueNAS
pulls it and offers each new release from the Apps screen. No GPU is
needed. Do not paste the template into TrueNAS before filling and checking
inputs.

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

## Choose an image

Pick the tag that sets how far updates go:

| Image | Updates you are offered |
|---|---|
| `ghcr.io/mtdewwolf/sloplex:latest` | every release (recommended) |
| `ghcr.io/mtdewwolf/sloplex:0.1` | fixes to 0.1 only; move to 0.2 by hand |
| `ghcr.io/mtdewwolf/sloplex:0.1.0` | none; this exact build forever |

Replace `0.1` with the current version from the
[releases page](https://github.com/mtdewwolf/sloplex/releases). Before 1.0,
a minor release such as 0.2 may change how stored data is kept; its release
notes say so. Each release also lists its image digest. For a fully pinned
image, use `ghcr.io/mtdewwolf/sloplex@sha256:...` from those notes.
[Releases](releases.md) explains the tags and how images are built and
tested.

The image is Linux amd64, matching x86-64 TrueNAS systems. The NAS needs to
reach `ghcr.io` over HTTPS when installing and updating. The package is
public, so no registry credentials are needed. Never enter a GitHub
password or token in the app YAML.

The image comes from the official Debian base, pinned by digest in the
`Dockerfile`, plus Debian's ffmpeg. Each release passes the same container
smoke test as CI before it is pushed. That test checks:

- non-root operation;
- read-only media, root and config;
- separate writable state and scratch;
- CPU H.264/AAC encode and probe;
- health, web serving and required authentication;
- identity persistence across restart.

It does not prove NAS ACLs or real-media performance.

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
verified NAS paths, UID/GID, address, checked port, and the chosen image.
Do not put passwords or tokens in it. Render with Docker Compose v2 on any
computer with a checkout of this repository:

```sh
docker compose --env-file /ABSOLUTE/PATH/TO/filled.env \
  -f deploy/compose.yaml config --no-path-resolution > /ABSOLUTE/PATH/TO/compose.rendered.yaml
```

Missing variables fail the render. Review the result carefully: all
placeholders must be gone, the user must be non-root, only the chosen LAN
address/port may be published, all paths must be NAS paths, and the image
must be the one you chose. There is
no need for a `.env` file in TrueNAS. Rendering validates Compose syntax,
not path existence, ACLs, port availability, or workload sizing.

## Install through TrueNAS Apps, when ready

In Apps, select/configure the Apps pool if not already done. Open Discover
Apps, use the three-dot menu beside Custom App, then **Install via YAML**.
Choose an unused application name such as `sloplex`, paste the reviewed
rendered YAML, and save only when ready to start the server. TrueNAS pulls
the image and then starts it. Use the Apps
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

## Updates

Watch the repository's releases on GitHub (**Watch**, then **Custom**, then
**Releases**) to hear about them. TrueNAS also notices them itself: with
**Check for docker image updates** on (the default, under the Apps
screen's settings), an app whose tag has a newer image gets a yellow update
badge and an **Update** button. For `latest` that is every release; for
`0.1`, every 0.1 fix. Updates are never applied without you.

For each update:

1. Read the release notes, especially for a new minor version.
2. Take a snapshot of the state dataset and config, and note the current
   exact version (shown on the Admin page) for rollback. Sloplex replaces
   its data files whole, so a snapshot of the running app is safe. Exclude
   transcode scratch; it is disposable. Do not remove identity.json or
   accounts.json.
3. Click **Update** on the app. TrueNAS pulls the new image and restarts
   Sloplex with it. Starting and restarting the app on its own never pulls
   an image.
4. Open the Admin page in the web app and check the version under
   **Server**. Repeat the checks above.

With an exact version tag there is no badge. Edit the app's YAML, change
the image to the new version, and save. If no badge appears for a moving
tag, do the same with the new release's exact version; you can switch
back to the moving tag at the next update.

New releases also carry Debian's latest ffmpeg and library fixes. Staying
on an old version means missing those as well.

### Updating from Sloplex

The **Admin** page shows the running version and a **Check for updates**
button. Sloplex also checks GitHub once a day; set `"updateCheck": false`
in `sloplex.json` to stop that.

With one-click updates set up, a newer release gets an **Update** button.
Sloplex then asks TrueNAS to pull the image and restart the app, the same
as clicking **Update** in TrueNAS, without waiting for TrueNAS to notice
the release. The app's image must use a moving tag such as `latest`. A
pinned version has nothing new to pull.

This needs a TrueNAS API key, which TrueNAS ties to a user. Anyone holding
the key can manage every app on the NAS, so give it to a user that can do
only that:

1. **Credentials → Groups → Add**: a group named `sloplex-updates`.
2. **Credentials → Groups → Privileges → Add**: a privilege named
   `Sloplex updates` for that group, with the single role `APPS_WRITE`.
3. **Credentials → Users → Add**: a user named `sloplex-updates` in that
   group, with password login and shell access disabled.
4. **My API Keys → Add API Key**, choosing that user, non-expiring. Copy
   the key; TrueNAS shows it only once.

If TrueNAS will not offer that user for a key, a key for your admin user
also works, but it can then do anything that admin can.

Then, in Sloplex, signed in as `owner`: **Admin → Server → Set up one-click
updates through TrueNAS**:

- **TrueNAS address**: `https://` and the address you use for the TrueNAS
  web UI, such as `https://192.168.1.10`. Plain `http://` is refused,
  because TrueNAS revokes an API key sent over it.
- **App name**: the app's name under **Apps → Installed**, such as
  `sloplex`.
- **API key**: the key from step 4.

Click **Test connection**. TrueNAS's own certificate is self-signed, so the
first test shows its SHA-256 fingerprint. Check it from the NAS shell,
where nothing can sit in between:

```sh
echo | openssl s_client -connect 127.0.0.1:443 2>/dev/null | openssl x509 -noout -fingerprint -sha256
```

If the two match, click **Trust this certificate**. Then **Test connection**
reports the app's state; **Save**. Only the owner sees these settings, and
the key is never shown again. Changing the address or the certificate
needs the key entered again, so a saved key cannot be sent somewhere else.
**Remove** deletes the settings and the key; revoke the key in TrueNAS as
well if you stop using it.

Take a snapshot before clicking **Update**, as above. Playback stops while
TrueNAS restarts Sloplex. The page reloads when the new version answers.

## Rollback

Stop the app, edit its YAML to the previous exact version, such as
`ghcr.io/mtdewwolf/sloplex:0.1.0`, and start it. If the newer version
changed stored data, restore the matching state snapshot while stopped,
before starting the older image. Restoring a snapshot discards changes made
after it; take a current one first. The app now stays on that version;
switch back to `latest` when the problem is fixed.

Never run two Sloplex instances against the same writable state. To
abandon the trial, stop and remove only this app. Keep the datasets until
you deliberately decide to remove them. Never delete or roll back the media
dataset as part of this procedure.

## Build your own image

To run unreleased code, or without a registry, build on a separate Docker
computer from a reviewed checkout. Verify the NAS architecture first:

```sh
REV=$(git rev-parse HEAD)
IMAGE="sloplex:cpu-$REV"
docker build --platform linux/amd64 --build-arg VERSION="$REV" --build-arg REVISION="$REV" -t "$IMAGE" .
python3 test/container/smoke.py "$IMAGE"
docker image inspect "$IMAGE" --format '{{.Id}} {{.Architecture}}'
docker save --output "sloplex-$REV.tar" "$IMAGE"
sha256sum "sloplex-$REV.tar" > "sloplex-$REV.tar.sha256"
```

Transfer the archive and checksum to a private NAS staging location. Then,
in that directory in the NAS shell:

```sh
sha256sum -c sloplex-REPLACE_REV.tar.sha256
sudo docker load --input sloplex-REPLACE_REV.tar
sudo docker image inspect sloplex:cpu-REPLACE_REV --format '{{.Id}} {{.Architecture}}'
```

Match the ID and architecture against the build computer's record. Set
`SLOPLEX_IMAGE=sloplex:cpu-REV` with the actual commit. After rendering,
add `pull_policy: never` under `image:` so TrueNAS never looks for it in a
registry. Such an app gets no update badge; repeat this section for each
update.

Keep the archive: pruning or a host rebuild can remove a loaded image. A
fresh build is not byte-for-byte repeatable, because Debian's package
repositories change. The base images are pinned to official digests,
verified against Docker Library metadata on 2026-10-10:
[Go](https://github.com/docker-library/repo-info/blob/master/repos/golang/remote/1.27.2-bookworm.md)
and [Debian](https://github.com/docker-library/repo-info/blob/master/repos/debian/remote/bookworm-slim.md).
Build arguments can override them with newly verified digests.
