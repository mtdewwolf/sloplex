// Core web app flows: sign-in, library, playback and resume, search, admin,
// restricted accounts, and safe rendering of markup in file names.
import { chromium, base, Suite, login, playButton } from "./lib.mjs";

const s = new Suite("web");
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
s.watch(page, (status, path) => status === 401 && path === "/sloplex/auth/login");

// Sign-in errors, then the owner's library.
await page.goto(base + "/web/");
await page.fill("input[name=username]", "owner");
await page.fill("input[name=password]", "wrong-password");
await page.click("button[type=submit]");
await page.waitForSelector(".error:not(:empty)");
s.check((await page.textContent(".error")).includes("invalid"), "wrong password shows an error");
await login(page);
s.check(await page.isVisible("#nav-admin"), "admin nav visible for owner");
await page.waitForFunction(() => [...document.querySelectorAll(".tile img")].some((i) => i.complete && i.naturalWidth > 0));
s.check((await page.locator(".tile").count()) >= 4, "recently added shows poster tiles");
await s.shot(page, "library");

// Play a WebM movie, pause, and see it On Deck with progress.
await page.click("a.tile:has-text('Big Buck')");
await page.waitForSelector("h1:text('Big Buck')");
await page.click(playButton);
await page.waitForFunction(() => document.querySelector("video")?.currentTime > 2, null, { timeout: 15000 });
await page.evaluate(() => document.querySelector("video").pause());
await page.waitForTimeout(500);
await page.goto(base + "/web/#/library");
await page.waitForSelector("h2:text('On Deck')");
s.check(await page.isVisible(".tile:has-text('Big Buck') .progress"), "paused movie appears On Deck with a progress bar");

// Search by prefix.
await page.fill("#search-input", "sint");
await page.press("#search-input", "Enter");
await page.waitForSelector("h2:text('Movies')");
s.check(await page.isVisible(".tile:has-text('Sintel')"), "search finds Sintel by prefix");

// .nfo metadata on the detail page; markup in it renders as text.
await page.click("a.tile:has-text('Sintel')");
await page.waitForSelector("h1:text('Sintel')");
const meta = await page.evaluate(() => ({
  summary: document.querySelector(".summary")?.textContent,
  tagline: document.querySelector(".tagline")?.textContent,
  facts: [...document.querySelectorAll(".detail dd")].map((d) => d.textContent),
  cast: document.querySelector(".cast li")?.textContent,
  bold: !!document.querySelector(".summary b"),
}));
s.check(meta.summary === "<b>Open</b> movie by the Blender Institute." && !meta.bold && meta.tagline === "A girl and her dragon" &&
  ["PG", "7.5 / 10", "Animation, Fantasy", "Colin Levy"].every((f) => meta.facts.includes(f)) && meta.cast === "Halina Reijn as Sintel",
  `nfo metadata shown as text: ${JSON.stringify(meta)}`);
await s.shot(page, "nfo");

// The .nfo set becomes a collection on the section page, in release order.
await page.click("#nav-library, nav a:text('Library')");
await page.click("a:has(button:text('Movies'))");
await page.waitForSelector("h2:text('Collections')");
await page.click("a.tile:has-text('Blender Open Movies')");
await page.waitForSelector("h1:text('Blender Open Movies')");
const members = await page.locator(".tile .title").allTextContents();
s.check(JSON.stringify(members) === JSON.stringify(["Big Buck", "Sintel"]) && await page.isVisible("h2:text('Movies')"),
  `collection page lists its movies in release order: ${JSON.stringify(members)}`);
await s.shot(page, "collection");

// Admin: a kid account limited to the Kids library.
await page.click("#nav-admin");
await page.waitForSelector("h1:text('Admin')");
const transcoder = await page.textContent("#transcoder");
s.check(transcoder.includes("up to") && transcoder.includes("HDR tone mapping on the CPU"), `admin page shows the transcoder: ${transcoder}`);
await page.click("button:text('New account')");
await page.fill("#admin-panel input:not([type])", "kid");
await page.fill("#admin-panel input[type=password]", "cartoons1");
await page.uncheck("#admin-panel .libs label:has-text('All libraries') input");
await page.check("#admin-panel .libs label:has-text('Kids') input");
await page.click("#admin-panel button:text('Create')");
await page.waitForSelector("td:text('kid')");
s.check(await page.isVisible("tr:has-text('kid') td:text('Kids')"), "kid listed with Kids-only access");
await page.click("button:text('New account')");
await page.fill("#admin-panel input:not([type])", "bad name!");
s.check(await page.evaluate(() => !document.querySelector("#admin-panel input:not([type])").checkValidity()), "invalid account name fails form validation");
await page.click("#admin-panel button:text('Cancel')");

// A title containing markup (from a file name) renders as text.
await page.fill("#search-input", "img");
await page.press("#search-input", "Enter");
await page.waitForSelector("h2:text('Movies')");
const shown = await page.textContent(".tile .title");
s.check(shown === "<img src=x onerror=alert(1)>", `markup title rendered as text (got ${JSON.stringify(shown)})`);
s.check((await page.locator("main img[src='x']").count()) === 0, "no injected <img> element");

// The kid sees only Kids, and search cannot find hidden movies.
await page.click("#signout");
await page.waitForSelector("input[name=username]");
await login(page, "kid", "cartoons1");
s.check(!(await page.isVisible("#nav-admin")), "no admin nav for kid");
const sections = await page.locator("main .row button").allTextContents();
s.check(JSON.stringify(sections) === JSON.stringify(["Kids"]), `kid sees only Kids (got ${JSON.stringify(sections)})`);
await page.fill("#search-input", "big");
await page.press("#search-input", "Enter");
await page.waitForSelector("p:text('No matches.')");
s.check(true, "kid search cannot find hidden movies");
await page.click("nav a:text('Account')");
await page.waitForSelector("h1:text('Account: kid')");
s.check((await page.locator("table tr").count()) === 2, "kid has one signed-in device");

await browser.close();
s.finish();
