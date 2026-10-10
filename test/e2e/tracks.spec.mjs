// Audio/subtitle pickers: sidecar and embedded text subtitles as tracks,
// a non-first audio track switching to a transcode, persisted choices.
import { chromium, Suite, login, openBySearch, playButton } from "./lib.mjs";

const s = new Suite("tracks");
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
s.watch(page, (status, path) => status === 401 && path === "/sloplex/auth/login");
const streams = [];
page.on("response", (r) => {
  const u = new URL(r.url());
  if (/start\.(mp4|webm)$/.test(u.pathname)) streams.push({ status: r.status(), q: Object.fromEntries(u.searchParams) });
});

await login(page);
await openBySearch(page, "tracks", "Tracks");
await page.waitForSelector("select[aria-label=Subtitles]");
const audioOpts = await page.locator("select[aria-label=Audio] option").allTextContents();
const subOpts = await page.locator("select[aria-label=Subtitles] option").allTextContents();
s.check(audioOpts.length === 2 && audioOpts[1].includes("Commentary"), `audio picker: ${JSON.stringify(audioOpts)}`);
s.check(JSON.stringify(subOpts) === JSON.stringify(["No subtitles", "English (VTT)", "Spanish (SRT)"]), `subtitle picker: ${JSON.stringify(subOpts)}`);

// Spanish sidecar as a showing text track, direct play.
await page.selectOption("select[aria-label=Subtitles]", { label: "Spanish (SRT)" });
await page.waitForTimeout(300);
await page.click(playButton);
await page.waitForFunction(() => {
  const v = document.querySelector("video");
  const t = v && v.textTracks[0];
  return v && v.currentTime > 1 && t && t.mode === "showing" && t.cues && [...t.cues].some((c) => c.text.includes("Hola"));
}, null, { timeout: 15000 });
s.check(streams.length === 0, "direct play kept for the first audio track and a text subtitle");
await s.shot(page, "subtitles");

// Switching to the embedded English subtitle replaces the track.
await page.selectOption("select[aria-label=Subtitles]", { label: "English (VTT)" });
await page.waitForFunction(() => {
  const t = document.querySelector("video")?.textTracks[0];
  return t && t.cues && [...t.cues].some((c) => c.text.includes("embedded track"));
}, null, { timeout: 15000 });
s.check(true, "embedded subtitle extracted and shown after switching");

// A non-first audio track needs a transcode carrying the choice.
await page.selectOption("select[aria-label=Audio]", { index: 1 });
await page.waitForFunction(() => /start\.(mp4|webm)/.test(document.querySelector("video")?.src || ""), null, { timeout: 15000 });
await page.waitForTimeout(1500);
const req = streams[streams.length - 1];
s.check(req && req.status === 200 && req.q.audioStreamID && Number(req.q.offset) > 0.5,
  `transcode requested with audioStreamID at the current position: ${JSON.stringify(req && { ...req.q, "X-Plex-Token": undefined })}`);

// Choices persist for the account.
await page.reload();
await page.waitForSelector("select[aria-label=Subtitles]");
const selected = await page.evaluate(() => [...document.querySelectorAll("select")].map((x) => x.selectedOptions[0].textContent));
s.check(selected.includes("English (VTT)") && selected.some((t) => t.includes("Commentary")), `choices persisted: ${JSON.stringify(selected)}`);

await browser.close();
s.finish();
