// SPDX-License-Identifier: Apache-2.0
// Read-only mirror UI: session picker, role cards, one xterm per role.
"use strict";

const App = window.go.main.App;
const Ev = window.runtime;

const els = {
  picker: document.getElementById("picker"),
  list: document.getElementById("session-list"),
  empty: document.getElementById("picker-empty"),
  error: document.getElementById("picker-error"),
  mirror: document.getElementById("mirror"),
  cards: document.getElementById("cards"),
  terms: document.getElementById("terms"),
  conn: document.getElementById("conn-state"),
  gateway: document.getElementById("gateway"),
  detach: document.getElementById("detach"),
};

const state = {
  roles: [],
  terms: new Map(), // idx -> {term, holder, lastOutput}
  active: -1,
  attached: false,
  pollTimer: 0,
};

// working/idle window, mirroring the deck's 2s rule
const WORKING_MS = 2000;

// brand palette, matching style.css
const termTheme = {
  background: "#14120e",
  foreground: "#eae7dd",
  cursor: "#d3a15a",
};

function b64Bytes(b64) {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function termFor(idx) {
  let t = state.terms.get(idx);
  if (t) return t;
  const holder = document.createElement("div");
  holder.className = "term-holder hidden";
  els.terms.appendChild(holder);
  const term = new Terminal({
    cols: 80,
    rows: 24,
    disableStdin: true,
    scrollback: 5000,
    theme: termTheme,
    fontFamily: "ui-monospace, Menlo, monospace",
    fontSize: 13,
  });
  term.open(holder);
  t = { term, holder, lastOutput: 0 };
  state.terms.set(idx, t);
  if (state.active < 0) focusRole(idx);
  return t;
}

function focusRole(idx) {
  state.active = idx;
  for (const [i, t] of state.terms) t.holder.classList.toggle("hidden", i !== idx);
  renderCards();
}

function roleState(idx) {
  const r = state.roles[idx] || {};
  const t = state.terms.get(idx);
  if (r.gone) return null;
  if (r.exited) return { dot: "○", cls: "exited", label: "exited" };
  if (r.paused) return { dot: "❚❚", cls: "waiting", label: "paused" };
  if (r.waiting) return { dot: "◆", cls: "waiting", label: "waiting for input" };
  if (t && Date.now() - t.lastOutput < WORKING_MS) return { dot: "●", cls: "working", label: "working" };
  return { dot: "◦", cls: "idle", label: "idle" };
}

function renderCards() {
  els.cards.textContent = "";
  state.roles.forEach((r, idx) => {
    const st = roleState(idx);
    if (!st) return; // tombstoned by a reload
    const card = document.createElement("div");
    card.className = "card" + (idx === state.active ? " active" : "");
    card.addEventListener("click", () => focusRole(idx));
    const row = document.createElement("div");
    row.className = "row";
    const dot = document.createElement("span");
    dot.className = "dot " + st.cls;
    dot.textContent = st.dot;
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = `${idx + 1} ${r.name}`;
    row.append(dot, name);
    if (r.model) {
      const model = document.createElement("span");
      model.className = "model";
      model.textContent = r.model;
      row.append(model);
    }
    const stateLine = document.createElement("div");
    stateLine.className = "state";
    stateLine.textContent = st.label;
    card.append(row, stateLine);
    els.cards.appendChild(card);
  });
}

async function refreshSessions() {
  try {
    const sessions = (await App.Sessions()) || [];
    els.list.textContent = "";
    els.empty.classList.toggle("hidden", sessions.length > 0);
    for (const s of sessions) {
      const li = document.createElement("li");
      const left = document.createElement("div");
      const name = document.createElement("div");
      name.className = "name";
      name.textContent = s.name;
      const dir = document.createElement("div");
      dir.className = "dir";
      dir.textContent = s.dir;
      left.append(name, dir);
      const meta = document.createElement("span");
      meta.className = "meta";
      meta.textContent = `pid ${s.pid} · up ${s.up}`;
      li.append(left, meta);
      li.addEventListener("click", () => attach(s.dir));
      els.list.appendChild(li);
    }
  } catch (err) {
    showPickerError(String(err));
  }
}

function showPickerError(msg) {
  els.error.textContent = msg;
  els.error.classList.remove("hidden");
}

async function attach(dir) {
  els.error.classList.add("hidden");
  try {
    const roster = await App.Attach(dir);
    state.roles = roster.roles || [];
    state.attached = true;
    clearInterval(state.pollTimer);
    els.picker.classList.add("hidden");
    els.mirror.classList.remove("hidden");
    els.conn.textContent = "replaying…";
    state.roles.forEach((_, idx) => termFor(idx));
    focusRole(startIdx());
  } catch (err) {
    showPickerError(String(err));
  }
}

function startIdx() {
  const i = state.roles.findIndex((r) => r.start);
  return i >= 0 ? i : 0;
}

function toPicker() {
  state.attached = false;
  for (const t of state.terms.values()) t.term.dispose();
  state.terms.clear();
  state.active = -1;
  els.terms.textContent = "";
  els.mirror.classList.add("hidden");
  els.picker.classList.remove("hidden");
  refreshSessions();
  state.pollTimer = setInterval(refreshSessions, 5000);
}

Ev.EventsOn("pane:output", (idx, b64) => {
  if (!state.attached) return;
  const t = termFor(idx);
  t.lastOutput = Date.now();
  t.term.write(b64Bytes(b64));
});

Ev.EventsOn("pane:reset", (idx) => {
  const t = state.terms.get(idx);
  if (t) t.term.reset();
});

Ev.EventsOn("session:ready", () => {
  els.conn.textContent = "attached · live";
});

Ev.EventsOn("session:roster", (roles) => {
  state.roles = roles || [];
  renderCards();
});

Ev.EventsOn("session:status", (on, up) => {
  if (!on) {
    els.gateway.textContent = "sphragis off";
    els.gateway.className = "";
  } else if (up) {
    els.gateway.textContent = "sphragis ●";
    els.gateway.className = "on";
  } else {
    els.gateway.textContent = "sphragis down";
    els.gateway.className = "down";
  }
});

Ev.EventsOn("session:focus", (idx) => {
  if (state.attached) focusRole(idx);
});

Ev.EventsOn("session:lost", (msg) => {
  if (!state.attached) return;
  showPickerError(`connection lost: ${msg}`);
  toPicker();
});

els.detach.addEventListener("click", async () => {
  await App.Detach();
  toPicker();
});

setInterval(() => {
  if (state.attached) renderCards();
}, 1000);

toPicker();

// dev/test hook: attach straight to $CHORAGOS_DESKTOP_AUTOATTACH
App.AutoAttachDir().then((dir) => {
  if (dir) attach(dir);
});
