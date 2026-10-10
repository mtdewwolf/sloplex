#!/usr/bin/env python3
"""Exercise the real Compose recipe against a locally built image; never publish.
Needs Docker + Compose v2, Python 3. Run: python3 test/container/smoke.py IMAGE [--tailscale]
--tailscale adds deploy/compose.tailscale.yaml; Tailscale starts but never logs in.
All generated state is temporary. Never print server logs or the owner token.
"""
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]


def command(*args, check=True):
    result = subprocess.run(args, text=True, capture_output=True, timeout=180)
    if check and result.returncode:
        # Docker application logs can contain the master token. Keep failures terse.
        raise RuntimeError(f"command failed (exit {result.returncode}): {args[0]}")
    return result


def tailscale_status(compose):
    """Returns tailscaled's backend state, retrying while it starts or restarts."""
    for _ in range(30):
        tid = command(*compose, "ps", "-q", "tailscale").stdout.strip()
        result = command("docker", "exec", tid, "tailscale", "--socket=/tmp/tailscaled.sock",
                         "status", "--json", check=False) if tid else None
        if result and result.stdout.strip().startswith("{"):
            return json.loads(result.stdout)["BackendState"]
        time.sleep(1)
    raise RuntimeError("tailscaled did not answer within 30 seconds")


def tailscale_reaches_sloplex(compose, seconds):
    """Waits until the Tailscale container reaches Sloplex where it proxies to."""
    for _ in range(seconds):
        tid = command(*compose, "ps", "-q", "tailscale").stdout.strip()
        if tid and command("docker", "exec", tid, "wget", "-q", "-T", "3", "-O", "/dev/null",
                           "http://sloplex:32400/identity", check=False).returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError(f"the Tailscale container did not reach Sloplex within {seconds} seconds")


def check_tailscale(compose, work, uid, gid):
    tid = command(*compose, "ps", "-q", "tailscale").stdout.strip()
    info = json.loads(command("docker", "inspect", tid).stdout)[0]
    host = info["HostConfig"]
    assert info["Config"]["User"] == f"{uid}:{gid}"
    assert host["ReadonlyRootfs"] and not host["Privileged"] and host["CapDrop"] == ["ALL"]
    assert not host.get("Devices") and not host.get("CapAdd")
    assert not host["NetworkMode"].startswith(("container:", "host")), host["NetworkMode"]
    tailscale_reaches_sloplex(compose, 30)
    assert tailscale_status(compose) in ("NoState", "NeedsLogin", "Starting")
    serve = json.loads(command("docker", "exec", tid, "cat", "/tmp/serve.json").stdout)
    site = "${TS_CERT_DOMAIN}:443"
    assert serve["Web"][site]["Handlers"]["/"]["Proxy"] == "http://sloplex:32400"
    assert serve["TCP"]["443"]["HTTPS"] is True and serve["AllowFunnel"][site] is False
    assert (work / "tailscale/tailscaled.state").exists()


def main():
    args = sys.argv[1:]
    tailscale = "--tailscale" in args
    images = [a for a in args if a != "--tailscale"]
    if len(images) != 1:
        raise SystemExit("usage: smoke.py IMAGE [--tailscale]")
    image = images[0]
    uid, gid = (os.getuid(), os.getgid()) if os.getuid() else (65532, 65532)
    with tempfile.TemporaryDirectory(prefix="sloplex-container-") as directory:
        work = Path(directory)
        work.chmod(0o755)
        for name in ("state", "scratch", "media", "tailscale"):
            path = work / name
            path.mkdir()
            if os.getuid() == 0:
                os.chown(path, uid, gid)
        config = work / "sloplex.json"
        config.write_text((ROOT / "deploy/sloplex.container.json").read_text())
        config.chmod(0o644)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        settings = {
            "SLOPLEX_IMAGE": image, "SLOPLEX_UID": uid, "SLOPLEX_GID": gid,
            "SLOPLEX_PORT": port, "SLOPLEX_BIND_IP": "127.0.0.1",
            "SLOPLEX_CONFIG": config, "SLOPLEX_STATE": work / "state",
            "SLOPLEX_SCRATCH": work / "scratch", "SLOPLEX_MEDIA": work / "media",
            "SLOPLEX_TS_STATE": work / "tailscale",
        }
        env = work / "env"
        env.write_text("".join(f"{key}={value}\n" for key, value in settings.items()))
        compose = ["docker", "compose", "--env-file", str(env), "-p", work.name,
                   "-f", str(ROOT / "deploy/compose.yaml")]
        if tailscale:
            compose += ["-f", str(ROOT / "deploy/compose.tailscale.yaml")]
        command(*compose, "config", "--quiet")
        try:
            command(*compose, "up", "-d")
            cid = command(*compose, "ps", "-q", "sloplex").stdout.strip()
            for _ in range(90):
                info = json.loads(command("docker", "inspect", cid).stdout)[0]
                if info["State"].get("Health", {}).get("Status") == "healthy":
                    break
                time.sleep(1)
            else:
                raise RuntimeError("container did not become healthy within 90 seconds")
            assert info["Config"]["User"] == f"{uid}:{gid}"
            assert info["HostConfig"]["ReadonlyRootfs"]
            assert not info["HostConfig"]["Privileged"]
            assert info["HostConfig"]["NetworkMode"] != "host"
            mounts = {m["Destination"]: m for m in info["Mounts"]}
            assert not mounts["/media"]["RW"]
            assert not mounts["/config/sloplex.json"]["RW"]
            assert mounts["/data"]["RW"] and mounts["/data/transcode"]["RW"]
            for path in ("/media/should-fail", "/config/should-fail", "/should-fail"):
                assert command("docker", "exec", cid, "touch", path, check=False).returncode != 0
            command("docker", "exec", cid, "touch", "/data/state-check", "/data/transcode/scratch-check")
            assert (work / "state/state-check").exists()
            assert (work / "scratch/scratch-check").exists()
            # Real CPU encode exercises the runtime ffmpeg and writable temporary storage.
            command("docker", "exec", cid, "ffmpeg", "-v", "error", "-f", "lavfi",
                    "-i", "testsrc2=s=128x72:r=10:d=1", "-f", "lavfi", "-i", "sine=d=1",
                    "-c:v", "libx264", "-c:a", "aac", "-shortest", "/tmp/test.mp4")
            command("docker", "exec", cid, "ffprobe", "-v", "error", "/tmp/test.mp4")
            url = f"http://127.0.0.1:{port}"
            assert urllib.request.urlopen(url + "/web/", timeout=5).status == 200
            try:
                urllib.request.urlopen(url + "/library/sections", timeout=5)
                raise AssertionError("library accepted unauthenticated access")
            except urllib.error.HTTPError as error:
                assert error.code == 401
            identity = (work / "state/identity.json").read_bytes()
            token = json.loads(identity)["token"]
            request = urllib.request.Request(url + "/library/sections", headers={"X-Plex-Token": token})
            assert urllib.request.urlopen(request, timeout=5).status == 200
            # Exercise the documented bootstrap and password login without printing secrets.
            password = "temporary-container-test-password"
            bootstrap = urllib.request.Request(
                url + "/sloplex/accounts/1", method="PATCH",
                data=json.dumps({"password": password}).encode(),
                headers={"X-Plex-Token": token, "Content-Type": "application/json"})
            assert urllib.request.urlopen(bootstrap, timeout=10).status == 200
            login = urllib.request.Request(
                url + "/sloplex/auth/login", method="POST",
                data=json.dumps({"username": "owner", "password": password}).encode(),
                headers={"Content-Type": "application/json"})
            assert urllib.request.urlopen(login, timeout=10).status == 200
            if tailscale:
                check_tailscale(compose, work, uid, gid)
            command(*compose, "restart")
            for _ in range(60):
                try:
                    if urllib.request.urlopen(url + "/identity", timeout=2).status == 200:
                        break
                except (OSError, urllib.error.URLError):
                    pass
                time.sleep(1)
            else:
                raise RuntimeError("server did not recover after restart")
            assert (work / "state/identity.json").read_bytes() == identity
            if tailscale:
                # Both restart independently and find each other again.
                tailscale_reaches_sloplex(compose, 60)
            print("Container smoke passed: health, non-root, read-only media/root, isolated state/scratch, CPU encode/probe, web, auth, owner bootstrap/login, restart persistence"
                  + (", Tailscale sidecar." if tailscale else "."))
        finally:
            command(*compose, "down", "--volumes", check=False)


if __name__ == "__main__":
    main()
