# Browser end-to-end tests

These suites drive the built-in web app (`/web/`) in headless Chromium,
against a freshly built server and generated test media.

| Suite | Covers |
|---|---|
| `web` | Sign-in errors, poster grid, playback and resume to On Deck, search, the admin page, a Kids-only account, markup in file names rendered as text |
| `tracks` | Audio and subtitle pickers, sidecar and embedded subtitles as text tracks, a transcode for a non-first audio track, persisted choices |
| `transcode` | Fallback from undecodable files to a transcoded stream, and the message for browsers without H.264 |

Every suite fails on page errors, console errors, unexpected dialogs, and
unexpected HTTP errors, not only on its own checks.

## Running

You need Go, ffmpeg/ffprobe (with libvpx and libopus), Node 18+, and
Playwright with Chromium:

```sh
cd test/e2e
npm install
npx playwright install chromium
./run.sh              # all suites
./run.sh tracks web   # some suites
```

`run.sh` does the following:

- builds the server;
- generates test media once into `$E2E_WORK/media` (default
  `/tmp/sloplex-e2e`);
- runs each suite against its own fresh data directory on port `$E2E_PORT`
  (default 32497).

Screenshots and server logs go to `$E2E_WORK`. To use a Playwright installed
elsewhere, set `PW` to its path.

Playwright's bundled Chromium has no H.264 decoder, so the suites use
VP9/WebM media where real playback matters.
