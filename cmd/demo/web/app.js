"use strict";

const $ = (sel, el = document) => el.querySelector(sel);
const el = (tag, attrs = {}, ...kids) => {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") n.className = v;
    else if (k === "text") n.textContent = v;
    else if (v !== undefined && v !== null && v !== false) n.setAttribute(k, v);
  }
  for (const k of kids) if (k !== null && k !== undefined) n.append(k);
  return n;
};
// kids drops the null, undefined, and false of optional parts, which
// append and replaceChildren would otherwise print.
const kids = (...xs) => xs.filter((k) => k !== null && k !== undefined && k !== false);
const SVG = "http://www.w3.org/2000/svg";
const svg = (tag, attrs = {}) => {
  const n = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  return n;
};

const clock = (s) => {
  const t = Math.max(0, Math.round(s));
  return `${Math.floor(t / 60)}:${String(t % 60).padStart(2, "0")}`;
};
const pct = (p) => (Math.abs(p) < 0.05 ? "0%" : `${p > 0 ? "+" : ""}${p.toFixed(1)}%`);
const hhmmss = (unix) => new Date(unix * 1000).toLocaleTimeString([], { hour12: false });
const color = (track, alpha = 1) => {
  if (track === undefined || track < 0) return `hsla(220, 8%, 60%, ${alpha})`;
  const h = (track * 137.508) % 360;
  return `hsla(${h.toFixed(0)}, 62%, 50%, ${alpha})`;
};

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
  return r.json();
}

/* ---------- tabs and header ---------- */

function showTab(name) {
  for (const t of document.querySelectorAll(".tab")) t.setAttribute("aria-selected", t.dataset.tab === name);
  for (const p of document.querySelectorAll(".panel")) p.hidden = p.id !== `panel-${name}`;
  if (name === "live") startLive();
  history.replaceState(null, "", `#${name}`);
}
for (const t of document.querySelectorAll(".tab")) t.addEventListener("click", () => showTab(t.dataset.tab));
window.addEventListener("hashchange", () => {
  const tab = location.hash.slice(1);
  if (["identify", "tracklist", "live"].includes(tab)) showTab(tab);
});

let info = { live: false };
async function init() {
  try {
    info = await getJSON("/api/info");
    $("#stats").replaceChildren(
      el("span", { class: "chip" }, el("b", { text: info.tracks.toLocaleString() }), " songs indexed"),
      el("span", { class: "chip" }, el("b", { text: info.mixes }), " test mixes"),
      el("span", { class: `chip ${info.live ? "good" : ""}`, text: info.live ? "live monitoring on" : "live monitoring off" }),
    );
    const ex = await getJSON("/api/examples");
    fillSongs(ex.songs);
    fillMixes(ex.mixes);
  } catch (e) {
    $("#stats").replaceChildren(el("span", { class: "chip bad", text: `server: ${e.message}` }));
  }
  const tab = location.hash.slice(1);
  if (["identify", "tracklist", "live"].includes(tab)) showTab(tab);
}

/* ---------- identify ---------- */

function fillSongs(songs) {
  const sel = $("#id-song");
  for (const s of songs.slice().sort((a, b) => a.name.localeCompare(b.name))) {
    sel.append(el("option", { value: s.track, text: s.name }));
  }
}

let kind = "turntable";
for (const b of document.querySelectorAll(".seg-btn")) {
  b.addEventListener("click", () => {
    kind = b.dataset.kind;
    for (const o of document.querySelectorAll(".seg-btn")) o.setAttribute("aria-pressed", o === b);
  });
}
const tempo = $("#id-tempo");
const showTempo = () => ($("#id-tempo-out").textContent = pct(Number(tempo.value)));
tempo.addEventListener("input", showTempo);
showTempo();

async function identify(form, label) {
  const out = $("#id-results");
  out.replaceChildren(el("div", { class: "placeholder", text: `Searching ${label}...` }));
  for (const b of document.querySelectorAll("#panel-identify .btn")) b.disabled = true;
  try {
    const r = await fetch("/api/identify", { method: "POST", body: form });
    const res = await r.json();
    if (!r.ok) throw new Error(res.error || r.statusText);
    renderIdentify(res);
  } catch (e) {
    out.replaceChildren(el("div", { class: "card err", text: e.message }));
  } finally {
    for (const b of document.querySelectorAll("#panel-identify .btn")) b.disabled = false;
  }
}

$("#id-example").addEventListener("click", () => {
  const f = new FormData();
  f.set("example", $("#id-song").value);
  f.set("tempo", tempo.value);
  f.set("kind", kind);
  identify(f, "a test song");
});

const idFile = $("#id-file");
function sendFile(file) {
  const f = new FormData();
  f.set("audio", file, file.name || "clip");
  f.set("start", $("#id-start").value || "0");
  identify(f, file.name || "your clip");
}
idFile.addEventListener("change", () => idFile.files[0] && sendFile(idFile.files[0]));
dropTarget($("#id-drop"), sendFile);

function dropTarget(zone, onFile) {
  zone.addEventListener("dragover", (e) => { e.preventDefault(); zone.classList.add("over"); });
  zone.addEventListener("dragleave", () => zone.classList.remove("over"));
  zone.addEventListener("drop", (e) => {
    e.preventDefault();
    zone.classList.remove("over");
    if (e.dataTransfer.files[0]) onFile(e.dataTransfer.files[0]);
  });
}

$("#id-record").addEventListener("click", async (ev) => {
  const btn = ev.currentTarget;
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false },
    });
  } catch (e) {
    $("#id-results").replaceChildren(el("div", { class: "card err", text: `Microphone: ${e.message}` }));
    return;
  }
  const type = ["audio/webm;codecs=opus", "audio/mp4", "audio/ogg"].find((t) => MediaRecorder.isTypeSupported(t));
  const rec = new MediaRecorder(stream, type ? { mimeType: type } : {});
  const chunks = [];
  rec.ondataavailable = (e) => chunks.push(e.data);
  rec.onstop = () => {
    stream.getTracks().forEach((t) => t.stop());
    btn.classList.remove("recording");
    btn.lastChild.textContent = " Record 10 s from the microphone";
    const blob = new Blob(chunks, { type: rec.mimeType });
    const f = new FormData();
    f.set("audio", blob, `recording.${rec.mimeType.includes("mp4") ? "m4a" : "webm"}`);
    identify(f, "your recording");
  };
  rec.start();
  btn.classList.add("recording");
  let left = 10;
  btn.lastChild.textContent = ` Listening... ${left}`;
  const timer = setInterval(() => {
    left -= 1;
    btn.lastChild.textContent = ` Listening... ${left}`;
    if (left <= 0) { clearInterval(timer); rec.stop(); }
  }, 1000);
});

function renderIdentify(res) {
  const out = $("#id-results");
  const a = res.answer;
  const card = el("div", { class: "card answer" });
  if (a) {
    card.append(...kids(
      el("div", { class: "kicker", text: "Landmark's answer" }),
      el("div", { class: "name", text: a.title || a.name }),
      a.artist ? el("div", { class: "artist", text: a.artist }) : null,
      el("div", { class: "chips" },
        el("span", { class: "chip" }, "tempo ", el("b", { text: pct(a.tempo_pct) })),
        el("span", { class: "chip" }, "clip starts ", el("b", { text: clock(a.offset_s) }), " into the song"),
        el("span", { class: "chip" }, "found by ", el("b", { text: a.search })),
        el("span", { class: "chip" }, el("b", { text: `${Math.round(res.ms.search)} ms` }), " to search"),
      ),
    ));
  } else {
    card.append(el("div", { class: "kicker", text: "Landmark's answer" }), el("div", { class: "name", text: "No match" }),
      el("p", { class: "muted", text: "No search found a song with enough matching landmarks. Is it in the catalog?" }));
  }
  if (res.truth) {
    const t = res.truth;
    const how = t.tempo_pct ? ` at ${pct(t.tempo_pct)} (${t.kind})` : "";
    card.append(el("div", { class: `chip note ${t.correct ? "good" : "bad"}`,
      text: `${t.correct ? "Correct" : "Wrong"}: the clip is ${t.name}${how}, from ${clock(t.start_s)}` }));
  }
  const fam = (title, what, v) => {
    const total = v.score + v.runner_up || 1;
    return el("div", { class: "family" },
      el("h4", { text: title }),
      el("div", { class: "what", text: what }),
      el("div", { class: `verdict ${v.matched ? "yes" : "no"}`, text: v.matched ? "Found it" : "No match" }),
      v.track >= 0 ? el("div", { class: "song", text: `${v.matched ? "" : "best guess: "}${v.name}` }) : null,
      el("div", { class: "what", text: `${Math.round(v.score)} matching landmarks, ${Math.round(v.runner_up)} for the next song` }),
      el("div", { class: "bar" }, el("span", { style: `width:${(100 * v.score / total).toFixed(0)}%` })),
    );
  };
  const listen = el("div", { class: "listen" },
    el("label", {}, "What Landmark heard: the 10 s it searched, at 11,025 Hz", el("audio", { controls: "", src: res.clip })));
  if (a && a.has_audio) {
    const song = el("audio", { controls: "", preload: "metadata", src: `/api/songs/${a.track}/audio` });
    song.addEventListener("loadedmetadata", () => { song.currentTime = Math.max(0, a.offset_s); }, { once: true });
    listen.append(el("label", {}, `The song it found, from ${clock(a.offset_s)}`, song));
  }
  card.append(listen);
  out.replaceChildren(card, el("div", { class: "compare" },
    fam("Pair hashes", "classic fingerprinting: two peaks, their time gap", res.pairs),
    fam("Triplet hashes", "three peaks, time and pitch ratios: tempo-invariant", res.triplets),
  ));
}

/* ---------- tracklist ---------- */

function fillMixes(mixes) {
  const sel = $("#tl-mix");
  const how = { none: "no tempo change", resample: "turntable tempo changes", stretch: "keylock tempo changes" };
  const groups = {};
  for (const m of mixes) (groups[m.timescale] ||= []).push(m);
  for (const [ts, list] of Object.entries(groups)) {
    const g = el("optgroup", { label: how[ts] || ts });
    for (const m of list) {
      const fx = m.effect === "none" ? "no effect" : m.effect;
      g.append(el("option", { value: m.index, text: `${m.name} (${m.songs} songs, ${clock(m.seconds)}, ${fx})` }));
    }
    sel.append(g);
  }
  // Start on an interesting one: turntable changes with an effect.
  const pick = mixes.find((m) => m.timescale === "resample" && m.effect === "bass");
  if (pick) sel.value = pick.index;
}

let tl = null;
$("#tl-run").addEventListener("click", () => {
  const f = new FormData();
  f.set("example", $("#tl-mix").value);
  setPlayer(`/api/mixes/${$("#tl-mix").value}/audio`);
  runTracklist(f);
});
const tlFile = $("#tl-file");
tlFile.addEventListener("change", () => {
  if (!tlFile.files[0]) return;
  const f = new FormData();
  f.set("audio", tlFile.files[0], tlFile.files[0].name);
  setPlayer(URL.createObjectURL(tlFile.files[0]));
  runTracklist(f, tlFile.files[0].name);
});

async function runTracklist(form, label) {
  tl = { windows: [], entries: [], analysis: null, truth: null, seconds: 0, done: null, name: label || "", analyzing: false };
  $("#tl-out").hidden = false;
  $("#tl-title").textContent = label ? label : "Decoding the mix...";
  $("#tl-status").textContent = "";
  $("#tl-run").disabled = true;
  try {
    const r = await fetch("/api/tracklist", { method: "POST", body: form });
    if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
    const reader = r.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, i);
        buf = buf.slice(i + 1);
        if (line) onMessage(JSON.parse(line));
      }
    }
  } catch (e) {
    $("#tl-status").replaceChildren(el("span", { class: "err", text: e.message }));
  } finally {
    $("#tl-run").disabled = false;
    drawSoon();
  }
}

function onMessage(m) {
  switch (m.type) {
    case "start":
      Object.assign(tl, { seconds: m.seconds, total: m.windows, truth: m.truth || null, window: m.window_s, hop: m.hop_s });
      if (m.name) tl.name = m.name;
      $("#tl-title").textContent = tl.name;
      break;
    case "window": tl.windows.push(m); break;
    case "song":
      tl.entries[m.index] = { ...m.entry, open: m.kind === "start" };
      break;
    case "analyzing": tl.analyzing = true; break;
    case "analysis": tl.analysis = m.songs || {}; tl.analysisError = m.error; tl.analyzing = false; break;
    case "done":
      tl.done = m;
      if (m.truth) tl.truth = m.truth;
      break;
    case "error": $("#tl-status").replaceChildren(el("span", { class: "err", text: m.error })); break;
  }
  drawSoon();
}

let pending = false;
function drawSoon() {
  if (pending) return;
  pending = true;
  requestAnimationFrame(() => { pending = false; if (tl) drawTracklist(); });
}
window.addEventListener("resize", drawSoon);

function drawTracklist() {
  const s = tl;
  const status = $("#tl-status");
  if (s.done) {
    const parts = [`${s.done.entries} songs listed in ${s.done.elapsed_s.toFixed(1)} s (${s.done.searches} searches)`];
    if (s.done.known !== undefined) parts.push(`found ${s.done.found} of ${s.done.known} songs, ${s.done.correct} of ${s.done.entries} entries right`);
    status.textContent = parts.join(" · ");
  } else if (s.analyzing) {
    status.textContent = "Reading fades and tempo changes from the audio...";
  } else if (s.total) {
    status.textContent = `Window ${s.windows.length} of ${s.total} · ${s.entries.filter(Boolean).length} songs so far`;
  }

  const box = $("#tl-timeline");
  const W = Math.max(560, box.clientWidth);
  const gut = 92, pad = 12;
  const dur = s.seconds || 1;
  const x = (t) => gut + (Math.min(Math.max(t, 0), dur) / dur) * (W - gut - pad);
  const lanes = [{ key: "win", label: "Windows", y: 26, h: 12 }, { key: "est", label: "Landmark", y: 48, h: 44 }];
  if (s.truth) lanes.push({ key: "gt", label: "True mix", y: 104, h: 44 });
  const H = lanes[lanes.length - 1].y + lanes[lanes.length - 1].h + 8;
  const root = svg("svg", { viewBox: `0 0 ${W} ${H}`, height: H, role: "img", "aria-label": "Mix timeline" });

  // Axis.
  const step = [15, 30, 60, 120, 300, 600].find((v) => dur / v <= 10) || 1200;
  for (let t = 0; t <= dur; t += step) {
    root.append(svg("line", { x1: x(t), x2: x(t), y1: 16, y2: H, stroke: "var(--line)" }));
    const tx = svg("text", { x: x(t) + 3, y: 12 });
    tx.textContent = clock(t);
    root.append(tx);
  }
  for (const l of lanes) {
    const t = svg("text", { x: 0, y: l.y + l.h / 2 + 4, class: "lane-label" });
    t.textContent = l.label;
    root.append(t);
  }

  // Windows: one strip per window, colored by its best answer.
  const win = lanes[0];
  const ww = Math.max(1, x(s.hop || 2.5) - x(0) - 0.5);
  for (const w of s.windows) {
    root.append(svg("rect", { x: x(w.t0), y: win.y, width: ww, height: win.h, rx: 1.5,
      fill: w.accepted ? color(w.track) : color(-1, 0.25) }));
  }
  const cursor = s.windows.length ? s.windows[s.windows.length - 1].t0 + (s.window || 10) : 0;

  // Envelopes: rise over the fade-in, hold, fall over the fade-out.
  const envelope = (lane, a, b, c, d, fill, label, opts = {}) => {
    const top = lane.y, bot = lane.y + lane.h;
    const pts = [[x(a), bot], [x(b), top], [x(c), top], [x(d), bot]].map((p) => p.join(",")).join(" ");
    const g = svg("g");
    g.append(svg("polygon", { points: pts, fill, opacity: opts.open ? 0.55 : 0.88,
      stroke: opts.stroke || "none", "stroke-width": 1.5, "stroke-dasharray": opts.dash || "" }));
    const room = x(c) - x(b) - 10;
    if (label && room > 30) {
      const t = svg("text", { x: x(b) + 6, y: top + lane.h / 2 + 4, class: "bar-label" });
      const chars = Math.floor(room / 6.6);
      t.textContent = label.length > chars ? `${label.slice(0, Math.max(chars - 1, 1))}…` : label;
      g.append(t);
    }
    const title = svg("title");
    title.textContent = opts.title || label || "";
    g.append(title);
    root.append(g);
  };

  const est = lanes[1];
  s.entries.forEach((e, i) => {
    if (!e) return;
    const an = s.analysis && s.analysis[i];
    const end = e.open ? Math.max(e.end_s, cursor - (s.window || 10) + (s.hop || 2.5)) : e.end_s;
    let a = e.start_s, b = e.start_s, c = end, d = end;
    if (an && an.fade_in) [a, b] = an.fade_in;
    if (an && an.fade_out) [c, d] = an.fade_out;
    if (b > c) [b, c] = [(b + c) / 2, (b + c) / 2];
    const wrong = e.correct === false;
    envelope(est, a, b, c, d, color(e.track), e.title || e.name, {
      open: e.open, stroke: wrong ? "var(--bad)" : "none", dash: wrong ? "4 3" : "",
      title: `${e.name}\n${clock(e.start_s)} to ${clock(e.end_s)}, tempo ${pct(e.tempo_pct)}${wrong ? "\nnot in the mix here" : ""}`,
    });
  });
  if (s.truth) {
    const gt = lanes[2];
    for (const t of s.truth) {
      envelope(gt, t.start_s, t.start_s + t.fade_in, t.end_s - t.fade_out, t.end_s,
        t.known ? color(t.track) : color(-1, 0.6), t.known ? (t.title || t.name) : "not in the catalog", {
          stroke: t.known && !t.found && s.done ? "var(--bad)" : "none", dash: "4 3",
          title: `${t.name}\n${clock(t.start_s)} to ${clock(t.end_s)}, tempo ${pct(t.tempo_pct)} (${t.mode})`,
        });
    }
  }
  if (!s.done) root.append(svg("line", { x1: x(cursor), x2: x(cursor), y1: 16, y2: H, stroke: "var(--accent)", "stroke-width": 2 }));
  const ph = svg("line", { id: "playhead", y1: 16, y2: H, stroke: "var(--text)", "stroke-width": 1.5, "stroke-dasharray": "3 2", visibility: "hidden" });
  root.append(ph);
  root.addEventListener("click", (ev) => {
    const r = root.getBoundingClientRect();
    const px = ((ev.clientX - r.left) / r.width) * W;
    seek(((px - gut) / (W - gut - pad)) * dur);
  });
  s.map = { gut, pad, W, dur };
  box.replaceChildren(root);
  updatePlayhead();

  $("#tl-legend").replaceChildren(...kids(
    el("span", { text: "Bars rise and fall with each song's fade in and out (estimated from the audio once tracklisting is done)." }),
    s.truth ? el("span", { text: "Same color means the same song; a dashed red outline marks a mistake or a missed song." }) : null,
    s.analysisError ? el("span", { class: "err", text: `Analysis: ${s.analysisError}` }) : null,
  ));

  const body = $("#tl-table tbody");
  body.replaceChildren(...s.entries.map((e, i) => {
    if (!e) return el("tr");
    const an = s.analysis && s.analysis[i];
    let mixed = "";
    if (an) {
      if (Math.abs(an.rate - 1) > 0.005) mixed = an.turntable ? "turntable" : "keylock";
      if (an.bpm) mixed += `${mixed ? ", " : ""}${an.bpm.toFixed(1)} BPM`;
    }
    const fades = an ? [an.fade_in && `in ${clock(an.fade_in[0])}-${clock(an.fade_in[1])}`,
      an.fade_out && `out ${clock(an.fade_out[0])}-${clock(an.fade_out[1])}`].filter(Boolean).join(" · ") : "";
    const row = el("tr", { "data-i": i, title: "Play from here" },
      el("td", { text: i + 1 }),
      el("td", { text: `${clock(e.start_s)}-${e.open ? "" : clock(e.end_s)}` }),
      el("td", { class: "song" }, el("span", { class: "swatch", style: `background:${color(e.track)}` }), e.name),
      el("td", { text: pct(e.tempo_pct) }),
      el("td", { text: mixed || (s.analyzing ? "..." : "") }),
      el("td", { text: fades }),
      el("td", { text: e.correct === undefined ? "" : e.correct ? "✓" : "✗", title: e.correct === false ? "not playing at that time" : "" }),
    );
    row.addEventListener("click", () => seek(e.start_s));
    return row;
  }));
  updatePlayhead();
}

/* The mix player: the playhead follows it, the timeline and the table seek it. */
const audioEl = $("#tl-audio");
function setPlayer(src) {
  audioEl.pause();
  audioEl.src = src;
  $("#tl-player").hidden = false;
}
function seek(t) {
  if (!audioEl.src) return;
  audioEl.currentTime = Math.max(0, t);
  audioEl.play().catch(() => {});
}
function updatePlayhead() {
  if (!tl || !tl.map) return;
  const ph = document.getElementById("playhead");
  const t = audioEl.currentTime || 0;
  const { gut, pad, W, dur } = tl.map;
  const shown = audioEl.src && (t > 0 || !audioEl.paused);
  if (ph) {
    const px = gut + (Math.min(t, dur) / dur) * (W - gut - pad);
    ph.setAttribute("x1", px);
    ph.setAttribute("x2", px);
    ph.setAttribute("visibility", shown ? "visible" : "hidden");
  }
  const now = tl.entries.map((e, i) => [e, i]).filter(([e]) => e && t >= e.start_s && t < (e.open ? Infinity : e.end_s));
  for (const tr of document.querySelectorAll("#tl-table tbody tr")) {
    tr.classList.toggle("current", shown && now.some(([, i]) => String(i) === tr.dataset.i));
  }
  const label = $("#tl-now");
  if (!shown) label.textContent = "Play the mix: the dashed line follows it. Click the timeline or a row to jump.";
  else if (!now.length) label.textContent = `${clock(t)}: no song listed here`;
  else label.replaceChildren(`${clock(t)}: `, ...now.flatMap(([e], k) => [k ? " into " : "", el("b", { text: e.name })]));
}
let playing = false;
function tick() {
  updatePlayhead();
  if (playing) requestAnimationFrame(tick);
}
audioEl.addEventListener("play", () => { playing = true; tick(); });
audioEl.addEventListener("pause", () => { playing = false; updatePlayhead(); });
audioEl.addEventListener("seeked", updatePlayhead);

/* ---------- live ---------- */

let live = null;
const stations = new Map();
const seenEvents = new Set(); // stream#seq: a reconnect replays the log
function startLive() {
  if (live || !info.live) {
    $("#live-off").hidden = info.live;
    return;
  }
  live = new EventSource("/api/live");
  live.addEventListener("streams", (ev) => {
    for (const st of JSON.parse(ev.data)) {
      const s = station(st.id);
      s.holder = st.holder;
    }
    drawStations();
  });
  live.addEventListener("event", (ev) => {
    const e = JSON.parse(ev.data);
    const key = `${e.stream}#${e.seq}`;
    if (seenEvents.has(key)) return;
    seenEvents.add(key);
    $("#live-off").hidden = true;
    const s = station(e.stream);
    if (e.kind === "start") s.now = e;
    else {
      if (s.now && s.now.song.track === e.song.track) s.now = null;
      s.played.unshift(e);
      s.played.length = Math.min(s.played.length, 5);
    }
    feed(e);
    drawStations();
  });
  live.addEventListener("problem", (ev) => {
    $("#live-off").hidden = false;
    $("#live-off").replaceChildren(el("h2", { text: "Live monitoring problem" }), el("p", { class: "err", text: JSON.parse(ev.data).error }));
  });
}

function station(id) {
  if (!stations.has(id)) stations.set(id, { id, holder: "", now: null, played: [] });
  return stations.get(id);
}

function drawStations() {
  const grid = $("#live-grid");
  grid.replaceChildren(...[...stations.values()].sort((a, b) => a.id.localeCompare(b.id)).map((s) => {
    const now = s.now;
    return el("div", { class: "card station" },
      el("div", { class: "id" }, el("b", { text: s.id }),
        el("span", { class: "chip" }, s.holder ? el("span", { class: "listening" }, el("span"), el("span"), el("span")) : null,
          s.holder ? ` ${s.holder}` : "no worker")),
      el("div", { class: "now", text: now ? (now.song.title || now.song.name) : "Listening..." }),
      el("div", { class: "since", text: now ? `${now.song.artist ? `${now.song.artist} · ` : ""}since ${hhmmss(now.start_unix)}${Math.abs(now.tempo_pct) > 0.5 ? ` · ${pct(now.tempo_pct)}` : ""}` : "waiting for the first song to be certain" }),
      s.played.length ? el("ul", {}, ...s.played.map((p) => el("li", { text: `${hhmmss(p.start_unix)}  ${p.song.title || p.song.name}${p.resumed ? " (continued after failover)" : ""}` }))) : null,
    );
  }));
}

// The feed lists events by broadcast time (a start at the song's start, an
// end at its end), newest first: the log replays stream by stream.
const feedEvents = [];
let feedPending = false;
function feed(e) {
  feedEvents.push({ ...e, at: e.kind === "start" ? e.start_unix : e.end_unix });
  if (feedPending) return;
  feedPending = true;
  requestAnimationFrame(() => {
    feedPending = false;
    feedEvents.sort((a, b) => b.at - a.at || b.seq - a.seq);
    feedEvents.length = Math.min(feedEvents.length, 200);
    $("#live-feed").replaceChildren(...feedEvents.map((e) => {
      const what = e.kind === "start" ? "playing" : `played ${Math.round(e.end_unix - e.start_unix)} s`;
      return el("li", {},
        el("span", { class: "t", text: `${hhmmss(e.at)}  ${e.stream.padEnd(6)} ` }),
        el("span", { class: e.kind, text: what.padEnd(13) }),
        ` ${e.song.name}${Math.abs(e.tempo_pct) > 0.5 ? ` (${pct(e.tempo_pct)})` : ""}${e.resumed ? " [continued]" : ""}${e.interrupted ? " [interrupted]" : ""}`,
      );
    }));
  });
}

init();
