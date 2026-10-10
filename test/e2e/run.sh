#!/usr/bin/env bash
# Runs the browser end-to-end suites against a freshly built server.
# Usage: test/e2e/run.sh [suite...]   (default: all *.spec.mjs)
# Needs: go, ffmpeg/ffprobe, node, and Playwright with Chromium. See README.md.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
work="${E2E_WORK:-${TMPDIR:-/tmp}/sloplex-e2e}"
port="${E2E_PORT:-32497}"
media="$work/media"
mkdir -p "$work" "$media"

log() { printf '\n== %s\n' "$*"; }
ff() { ffmpeg -nostdin -v error -y "$@"; }

log "building"
(cd "$repo" && go build -o "$work/sloplex" ./cmd/sloplex)

# Test media is generated once and reused between runs.
if [ ! -f "$media/.done" ]; then
  log "generating test media in $media"
  rm -rf "$media" && mkdir -p "$media"
  m="$media/movies" tv="$media/tv" kids="$media/kids"
  mkdir -p "$m/Big Buck (2008)" "$m/Sintel (2010)" "$m/Legacy (1999)" "$m/Tracks (2021)" \
    "$tv/Lost (2004)/Season 01" "$kids/Bluey (2018)/Season 01"
  ff -f lavfi -i "testsrc2=size=640x360:duration=20:rate=24" -f lavfi -i "sine=f=440:duration=20" \
    -c:v libvpx-vp9 -b:v 300k -c:a libopus -shortest "$m/Big Buck (2008)/Big Buck (2008).webm"
  ff -f lavfi -i "smptebars=size=640x360:duration=5:rate=24" -c:v libvpx-vp9 "$m/Sintel (2010)/Sintel (2010).webm"
  cp "$m/Sintel (2010)/Sintel (2010).webm" "$m/<img src=x onerror=alert(1)> (2020).webm"
  # MPEG-4 Part 2 + MP2: no browser decodes it, so it needs a transcode.
  ff -f lavfi -i "testsrc=size=640x360:rate=24:duration=30" -f lavfi -i "sine=duration=30" -shortest \
    -c:v mpeg4 -c:a mp2 "$m/Legacy (1999)/Legacy (1999).mkv"
  # Two audio tracks, an embedded WebVTT subtitle and a Spanish sidecar.
  printf 'WEBVTT\n\n00:00:01.000 --> 00:00:09.000\nHello from the embedded track\n' > "$work/en.vtt"
  printf '1\n00:00:00,500 --> 00:00:09,000\nHola desde el archivo\n' > "$m/Tracks (2021)/Tracks (2021).es.srt"
  ff -f lavfi -i "testsrc2=s=640x360:r=24:d=12" -f lavfi -i "sine=f=440:d=12" -f lavfi -i "sine=f=660:d=12" -i "$work/en.vtt" \
    -map 0 -map 1 -map 2 -map 3 -c:v libvpx-vp9 -b:v 300k -c:a libopus -c:s webvtt \
    -metadata:s:a:0 language=eng -metadata:s:a:1 language=fre -metadata:s:a:1 title=Commentary \
    -metadata:s:s:0 language=eng -disposition:s:0 0 "$m/Tracks (2021)/Tracks (2021).webm"
  for d in "$m/Big Buck (2008)" "$m/Sintel (2010)" "$tv/Lost (2004)" "$kids/Bluey (2018)"; do
    ff -f lavfi -i "color=c=0x3366aa:size=400x600" -frames:v 1 "$d/poster.jpg"
  done
  ff -f lavfi -i "testsrc=size=640x360:duration=4:rate=10" -c:v libvpx-vp9 "$tv/Lost (2004)/Season 01/Lost - S01E01 - Pilot.webm"
  ff -f lavfi -i "testsrc=size=640x360:duration=4:rate=10" -c:v libvpx-vp9 "$kids/Bluey (2018)/Season 01/Bluey - S01E01 - Magic Xylophone.webm"
  touch "$media/.done"
fi
leaves=7 # movies and episodes above

suites=("$@")
if [ ${#suites[@]} -eq 0 ]; then
  suites=()
  for f in "$here"/*.spec.mjs; do suites+=("$(basename "$f" .spec.mjs)"); done
fi

failed=()
for suite in "${suites[@]}"; do
  log "suite: $suite"
  data="$work/data-$suite"
  rm -rf "$data"
  cat > "$work/config.json" <<JSON
{"friendlyName":"e2e","listen":"127.0.0.1:$port","dataDir":"$data","discovery":false,
 "libraries":[{"title":"Movies","type":"movie","paths":["$media/movies"]},
              {"title":"TV","type":"show","paths":["$media/tv"]},
              {"title":"Kids","type":"show","paths":["$media/kids"]}]}
JSON
  "$work/sloplex" -config "$work/config.json" > "$work/server-$suite.log" 2>&1 &
  pid=$!
  trap 'kill $pid 2>/dev/null || true' EXIT
  for _ in $(seq 100); do curl -sf "localhost:$port/identity" >/dev/null && break; sleep 0.1; done
  token="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['token'])" "$data/identity.json")"
  for _ in $(seq 300); do
    n="$(curl -sf -H "X-Plex-Token: $token" -H 'Accept: application/json' "localhost:$port/library/recentlyAdded" |
      python3 -c 'import json,sys;print(json.load(sys.stdin)["MediaContainer"]["size"])' 2>/dev/null || echo 0)"
    [ "$n" -ge "$leaves" ] && break
    sleep 0.1
  done
  curl -sf -X PUT -H "X-Plex-Token: $token" -d '{"new":"owner-password"}' "localhost:$port/sloplex/account/password"
  mkdir -p "$work/shots"
  if ! BASE="http://127.0.0.1:$port" SHOTS="$work/shots" node "$here/$suite.spec.mjs"; then
    failed+=("$suite")
    echo "server log: $work/server-$suite.log"
  fi
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  trap - EXIT
done

if [ ${#failed[@]} -gt 0 ]; then
  log "FAILED: ${failed[*]}"
  exit 1
fi
log "all suites passed (screenshots in $work/shots)"
