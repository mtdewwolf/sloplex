#!/usr/bin/env python3
"""Exercise the real Compose recipe against a locally built image; never publish.
Needs Docker + Compose v2, Python 3. Run: python3 test/container/smoke.py IMAGE
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


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: smoke.py IMAGE")
    uid, gid = (os.getuid(), os.getgid()) if os.getuid() else (65532, 65532)
    with tempfile.TemporaryDirectory(prefix="sloplex-container-") as directory:
        work = Path(directory)
        work.chmod(0o755)
        for name in ("state", "scratch", "media"):
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
            "SLOPLEX_IMAGE": sys.argv[1], "SLOPLEX_UID": uid, "SLOPLEX_GID": gid,
            "SLOPLEX_PORT": port, "SLOPLEX_BIND_IP": "127.0.0.1",
            "SLOPLEX_CONFIG": config, "SLOPLEX_STATE": work / "state",
            "SLOPLEX_SCRATCH": work / "scratch", "SLOPLEX_MEDIA": work / "media",
        }
        env = work / "env"
        env.write_text("".join(f"{key}={value}\n" for key, value in settings.items()))
        compose = ["docker", "compose", "--env-file", str(env), "-p", work.name,
                   "-f", str(ROOT / "deploy/compose.yaml")]
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
            print("Container smoke passed: health, non-root, read-only media/root, isolated state/scratch, CPU encode/probe, web, auth, owner bootstrap/login, restart persistence.")
        finally:
            command(*compose, "down", "--volumes", check=False)


if __name__ == "__main__":
    main()
