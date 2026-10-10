// Shared helpers for the browser end-to-end suites. See README.md.
import { createRequire } from "node:module";

async function loadPlaywright() {
  try {
    return await import("playwright");
  } catch {
    // Fall back to an explicit path (PW) or a global install.
    const require = createRequire(import.meta.url);
    return require(process.env.PW || "playwright");
  }
}

export const { chromium } = await loadPlaywright();
export const base = process.env.BASE || "http://127.0.0.1:32497";
export const shots = process.env.SHOTS || "";
export const ownerPassword = process.env.OWNER_PASSWORD || "owner-password";

// Suite collects failures; every check logs, and finish() exits non-zero
// if anything failed.
export class Suite {
  constructor(name) {
    this.name = name;
    this.problems = [];
  }

  check(cond, msg) {
    if (cond) console.log(`  ok: ${msg}`);
    else this.problems.push(`FAIL: ${msg}`);
  }

  // expectDialog lets the next dialog through, accepting it or not.
  expectDialog(accept = true) {
    (this.dialogs ??= []).push(accept);
  }

  // watch records page errors, console errors, unexpected dialogs and HTTP
  // errors. allow(status, path) whitelists expected failures.
  watch(page, allow = () => false) {
    page.on("pageerror", (e) => this.problems.push(`pageerror: ${e.message}`));
    page.on("console", (m) => {
      if ((m.type() === "error" || m.type() === "warning") && !m.text().startsWith("Failed to load resource")) {
        this.problems.push(`console ${m.type()}: ${m.text()}`);
      }
    });
    page.on("dialog", (d) => {
      if (this.dialogs?.length) return this.dialogs.shift() ? d.accept() : d.dismiss();
      this.problems.push(`unexpected dialog: ${d.message()}`);
      d.dismiss();
    });
    page.on("response", (r) => {
      const path = new URL(r.url()).pathname;
      if (r.status() >= 400 && !allow(r.status(), path)) this.problems.push(`HTTP ${r.status()} ${path}`);
    });
  }

  async shot(page, name) {
    if (shots) await page.screenshot({ path: `${shots}/${this.name}-${name}.png` });
  }

  finish() {
    if (this.problems.length) {
      console.log(`\n${this.name}: ${this.problems.length} problem(s)\n  ` + this.problems.join("\n  "));
      process.exit(1);
    }
    console.log(`${this.name}: all checks passed`);
  }
}

export async function login(page, user = "owner", password = ownerPassword) {
  await page.goto(base + "/web/");
  await page.fill("input[name=username]", user);
  await page.fill("input[name=password]", password);
  await page.click("button[type=submit]");
  await page.waitForSelector("h1:text('Library')");
}

export async function openBySearch(page, query, title) {
  await page.fill("#search-input", query);
  await page.press("#search-input", "Enter");
  await page.click(`a.tile:has-text('${title}')`);
}

export const playButton = "button:text('Play'), button:text('Resume')";
