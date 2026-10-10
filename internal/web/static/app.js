// Sloplex web app. A plain client of the documented API; see
// docs/spec/web.md. All server-provided text is inserted with textContent.
"use strict";

const TOKEN_KEY = "sloplex.token";
const $ = (sel) => document.querySelector(sel);

const state = {
  token: null,
  me: null,
  sections: [],
};

try { state.token = localStorage.getItem(TOKEN_KEY); } catch { /* storage blocked */ }

function saveToken(tok) {
  state.token = tok;
  try {
    if (tok) localStorage.setItem(TOKEN_KEY, tok);
    else localStorage.removeItem(TOKEN_KEY);
  } catch { /* storage blocked */ }
}

// ---- DOM helpers -----------------------------------------------------------

// el builds an element. attrs: class, text, on<event>, hidden, or any
// attribute. Children may be nodes, strings (as text) or null.
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") node.className = v;
    else if (k === "text") node.textContent = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else if (k === "value") node.value = v;
    // CSSOM, not the style attribute, so the CSP needs no 'unsafe-inline'.
    else if (k === "style") node.style.cssText = v;
    else if (k === "checked") node.checked = !!v;
    else node.setAttribute(k, v === true ? "" : String(v));
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

// render replaces the page content. Nested arrays are flattened and empty
// values dropped, like el's children.
function render(...nodes) {
  $("#app").replaceChildren(...nodes.flat(Infinity).filter((n) => n !== null && n !== undefined && n !== false));
}

let toastTimer;
function toast(msg, bad = false) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = bad ? "bad" : "";
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

// ---- API -------------------------------------------------------------------

class APIError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

async function api(method, path, body) {
  const headers = { Accept: "application/json" };
  if (state.token) headers["X-Plex-Token"] = state.token;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: "omit",
  });
  if (res.status === 401 && path !== "/sloplex/auth/login") {
    signedOut();
    throw new APIError(401, "Please sign in again.");
  }
  const type = res.headers.get("Content-Type") || "";
  const data = type.includes("application/json") ? await res.json().catch(() => null) : null;
  if (!res.ok) {
    const msg = (data && data.error) || (await res.text().catch(() => "")) || res.statusText;
    throw new APIError(res.status, msg.trim() || `HTTP ${res.status}`);
  }
  return data;
}

const container = (data) => (data && data.MediaContainer) || {};

// withToken adds the token to a media or image URL, which cannot carry
// headers.
function withToken(path) {
  const u = new URL(path, location.origin);
  u.searchParams.set("X-Plex-Token", state.token || "");
  return u.pathname + u.search;
}

function posterURL(thumb, w = 300, h = 450) {
  if (!thumb) return null;
  const q = new URLSearchParams({ width: w, height: h, minSize: 1, url: thumb });
  return withToken("/photo/:/transcode?" + q);
}

function fmtDuration(ms) {
  if (!ms) return "";
  if (ms < 60000) return `${Math.round(ms / 1000)} s`;
  const m = Math.round(ms / 60000);
  return m >= 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m} min`;
}

// fmtClock formats a position as h:mm:ss or m:ss.
function fmtClock(ms) {
  const t = Math.floor(ms / 1000);
  const h = Math.floor(t / 3600), m = Math.floor((t % 3600) / 60), s = t % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return h ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

function fmtDate(sec) {
  return sec ? new Date(sec * 1000).toLocaleString() : "";
}

// ---- Session ---------------------------------------------------------------

function signedOut() {
  saveToken(null);
  state.me = null;
  $("#top").hidden = true;
  if (location.hash !== "#/login") location.hash = "#/login";
  else route();
}

async function loadSession() {
  state.me = await api("GET", "/sloplex/account");
  const secs = container(await api("GET", "/library/sections"));
  state.sections = secs.Directory || [];
  $("#top").hidden = false;
  $("#whoami").textContent = state.me.name + (state.me.admin ? " (admin)" : "");
  $("#nav-admin").hidden = !state.me.admin;
}

function viewLogin() {
  $("#top").hidden = true;
  const err = el("p", { class: "error" });
  const user = el("input", { name: "username", autocomplete: "username", required: true, autofocus: true });
  const pass = el("input", { name: "password", type: "password", autocomplete: "current-password", required: true });
  const form = el("form", {
    class: "stack",
    onsubmit: async (e) => {
      e.preventDefault();
      err.textContent = "";
      try {
        const res = await api("POST", "/sloplex/auth/login", {
          username: user.value.trim(),
          password: pass.value,
          device: "Web browser",
        });
        saveToken(res.token.token);
        await loadSession();
        location.hash = "#/library";
      } catch (ex) {
        err.textContent = ex.message;
        pass.select();
      }
    },
  },
  el("label", {}, "Username", user),
  el("label", {}, "Password", pass),
  err,
  el("button", { class: "primary", type: "submit", text: "Sign in" }));
  render(el("div", { class: "card-panel login" }, el("h1", { text: "Sloplex" }), form));
}

// ---- Library views ---------------------------------------------------------

function tile(item) {
  const leaf = item.type === "movie" || item.type === "episode";
  const wide = item.type === "episode";
  const img = wide
    ? (item.thumb ? withToken(item.thumb) : null)
    : posterURL(item.thumb);
  const art = el("div", { class: "art" },
    img ? el("img", { src: img, alt: "", loading: "lazy" }) : item.title);
  if (leaf && item.viewCount > 0) art.append(el("span", { class: "badge", text: "✓" }));
  if (!leaf && item.leafCount > 0 && item.viewedLeafCount < item.leafCount) {
    art.append(el("span", { class: "badge", text: String(item.leafCount - item.viewedLeafCount) }));
  }
  if (leaf && item.viewOffset > 0 && item.duration > 0) {
    const pct = Math.min(100, (100 * item.viewOffset) / item.duration);
    art.append(el("div", { class: "progress" }, el("span", { style: `width:${pct.toFixed(1)}%` })));
  }
  let sub = item.year ? String(item.year) : "";
  if (item.type === "episode") {
    sub = `${item.grandparentTitle || ""} · S${item.parentIndex}E${item.index}`;
  } else if (item.type === "season") {
    sub = `${item.leafCount || 0} episodes`;
  }
  return el("a", { class: "tile" + (wide ? " wide" : ""), href: `#/item/${item.ratingKey}` },
    art,
    el("div", { class: "title", text: item.title, title: item.title }),
    el("div", { class: "sub", text: sub }));
}

function grid(items) {
  if (!items || items.length === 0) return el("p", { class: "muted", text: "Nothing here yet." });
  return el("div", { class: "grid" }, items.map(tile));
}

async function viewLibrary() {
  const onDeck = container(await api("GET", "/library/onDeck?X-Plex-Container-Size=12")).Metadata;
  const recent = container(await api("GET", "/library/recentlyAdded?X-Plex-Container-Size=18")).Metadata;
  const sections = el("div", { class: "row" },
    state.sections.map((s) => el("a", { href: `#/section/${s.key}` },
      el("button", { type: "button", text: s.title }))));
  render(
    el("h1", { text: "Library" }),
    state.sections.length ? sections : el("p", { class: "muted", text: "No libraries are available to you." }),
    onDeck && onDeck.length ? [el("h2", { text: "On Deck" }), grid(onDeck)] : null,
    el("h2", { text: "Recently Added" }), grid(recent),
  );
}

async function viewSection(id) {
  const mc = container(await api("GET", `/library/sections/${encodeURIComponent(id)}/all`));
  render(el("h1", { text: mc.librarySectionTitle || mc.title1 || "Library" }), grid(mc.Metadata));
}

async function viewItem(id) {
  const mc = container(await api("GET", `/library/metadata/${encodeURIComponent(id)}`));
  const item = (mc.Metadata || [])[0];
  if (!item) throw new APIError(404, "Not found");

  if (item.type === "show" || item.type === "season") {
    const kids = container(await api("GET", item.key)).Metadata || [];
    const heading = item.type === "season" ? `${item.parentTitle} · ${item.title}` : item.title;
    render(
      el("h1", { text: heading }),
      el("div", { class: "row" },
        watchButtons(item, () => viewItem(id))),
      el("h2", { text: item.type === "show" ? "Seasons" : "Episodes" }),
      grid(kids));
    return;
  }

  const media = (item.Media || [])[0];
  const part = media && (media.Part || [])[0];
  const facts = [];
  const add = (k, v) => { if (v) facts.push(el("dt", { text: k }), el("dd", { text: v })); };
  if (item.type === "episode") {
    add("Show", item.grandparentTitle);
    add("Episode", `Season ${item.parentIndex}, episode ${item.index}`);
  }
  add("Year", item.year && String(item.year));
  add("Duration", fmtDuration(item.duration));
  const res = { sd: "SD", "4k": "4K" }[media && media.videoResolution] || (media && media.videoResolution ? media.videoResolution + "p" : "");
  if (media) add("Video", [res, media.videoCodec, media.container].filter(Boolean).join(" · "));
  add("Last watched", fmtDate(item.lastViewedAt));
  if (part) add("File", part.file);

  const player = el("div");
  const art = item.type === "episode" ? item.grandparentThumb || item.thumb : item.thumb;
  const tracks = part ? trackPickers(part, () => restartPlayer()) : null;
  let active = null; // the running player, if any
  const restartPlayer = () => {
    if (active) active = play(player, item, part, tracks.choice(), active.position());
  };
  render(
    el("div", { class: "detail" },
      el("div", { class: "art" }, art ? el("img", { src: posterURL(art), alt: "" }) : null),
      el("div", {},
        el("h1", { text: item.title }),
        el("dl", {}, facts),
        tracks ? tracks.node : null,
        el("div", { class: "row" },
          part ? el("button", {
            class: "primary",
            type: "button",
            text: item.viewOffset > 0 ? `Resume from ${fmtClock(item.viewOffset)}` : "Play",
            onclick: () => { active = play(player, item, part, tracks.choice(), (item.viewOffset || 0) / 1000); },
          }) : null,
          watchButtons(item, () => viewItem(id))),
        player)));
}

// trackPickers builds the audio and subtitle selects of a part (see
// docs/spec/streams.md). Changes are saved as the account's choice.
function trackPickers(part, onChange) {
  const streams = part.Stream || [];
  const audio = streams.filter((s) => s.streamType === 2);
  const subs = streams.filter((s) => s.streamType === 3);
  const option = (s) => el("option", { value: s.id, selected: !!s.selected, text: s.extendedDisplayTitle });
  const audioSel = el("select", { "aria-label": "Audio" }, audio.map(option));
  const subSel = el("select", { "aria-label": "Subtitles" },
    el("option", { value: 0, text: "No subtitles", selected: !subs.some((s) => s.selected) }), subs.map(option));
  const save = async () => {
    const q = new URLSearchParams({ subtitleStreamID: subSel.value });
    if (audioSel.value) q.set("audioStreamID", audioSel.value);
    await api("PUT", `/library/parts/${part.id}?${q}`).catch(fail);
    onChange();
  };
  audioSel.addEventListener("change", save);
  subSel.addEventListener("change", save);
  const show = audio.length > 1 || subs.length > 0;
  return {
    node: show ? el("div", { class: "row tracks" },
      audio.length > 1 ? el("label", {}, "Audio ", audioSel) : null,
      el("label", {}, "Subtitles ", subSel)) : null,
    choice: () => ({
      audio: audio.find((s) => String(s.id) === audioSel.value) || null,
      firstAudio: audio[0] || null,
      sub: subs.find((s) => String(s.id) === subSel.value) || null,
    }),
  };
}

function watchButtons(item, refresh) {
  const leaf = item.type === "movie" || item.type === "episode";
  const watched = leaf ? item.viewCount > 0 : item.viewedLeafCount >= item.leafCount;
  const call = async (what) => {
    await api("GET", `/:/${what}?key=${encodeURIComponent(item.ratingKey)}&identifier=com.plexapp.plugins.library`);
    toast(what === "scrobble" ? "Marked as watched" : "Marked as unwatched");
    refresh();
  };
  return [
    el("button", { type: "button", text: "Mark watched", disabled: watched, onclick: () => call("scrobble").catch(fail) }),
    el("button", { type: "button", text: "Mark unwatched", onclick: () => call("unscrobble").catch(fail) }),
  ];
}

// play starts a part at `startAt` seconds and returns a handle with the
// current position. It direct-plays when the browser can show the chosen
// tracks, and otherwise converts (see docs/spec/transcode.md, streams.md and
// web.md):
// - a non-first audio track or an image subtitle needs a conversion;
// - text subtitles are a WebVTT <track>, shifted to match a conversion;
// - conversions are MP4 (H.264) when the browser plays it, else WebM (VP9).
// A converted stream starts at `base` seconds of the source, so it gets its
// own controls: the seek bar spans the whole item, seeks inside what has
// already arrived stay in the stream, and other seeks restart it there.
function play(holder, item, part, choice, startAt = 0) {
  const video = el("video", { controls: true, autoplay: true, preload: "metadata" });
  const wrap = el("div", { class: "player" }, video);
  const textSub = choice.sub && choice.sub.key ? choice.sub : null;
  const needsTranscode = (choice.audio && choice.firstAudio && choice.audio.id !== choice.firstAudio.id) ||
    (choice.sub && !choice.sub.key);
  const format = video.canPlayType('video/mp4; codecs="avc1.640028, mp4a.40.2"') ? "mp4"
    : video.canPlayType('video/webm; codecs="vp9, opus"') ? "webm" : "";
  let transcoding = false;
  let base = 0;
  let last = 0;
  let controls = null;
  const total = (part.duration || item.duration || 0) / 1000;
  const position = () => Math.min(base + (video.currentTime || 0), total || Infinity);
  const durationMs = () => (transcoding ? 0 : Math.floor((video.duration || 0) * 1000)) || item.duration || 0;
  const report = (st) => {
    const t = Math.floor(position() * 1000);
    if (!t) return;
    const q = new URLSearchParams({ ratingKey: item.ratingKey, key: item.key, state: st, time: t, duration: durationMs() });
    api("GET", "/:/timeline?" + q).catch(() => {});
  };
  const setTrack = () => {
    video.querySelectorAll("track").forEach((t) => t.remove());
    if (!textSub) return;
    const q = new URLSearchParams({ format: "vtt" });
    if (transcoding && base > 0) q.set("offset", base.toFixed(3));
    const track = el("track", { kind: "subtitles", default: true, label: textSub.displayTitle,
      srclang: textSub.languageCode || "und", src: withToken(textSub.key + "?" + q) });
    video.append(track);
    track.track.mode = "showing";
  };
  const startTranscode = (at, paused = false) => {
    transcoding = true;
    base = Math.max(0, at);
    const q = new URLSearchParams({ path: item.key, offset: base.toFixed(1), subtitles: "auto" });
    if (format === "webm") q.set("videoResolution", "1280x720");
    if (choice.audio) q.set("audioStreamID", choice.audio.id);
    q.set("subtitleStreamID", choice.sub ? choice.sub.id : 0);
    if (!controls) controls = convertedControls();
    controls.loading(true);
    video.autoplay = !paused;
    video.src = withToken(`/video/:/transcode/universal/start.${format}?` + q);
    setTrack();
    if (!paused) video.play().catch(() => {});
  };

  // convertedControls replaces the native controls in converted mode.
  function convertedControls() {
    video.controls = false;
    const playBtn = el("button", { type: "button", class: "play", "aria-label": "Pause", text: "❚❚" });
    const bar = el("input", { type: "range", class: "seek", "aria-label": "Seek", min: 0, max: total || 1, step: "any", value: 0 });
    const clock = el("span", { class: "clock" });
    const muteBtn = el("button", { type: "button", "aria-label": "Mute", text: "🔊" });
    const fullBtn = el("button", { type: "button", "aria-label": "Full screen", text: "⛶" });
    const spinner = el("div", { class: "loading", text: "Converting…", hidden: true });
    let dragging = false;
    let timer = 0;
    let target = null;
    const show = (t) => {
      if (!dragging) bar.value = t;
      clock.textContent = `${fmtClock(t * 1000)} / ${fmtClock(total * 1000)}`;
    };
    // local reports whether stream time t has arrived and the browser can
    // seek to it. Browsers that treat the stream as live (Chromium does)
    // report nothing seekable, so every seek restarts there.
    const within = (ranges, t, margin) => {
      for (let i = 0; i < ranges.length; i++) {
        if (t >= ranges.start(i) && t <= ranges.end(i) - margin) return true;
      }
      return false;
    };
    const local = (t) => within(video.buffered, t, 0.5) && within(video.seekable, t, 0);
    // seek moves to source time t. Restarts are debounced, so dragging or
    // repeated key presses start one conversion, not many.
    const seek = (t) => {
      t = Math.min(Math.max(0, t), Math.max(0, total - 1));
      target = t;
      show(t);
      clearTimeout(timer);
      if (t >= base && local(t - base)) {
        target = null;
        video.currentTime = t - base;
        return;
      }
      timer = setTimeout(() => {
        target = null;
        if (wrap.isConnected) startTranscode(t, video.paused); // not if the player was replaced
      }, 350);
    };
    const toggle = () => (video.paused ? video.play().catch(() => {}) : video.pause());
    playBtn.addEventListener("click", toggle);
    video.addEventListener("click", toggle);
    muteBtn.addEventListener("click", () => { video.muted = !video.muted; });
    fullBtn.addEventListener("click", () => {
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
      else wrap.requestFullscreen().catch(() => {});
    });
    bar.addEventListener("input", () => { dragging = true; show(Number(bar.value)); });
    bar.addEventListener("change", () => { dragging = false; seek(Number(bar.value)); });
    wrap.addEventListener("keydown", (e) => {
      if (e.target.tagName === "BUTTON" && (e.key === " " || e.key === "Enter")) return;
      const from = target ?? position();
      const keys = {
        " ": toggle, k: toggle, m: () => muteBtn.click(), f: () => fullBtn.click(),
        ArrowLeft: () => seek(from - 10), ArrowRight: () => seek(from + 10),
        Home: () => seek(0), End: () => seek(total),
      };
      if (!keys[e.key]) return;
      e.preventDefault();
      keys[e.key]();
    });
    const sync = () => {
      playBtn.textContent = video.paused ? "▶" : "❚❚";
      playBtn.setAttribute("aria-label", video.paused ? "Play" : "Pause");
      muteBtn.textContent = video.muted ? "🔇" : "🔊";
      muteBtn.setAttribute("aria-label", video.muted ? "Unmute" : "Mute");
    };
    ["play", "pause", "volumechange"].forEach((ev) => video.addEventListener(ev, sync));
    video.addEventListener("timeupdate", () => { if (target === null) show(position()); });
    video.addEventListener("playing", () => { spinner.hidden = true; });
    video.addEventListener("canplay", () => { spinner.hidden = true; });
    wrap.tabIndex = 0;
    wrap.classList.add("converted");
    wrap.append(spinner, el("div", { class: "controls" }, playBtn, bar, clock, muteBtn, fullBtn));
    show(base);
    sync();
    return { loading: (on) => { spinner.hidden = !on; }, seek };
  }

  video.addEventListener("loadedmetadata", () => {
    if (!transcoding && startAt > 0) video.currentTime = startAt;
  });
  video.addEventListener("timeupdate", () => {
    if (Date.now() - last > 10000) { last = Date.now(); report("playing"); }
  });
  video.addEventListener("pause", () => report("paused"));
  video.addEventListener("ended", () => report("stopped"));
  video.addEventListener("error", () => {
    // Some browser builds (e.g. Chromium without proprietary codecs) play
    // neither the file nor H.264; those get WebM.
    if (!transcoding && !format) {
      holder.replaceChildren(el("p", { class: "error", text: "This browser cannot play this file, and it cannot play the H.264 or VP9 video that Sloplex converts to." }));
      return;
    }
    if (!transcoding) {
      toast("Converting this file for your browser…");
      startTranscode(video.currentTime > 0 ? video.currentTime : startAt);
      return;
    }
    holder.replaceChildren(el("p", { class: "error", text: "This file could not be played, even after converting it." }));
  });
  holder.replaceChildren(wrap);
  if (needsTranscode && format) {
    startTranscode(startAt);
  } else {
    video.src = withToken(part.key);
    setTrack();
  }
  return { position, seek: (t) => (controls ? controls.seek(t) : (video.currentTime = t)) };
}

async function viewSearch(query) {
  $("#search-input").value = query;
  if (!query.trim()) { render(el("h1", { text: "Search" })); return; }
  const hubs = container(await api("GET", "/hubs/search?" + new URLSearchParams({ query, limit: 30 }))).Hub || [];
  render(
    el("h1", {}, "Results for “", query, "”"),
    hubs.length ? hubs.map((h) => [el("h2", { text: h.title }), grid(h.Metadata)])
      : el("p", { class: "muted", text: "No matches." }));
}

// ---- Account ---------------------------------------------------------------

async function viewAccount() {
  const me = state.me;
  const tokens = (await api("GET", "/sloplex/tokens")).tokens || [];
  const cur = el("input", { type: "password", autocomplete: "current-password" });
  const nw = el("input", { type: "password", autocomplete: "new-password", minlength: 8, required: true });
  const err = el("p", { class: "error" });
  const pwForm = el("form", {
    class: "stack",
    onsubmit: async (e) => {
      e.preventDefault();
      err.textContent = "";
      try {
        await api("PUT", "/sloplex/account/password", { current: cur.value, new: nw.value });
        toast("Password changed. Other devices were signed out.");
        state.me = await api("GET", "/sloplex/account");
        viewAccount();
      } catch (ex) { err.textContent = ex.message; }
    },
  },
  me.hasPassword ? el("label", {}, "Current password", cur) : el("p", { class: "muted", text: "This account has no password yet." }),
  el("label", {}, "New password (at least 8 characters)", nw),
  err,
  el("button", { class: "primary", type: "submit", text: me.hasPassword ? "Change password" : "Set password" }));

  render(
    el("h1", { text: `Account: ${me.name}` }),
    el("div", { class: "card-panel" }, pwForm),
    el("h2", { text: "Signed-in devices" }),
    tokenTable(tokens, async (t) => {
      await api("DELETE", `/sloplex/tokens/${t.id}`);
      toast(`Signed out ${t.device}`);
      viewAccount();
    }));
}

function tokenTable(tokens, revoke) {
  if (!tokens.length) return el("p", { class: "muted", text: "No issued tokens." });
  return el("table", {},
    el("tr", {}, el("th", { text: "Device" }), el("th", { text: "Created" }), el("th")),
    tokens.map((t) => el("tr", {},
      el("td", { text: t.device }),
      el("td", { text: fmtDate(t.createdAt) }),
      el("td", {}, el("button", { class: "danger", type: "button", text: "Revoke", onclick: () => revoke(t).catch(fail) })))));
}

// ---- Admin -----------------------------------------------------------------

function librariesPicker(current) {
  const all = el("input", { type: "checkbox", checked: current === null || current === undefined });
  const boxes = state.allSections.map((s) => {
    const cb = el("input", { type: "checkbox", value: s.key, checked: Array.isArray(current) && current.includes(Number(s.key)) });
    return { cb, label: el("label", {}, cb, s.title) };
  });
  const sync = () => boxes.forEach((b) => { b.cb.disabled = all.checked; });
  all.addEventListener("change", sync);
  sync();
  return {
    node: el("div", { class: "libs" }, el("label", {}, all, el("strong", { text: "All libraries" })), boxes.map((b) => b.label)),
    value: () => (all.checked ? null : boxes.filter((b) => b.cb.checked).map((b) => Number(b.cb.value))),
  };
}

function libsSummary(a) {
  if (a.admin || a.libraries === null || a.libraries === undefined) return "All";
  if (!a.libraries.length) return "None";
  return a.libraries.map((id) => (state.allSections.find((s) => Number(s.key) === id) || { title: `#${id}` }).title).join(", ");
}

async function viewAdmin() {
  if (!state.me.admin) { render(el("p", { class: "error", text: "Admins only." })); return; }
  // Admins see every section, so this is the full list.
  state.allSections = state.sections;
  const accounts = (await api("GET", "/sloplex/accounts")).accounts || [];
  const tc = await api("GET", "/sloplex/transcoder").catch(() => null);

  const rows = accounts.map((a) => el("tr", {},
    el("td", {}, el("strong", { text: a.name }), a.id === 1 ? el("div", { class: "muted", text: "owner" }) : null),
    el("td", { text: a.admin ? "Admin" : "User" }),
    el("td", { text: libsSummary(a) }),
    el("td", { text: a.hasPassword ? "Yes" : "No" }),
    el("td", {}, el("div", { class: "row" },
      el("button", { type: "button", text: "Edit", onclick: () => editAccount(a) }),
      el("button", { type: "button", text: "Device token", onclick: () => issueToken(a).catch(fail) }),
      a.id === 1 ? null : el("button", {
        class: "danger", type: "button", text: "Delete",
        onclick: async () => {
          if (!confirm(`Delete ${a.name}? Their watch history is deleted too.`)) return;
          await api("DELETE", `/sloplex/accounts/${a.id}`).catch(fail);
          toast(`Deleted ${a.name}`);
          viewAdmin();
        },
      })))));

  const refresh = el("div", { class: "row" }, state.sections.map((s) => el("button", {
    type: "button", text: `Refresh ${s.title}`,
    onclick: async () => {
      await api("GET", `/library/sections/${s.key}/refresh`);
      toast(`Scanning ${s.title}…`);
    },
  })));

  render(
    el("h1", { text: "Admin" }),
    el("h2", { text: "Accounts" }),
    el("table", {},
      el("tr", {}, ["Name", "Role", "Libraries", "Password", ""].map((h) => el("th", { text: h }))),
      rows),
    el("div", { class: "row", style: "margin-top:12px" },
      el("button", { class: "primary", type: "button", text: "New account", onclick: () => editAccount(null) })),
    el("h2", { text: "Libraries" }),
    state.sections.length ? refresh : el("p", { class: "muted", text: "No libraries configured." }),
    el("h2", { text: "Transcoding" }),
    el("p", { id: "transcoder", text: transcoderSummary(tc) }),
    el("div", { id: "admin-panel" }));
}

function transcoderSummary(tc) {
  if (!tc) return "Unknown.";
  if (!tc.available) return "Unavailable: ffmpeg was not found.";
  const hw = tc.hwaccel === "none"
    ? `Software (${tc.encoder})` + (tc.requested !== "none" ? `; no working hardware encoder found (requested: ${tc.requested})` : "")
    : `Hardware: ${tc.hwaccel} (${tc.encoder})` + (tc.device ? ` on ${tc.device}` : "");
  return `${hw} · up to ${tc.maxTranscodes} at once`;
}

function editAccount(a) {
  const isNew = a === null;
  const name = el("input", { value: isNew ? "" : a.name, required: true, maxlength: 32, pattern: "[A-Za-z0-9._\\-]{1,32}" });
  const pw = el("input", { type: "password", autocomplete: "new-password", minlength: 8, placeholder: isNew ? "optional" : "leave blank to keep" });
  const admin = el("input", { type: "checkbox", checked: !isNew && a.admin, disabled: !isNew && a.id === 1 });
  const libs = librariesPicker(isNew ? null : a.libraries);
  const err = el("p", { class: "error" });
  const form = el("form", {
    class: "stack",
    onsubmit: async (e) => {
      e.preventDefault();
      err.textContent = "";
      const body = { name: name.value.trim(), admin: admin.checked, libraries: libs.value() };
      if (pw.value) body.password = pw.value;
      if (!isNew && a.id === 1) delete body.admin;
      try {
        if (isNew) await api("POST", "/sloplex/accounts", body);
        else await api("PATCH", `/sloplex/accounts/${a.id}`, body);
        toast(isNew ? `Created ${body.name}` : `Saved ${body.name}`);
        if (!isNew && a.id === state.me.id) state.me = await api("GET", "/sloplex/account");
        viewAdmin();
      } catch (ex) { err.textContent = ex.message; }
    },
  },
  el("h2", { text: isNew ? "New account" : `Edit ${a.name}` }),
  el("label", {}, "Name", name),
  el("label", {}, isNew ? "Password (optional; without one, use device tokens)" : "New password (signs out their devices)", pw),
  el("label", { class: "row" }, admin, "Admin (can manage accounts and sees every library)"),
  el("div", {}, el("div", { class: "muted", text: "Library access" }), libs.node),
  err,
  el("div", { class: "row" },
    el("button", { class: "primary", type: "submit", text: isNew ? "Create" : "Save" }),
    el("button", { type: "button", text: "Cancel", onclick: () => $("#admin-panel").replaceChildren() })));
  $("#admin-panel").replaceChildren(el("div", { class: "card-panel" }, form));
  name.focus();
}

async function issueToken(a) {
  const device = prompt(`Device name for ${a.name}'s new token:`, "TV");
  if (device === null) return;
  const t = await api("POST", `/sloplex/accounts/${a.id}/tokens`, { device });
  const tokens = (await api("GET", `/sloplex/accounts/${a.id}/tokens`)).tokens || [];
  $("#admin-panel").replaceChildren(el("div", { class: "card-panel" },
    el("h2", { text: `Token for ${a.name} · ${t.device}` }),
    el("p", { text: "Copy it now. It is not shown again." }),
    el("div", { class: "secret", text: t.token }),
    el("h2", { text: `${a.name}'s devices` }),
    tokenTable(tokens, async (tok) => {
      await api("DELETE", `/sloplex/tokens/${tok.id}`);
      toast(`Revoked ${tok.device}`);
      issueTokenList(a);
    })));
}

async function issueTokenList(a) {
  const tokens = (await api("GET", `/sloplex/accounts/${a.id}/tokens`)).tokens || [];
  $("#admin-panel").replaceChildren(el("div", { class: "card-panel" },
    el("h2", { text: `${a.name}'s devices` }),
    tokenTable(tokens, async (tok) => {
      await api("DELETE", `/sloplex/tokens/${tok.id}`);
      issueTokenList(a);
    })));
}

// ---- Routing ---------------------------------------------------------------

function fail(ex) {
  if (ex instanceof APIError && ex.status === 401) return;
  toast(ex.message || String(ex), true);
}

async function route() {
  const hash = location.hash || "#/library";
  const [path, qs] = hash.slice(1).split("?");
  const parts = path.split("/").filter(Boolean);
  document.querySelectorAll("nav a").forEach((a) => a.classList.toggle("active", hash.startsWith(a.getAttribute("href"))));

  if (!state.token || parts[0] === "login") {
    if (state.token && parts[0] === "login") { location.hash = "#/library"; return; }
    viewLogin();
    return;
  }
  try {
    if (!state.me) await loadSession();
    switch (parts[0]) {
      case "section": await viewSection(parts[1]); break;
      case "item": await viewItem(parts[1]); break;
      case "search": await viewSearch(new URLSearchParams(qs).get("q") || ""); break;
      case "account": await viewAccount(); break;
      case "admin": await viewAdmin(); break;
      default: await viewLibrary();
    }
  } catch (ex) {
    if (ex instanceof APIError && ex.status === 401) return;
    render(el("p", { class: "error", text: ex.status === 404 ? "Not found." : ex.message }));
  }
}

$("#search").addEventListener("submit", (e) => {
  e.preventDefault();
  location.hash = "#/search?" + new URLSearchParams({ q: $("#search-input").value });
});
$("#signout").addEventListener("click", async () => {
  try { await api("POST", "/sloplex/auth/logout"); } catch { /* master tokens cannot log out */ }
  signedOut();
});
window.addEventListener("hashchange", route);
route();
