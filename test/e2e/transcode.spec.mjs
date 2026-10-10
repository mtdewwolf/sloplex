// Converting: a file the browser cannot decode switches to a converted
// stream (WebM here, as Playwright's Chromium has no H.264), with controls
// that span the whole item and seek by restarting the stream.
import { chromium, Suite, login, openBySearch, playButton } from "./lib.mjs";

const s = new Suite("transcode");
const browser = await chromium.launch();

// open plays Legacy (MPEG-4 Part 2, 30 s). codecs overrides canPlayType:
// "h264" claims H.264 support, "none" denies H.264 and VP9.
async function open(codecs) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  if (codecs) {
    await page.addInitScript((codecs) => {
      const orig = HTMLMediaElement.prototype.canPlayType;
      HTMLMediaElement.prototype.canPlayType = function (t) {
        if (codecs === "h264" && t.includes("avc1")) return "probably";
        if (codecs === "none" && (t.includes("avc1") || t.includes("vp9"))) return "";
        return orig.call(this, t);
      };
    }, codecs);
  }
  s.watch(page, (status, path) => status === 401 && path === "/sloplex/auth/login");
  const streams = [];
  const timeline = [];
  page.on("response", (r) => {
    const u = new URL(r.url());
    const m = u.pathname.match(/start\.(mp4|webm)$/);
    if (m) streams.push({ format: m[1], status: r.status(), offset: Number(u.searchParams.get("offset")) });
    if (u.pathname === "/:/timeline") timeline.push({ state: u.searchParams.get("state"), time: Number(u.searchParams.get("time")) });
  });
  await login(page);
  await openBySearch(page, "legacy", "Legacy");
  await page.click(playButton);
  return { page, streams, timeline };
}

// state reads the player: source position, pause state and the controls.
const state = (page) => page.evaluate(() => {
  const v = document.querySelector(".player video");
  const bar = document.querySelector(".player .seek");
  return {
    src: v.src, paused: v.paused, time: v.currentTime, native: v.controls,
    bar: bar && Number(bar.value), max: bar && Number(bar.max),
    clock: document.querySelector(".player .clock")?.textContent,
    loading: !document.querySelector(".player .loading")?.hidden,
  };
});

// seekBar drags the seek bar to t seconds and lets go.
const seekBar = (page, t) => page.evaluate((t) => {
  const bar = document.querySelector(".player .seek");
  bar.value = t;
  bar.dispatchEvent(new Event("input", { bubbles: true }));
  bar.dispatchEvent(new Event("change", { bubbles: true }));
}, t);

// clockAt waits until the clock shows a source time within [lo, hi] seconds
// while the video has really advanced past its stream start.
const clockAt = (page, lo, hi) => page.waitForFunction(([lo, hi]) => {
  const m = document.querySelector(".player .clock")?.textContent.match(/^(\d+):(\d\d) \//);
  const v = document.querySelector(".player video");
  if (!m || !v || v.readyState < 2) return false;
  const t = Number(m[1]) * 60 + Number(m[2]);
  return t >= lo && t <= hi;
}, [lo, hi], { timeout: 20000 });

{ // No H.264: the player converts to WebM and plays it with its own controls.
  const { page, streams, timeline } = await open(null);
  await page.waitForFunction(() => {
    const v = document.querySelector(".player video");
    return v && v.src.includes("start.webm") && v.currentTime > 1;
  }, null, { timeout: 20000 });
  let st = await state(page);
  s.check(streams.length === 1 && streams[0].status === 200 && streams[0].format === "webm" && streams[0].offset === 0,
    `fell back to a WebM conversion from the start: ${JSON.stringify(streams)}`);
  s.check(!st.native && st.max >= 29 && st.max <= 31 && /\/ 0:30$/.test(st.clock),
    `custom controls span the whole item: ${JSON.stringify(st)}`);
  await s.shot(page, "converted");

  // Dragging only moves the bar; letting go restarts the stream there.
  await seekBar(page, 20);
  await page.waitForFunction(() => document.querySelector(".player video").src.includes("offset=20.0"), null, { timeout: 5000 });
  await clockAt(page, 20, 29);
  st = await state(page);
  s.check(streams.length === 2 && streams[1].offset === 20 && st.bar >= 20,
    `seek bar restarts the conversion at 20 s: ${JSON.stringify(streams.slice(1))} ${st.clock}`);

  // Pausing reports the source position, not the stream position.
  await page.click(".player button.play");
  await page.waitForFunction(() => document.querySelector(".player video").paused);
  await page.waitForTimeout(300);
  const paused = timeline.filter((t) => t.state === "paused").pop();
  s.check(paused && paused.time >= 20000, `paused timeline reports source time: ${JSON.stringify(paused)}`);

  // Keys: ArrowLeft twice is one debounced restart 20 s back, still paused.
  // (Chromium cannot seek inside a converted stream, so every seek restarts.)
  await page.focus(".player");
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("ArrowLeft");
  await page.waitForFunction(() => document.querySelector(".player video").src.includes("offset=0.0"), null, { timeout: 5000 });
  await page.waitForTimeout(800);
  st = await state(page);
  s.check(streams.length === 3 && streams[2].offset === 0 && st.paused && /^0:00 \//.test(st.clock),
    `two ArrowLeft presses make one restart 20 s back and keep the pause: ${JSON.stringify(streams.slice(2))} ${JSON.stringify(st)}`);

  // Space resumes; End jumps near the end and plays to it.
  await page.keyboard.press(" ");
  await page.waitForFunction(() => !document.querySelector(".player video").paused);
  await page.keyboard.press("End");
  await page.waitForFunction(() => document.querySelector(".player video").ended, null, { timeout: 20000 });
  await page.waitForTimeout(300);
  const stopped = timeline.filter((t) => t.state === "stopped").pop();
  s.check(streams[streams.length - 1].offset === 29 && stopped && stopped.time >= 29000 && stopped.time <= 30500,
    `End plays out the last second and reports it: ${JSON.stringify(streams.slice(3))} ${JSON.stringify(stopped)}`);
  await page.close();
}
{ // With H.264 reported, the conversion is MP4.
  const { page, streams } = await open("h264");
  await page.waitForFunction(() => document.querySelector("video")?.src.includes("start.mp4"), null, { timeout: 15000 });
  await page.waitForTimeout(1500);
  s.check(streams.length >= 1 && streams[0].status === 200 && streams[0].format === "mp4",
    `H.264 browsers get start.mp4: ${JSON.stringify(streams)}`);
  await page.close();
}
{ // Neither H.264 nor VP9: a clear message and no wasted conversion.
  const { page, streams } = await open("none");
  await page.waitForSelector("p.error:has-text('cannot play the H.264 or VP9')");
  s.check(streams.length === 0, "browser without H.264 or VP9 gets a clear message and no conversion");
  await page.close();
}

await browser.close();
s.finish();
