// Transcode fallback: a file the browser cannot decode switches to a
// transcoded stream at the resume position.
import { chromium, Suite, login, openBySearch, playButton } from "./lib.mjs";

const s = new Suite("transcode");
const browser = await chromium.launch();

async function open(fakeH264) {
  const page = await browser.newPage();
  if (fakeH264) {
    await page.addInitScript(() => {
      const orig = HTMLMediaElement.prototype.canPlayType;
      HTMLMediaElement.prototype.canPlayType = function (t) { return t.includes("avc1") ? "probably" : orig.call(this, t); };
    });
  }
  s.watch(page, (status, path) => status === 401 && path === "/sloplex/auth/login");
  const streams = [];
  page.on("response", (r) => { if (r.url().includes("start.mp4")) streams.push({ status: r.status(), offset: new URL(r.url()).searchParams.get("offset") }); });
  await login(page);
  await openBySearch(page, "legacy", "Legacy");
  await page.click(playButton);
  return { page, streams };
}

{ // A browser without H.264 is told so, without a wasted transcode.
  const { page, streams } = await open(false);
  await page.waitForSelector("p.error:has-text('cannot play H.264')");
  s.check(streams.length === 0, "browser without H.264 gets a clear message and no wasted transcode");
  await page.close();
}
{ // With H.264 reported, the player falls back to start.mp4.
  const { page, streams } = await open(true);
  await page.waitForFunction(() => document.querySelector("video")?.src.includes("start.mp4"), null, { timeout: 15000 });
  await page.waitForTimeout(1500);
  s.check(streams.length >= 1 && streams[0].status === 200, `fell back to start.mp4 and the server streamed it (${JSON.stringify(streams)})`);
  await page.close();
}

await browser.close();
s.finish();
