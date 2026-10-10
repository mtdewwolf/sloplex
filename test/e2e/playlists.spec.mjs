// Playlists: create one, add movies from their pages, reorder, play
// through to the next item, and remove an entry.
import { chromium, base, Suite, login, openBySearch } from "./lib.mjs";

const s = new Suite("playlists");
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
s.watch(page, (status, path) => status === 401 && path === "/sloplex/auth/login");

await login(page);
await page.click("nav a:text('Playlists')");
await page.waitForSelector("h1:text('Playlists')");
s.check(await page.isVisible("text=No playlists yet"), "empty playlists page explains what to do");
await page.fill("input[name=title]", "Movie Night");
await page.click("button:text('Create')");
await page.waitForSelector("a:text('Movie Night')");

// Add Big Buck, then Sintel, from their pages.
for (const [query, title] of [["big buck", "Big Buck"], ["sintel", "Sintel"]]) {
  await openBySearch(page, query, title);
  await page.waitForSelector(`h1:text('${title}')`);
  await page.waitForFunction(() => document.querySelectorAll("select[aria-label='Add to playlist'] option").length > 2);
  await page.selectOption("select[aria-label='Add to playlist']", { label: "Movie Night" });
  await page.waitForSelector("#toast:text('Added to Movie Night')");
}

await page.goto(base + "/web/#/playlists");
await page.waitForSelector("a:text('Movie Night')");
const row = await page.locator("table.playlists tr:has-text('Movie Night') td").allTextContents();
s.check(row[1] === "2" && row[2] === "25 s", `playlist row shows 2 items, 25 s: ${JSON.stringify(row)}`);
await page.click("a:text('Movie Night')");
await page.waitForSelector("h1:text('Movie Night')");
const order = () => page.locator(".playlist-items li a").allTextContents();
s.check(JSON.stringify(await order()) === JSON.stringify(["Big Buck (2008)", "Sintel (2010)"]), `items in the order added: ${JSON.stringify(await order())}`);

// Move Sintel (5 s) to the top, so playing through is quick.
await page.click(".playlist-items li:nth-child(2) button[aria-label='Move up']");
await page.waitForFunction(() => document.querySelector(".playlist-items li a")?.textContent === "Sintel (2010)");
s.check(true, "moving an item up reorders the playlist");
await s.shot(page, "playlist");

// Play all: Sintel plays, then Big Buck starts on its own.
await page.click("button:text('Play all')");
await page.waitForSelector("text=Playing 1 of 2: Sintel");
await page.waitForFunction(() => document.querySelector("video")?.currentTime > 1, null, { timeout: 15000 });
const firstSrc = await page.evaluate(() => document.querySelector("video").src);
await page.waitForSelector("text=Playing 2 of 2: Big Buck", { timeout: 20000 });
await page.waitForFunction((first) => {
  const v = document.querySelector("video");
  return v && v.src !== first && v.currentTime > 0.5;
}, firstSrc, { timeout: 15000 });
s.check(await page.isVisible(".playlist-items li.current a:text('Big Buck (2008)')"), "the next item plays when the first ends, and is highlighted");

// Remove Big Buck.
await page.click(".playlist-items li:nth-child(2) button[aria-label='Remove']");
await page.waitForFunction(() => document.querySelectorAll(".playlist-items li").length === 1);
s.check(await page.isVisible("text=1 items · 5 s"), "removing an entry updates the playlist");

await browser.close();
s.finish();
