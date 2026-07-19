// SPDX-License-Identifier: Apache-2.0
// Interactive session client: picker/onboarding, role cards, xterm panes, gates, board.
"use strict";

const App = window.go.main.App;
const Ev = window.runtime;
const FitAddon = window.FitAddon.FitAddon; // the fit UMD exports a namespace, not the class

const els = {
  picker: document.getElementById("picker"),
  list: document.getElementById("session-list"),
  empty: document.getElementById("picker-empty"),
  status: document.getElementById("picker-status"),
  error: document.getElementById("picker-error"),
  openBtn: document.getElementById("open-project"),
  setup: document.getElementById("setup"),
  setupDir: document.getElementById("setup-dir"),
  setupAuto: document.getElementById("setup-auto"),
  setupTemplate: document.getElementById("setup-template"),
  setupCreate: document.getElementById("setup-create"),
  setupNote: document.getElementById("setup-note"),
  mirror: document.getElementById("mirror"),
  cards: document.getElementById("cards"),
  terms: document.getElementById("terms"),
  conn: document.getElementById("conn-state"),
  gateway: document.getElementById("gateway"),
  gateCount: document.getElementById("gate-count"),
  boardBtn: document.getElementById("board-btn"),
  detach: document.getElementById("detach"),
  stop: document.getElementById("stop"),
  gateModal: document.getElementById("gate-modal"),
  gateTo: document.getElementById("gate-to"),
  gateTask: document.getElementById("gate-task"),
  gateBrief: document.getElementById("gate-brief"),
  gateAt: document.getElementById("gate-at"),
  gateMore: document.getElementById("gate-more"),
  gateApprove: document.getElementById("gate-approve"),
  gateView: document.getElementById("gate-view"),
  gateReject: document.getElementById("gate-reject"),
  boardModal: document.getElementById("board-modal"),
  boardRows: document.getElementById("board-rows"),
  boardClose: document.getElementById("board-close"),
  viewerModal: document.getElementById("viewer-modal"),
  viewerTitle: document.getElementById("viewer-title"),
  viewerBody: document.getElementById("viewer-body"),
  viewerClose: document.getElementById("viewer-close"),
  star: document.getElementById("star-link"),
};

const state = {
  roles: [],
  terms: new Map(), // idx -> {term, fit, holder, lastOutput}
  active: -1,
  attached: false,
  expectStop: false,
  gates: [],
  board: [],
  pollTimer: 0,
  setupDir: "",
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

function strToB64(s) {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
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
    scrollback: 5000,
    theme: termTheme,
    fontFamily: "ui-monospace, Menlo, monospace",
    fontSize: 13,
  });
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.open(holder);
  term.onData((d) => App.Input(idx, strToB64(d)));
  term.onBinary((d) => App.Input(idx, btoa(d)));
  t = { term, fit, holder, lastOutput: 0 };
  state.terms.set(idx, t);
  if (state.active < 0) focusRole(idx);
  return t;
}

function focusRole(idx) {
  state.active = idx;
  for (const [i, t] of state.terms) t.holder.classList.toggle("hidden", i !== idx);
  fitActive();
  const t = state.terms.get(idx);
  if (t) t.term.focus();
  renderCards();
}

// fitActive sizes the visible pane to its container and pushes it to the PTY.
function fitActive() {
  const t = state.terms.get(state.active);
  if (!t || !state.attached) return;
  if (!t.fit.proposeDimensions()) return; // container not laid out yet
  t.fit.fit();
  const idx = state.active;
  const { cols, rows } = t.term;
  if (t.kicked) {
    App.Resize(idx, cols, rows);
    return;
  }
  // first fit: wiggle the PTY size so a full-screen child repaints (SIGWINCH)
  t.kicked = true;
  App.Resize(idx, cols > 2 ? cols - 1 : cols + 1, rows);
  setTimeout(() => App.Resize(idx, cols, rows), 60);
}

window.addEventListener("resize", () => fitActive());

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
    if (r.overBudget) {
      const budget = document.createElement("div");
      budget.className = "state budget";
      budget.textContent = "over budget";
      card.append(budget);
    }
    if (idx === state.active && !r.gone) {
      const actions = document.createElement("div");
      actions.className = "role-actions";
      const restart = document.createElement("button");
      restart.textContent = "restart";
      restart.addEventListener("click", (e) => {
        e.stopPropagation();
        App.RestartRole(idx);
      });
      const pause = document.createElement("button");
      pause.textContent = r.paused ? "resume" : "pause";
      pause.addEventListener("click", (e) => {
        e.stopPropagation();
        App.PauseRole(idx);
      });
      actions.append(restart, pause);
      card.append(actions);
    }
    els.cards.appendChild(card);
  });
}

/* gates */

function renderGate() {
  els.gateCount.textContent = state.gates.length ? `${state.gates.length} awaiting approval` : "";
  if (!state.gates.length) {
    els.gateModal.classList.add("hidden");
    return;
  }
  const g = state.gates[0];
  els.gateTo.textContent = g.to;
  els.gateTask.textContent = (g.reason ? `[${g.reason}] ` : "") + (g.task || (g.brief ? "see brief" : ""));
  els.gateBrief.textContent = g.reason ? g.report || "-" : g.brief || "-";
  els.gateAt.textContent = new Date(g.at).toLocaleTimeString();
  els.gateMore.textContent =
    state.gates.length > 1 ? `+${state.gates.length - 1} more waiting behind this one` : "";
  els.gateView.classList.toggle("hidden", !(g.reason ? g.report : g.brief));
  els.gateModal.classList.remove("hidden");
}

els.gateApprove.addEventListener("click", () => App.Gate(true));
els.gateReject.addEventListener("click", () => App.Gate(false));
els.gateView.addEventListener("click", () => {
  const g = state.gates[0];
  if (!g) return;
  const f = g.reason ? g.report : g.brief;
  if (f) openViewer(f);
});

/* task board */

function renderBoard() {
  els.boardRows.textContent = "";
  const head = document.createElement("div");
  head.className = "trow head";
  for (const h of ["time", "kind", "to", "task", "status"]) {
    const c = document.createElement("span");
    c.textContent = h;
    head.appendChild(c);
  }
  els.boardRows.appendChild(head);
  for (const t of state.board) {
    const row = document.createElement("div");
    row.className = "trow";
    const time = document.createElement("span");
    time.textContent = new Date(t.at).toLocaleTimeString();
    const kind = document.createElement("span");
    kind.textContent = (t.id ? `${t.kind} ${t.id}` : t.kind) + (t.round ? ` r${t.round}` : "");
    const to = document.createElement("span");
    to.textContent = t.to;
    const task = document.createElement("span");
    task.textContent = t.task;
    if (t.file) {
      const file = document.createElement("span");
      file.className = "file";
      file.textContent = ` [${t.file.split("/").pop()}]`;
      file.addEventListener("click", () => openViewer(t.file));
      task.appendChild(file);
    }
    const status = document.createElement("span");
    if (t.kind === "delegate" && !t.doneAt && t.timedOut) {
      status.className = "status timeout";
      status.textContent = "timeout";
    } else if (t.kind === "delegate" && !t.doneAt) {
      status.className = "status pending";
      status.textContent = "pending";
    } else if (t.kind === "delegate") {
      status.className = "status done";
      status.textContent = "✓ " + Math.round((t.doneAt - t.at) / 1000) + "s";
    } else if (t.done) {
      status.className = "status done";
      status.textContent = "✓";
    }
    if (t.score) status.textContent += ` ${t.score}`;
    row.append(time, kind, to, task, status);
    els.boardRows.appendChild(row);
  }
}

els.boardBtn.addEventListener("click", () => {
  renderBoard();
  els.boardModal.classList.remove("hidden");
});
els.boardClose.addEventListener("click", () => els.boardModal.classList.add("hidden"));

/* file viewer */

async function openViewer(path) {
  try {
    const body = await App.FileContent(path);
    els.viewerTitle.textContent = path.split("/").pop();
    els.viewerBody.textContent = body;
    els.viewerModal.classList.remove("hidden");
  } catch (err) {
    showPickerError(String(err));
  }
}

els.viewerClose.addEventListener("click", () => els.viewerModal.classList.add("hidden"));
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  els.viewerModal.classList.add("hidden");
  els.boardModal.classList.add("hidden");
});

/* session picker + onboarding */

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
  els.status.classList.add("hidden");
  els.error.textContent = msg;
  els.error.classList.remove("hidden");
}

function showPickerStatus(msg) {
  els.error.classList.add("hidden");
  els.status.textContent = msg;
  els.status.classList.toggle("hidden", !msg);
}

els.star.addEventListener("click", (e) => {
  e.preventDefault();
  Ev.BrowserOpenURL("https://github.com/sphragis-oss/choragos");
});

els.openBtn.addEventListener("click", async () => {
  els.error.classList.add("hidden");
  const dir = await App.PickFolder();
  if (!dir) return;
  if (await App.HasConfig(dir)) {
    els.setup.classList.add("hidden");
    await startAndAttach(dir);
    return;
  }
  state.setupDir = dir;
  els.setupDir.textContent = dir;
  els.setupNote.textContent = "";
  if (!els.setupTemplate.options.length) {
    for (const name of await App.Templates()) {
      const opt = document.createElement("option");
      opt.value = name;
      opt.textContent = name;
      els.setupTemplate.appendChild(opt);
    }
  }
  els.setup.classList.remove("hidden");
});

els.setupAuto.addEventListener("change", () => {
  els.setupTemplate.disabled = els.setupAuto.checked;
});

els.setupCreate.addEventListener("click", async () => {
  els.setupCreate.disabled = true;
  try {
    const note = await App.InitConfig(state.setupDir, els.setupTemplate.value, els.setupAuto.checked);
    els.setupNote.textContent = note;
    els.setup.classList.add("hidden");
    await startAndAttach(state.setupDir);
  } catch (err) {
    showPickerError(String(err));
  } finally {
    els.setupCreate.disabled = false;
  }
});

async function startAndAttach(dir) {
  showPickerStatus(`starting session in ${dir}…`);
  try {
    await App.StartSession(dir);
    showPickerStatus("");
    await attach(dir);
  } catch (err) {
    showPickerError(String(err));
  }
}

async function attach(dir) {
  els.error.classList.add("hidden");
  state.attached = true; // before Attach: the replay starts before the promise resolves
  try {
    const roster = await App.Attach(dir);
    state.roles = roster.roles || [];
    state.expectStop = false;
    clearInterval(state.pollTimer);
    els.picker.classList.add("hidden");
    els.mirror.classList.remove("hidden");
    els.conn.textContent = "replaying…";
    state.roles.forEach((_, idx) => termFor(idx));
    focusRole(startIdx());
  } catch (err) {
    state.attached = false;
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
  state.gates = [];
  state.board = [];
  els.terms.textContent = "";
  els.gateModal.classList.add("hidden");
  els.boardModal.classList.add("hidden");
  els.viewerModal.classList.add("hidden");
  els.setup.classList.add("hidden");
  els.status.classList.add("hidden");
  els.mirror.classList.add("hidden");
  els.picker.classList.remove("hidden");
  disarmStop();
  refreshSessions();
  state.pollTimer = setInterval(refreshSessions, 5000);
}

/* wire events */

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
  requestAnimationFrame(() => fitActive());
});

Ev.EventsOn("session:roster", (roles) => {
  const wasWaiting = new Set(state.roles.filter((r) => r.waiting).map((r) => r.name));
  state.roles = roles || [];
  renderCards();
  const fresh = state.roles.filter((r) => r.waiting && !r.gone && !wasWaiting.has(r.name));
  if (fresh.length && !document.hasFocus()) {
    App.Notify("Waiting for input", fresh.map((r) => r.name).join(", "));
  }
});

Ev.EventsOn("session:board", (board) => {
  state.board = board || [];
  if (!els.boardModal.classList.contains("hidden")) renderBoard();
});

Ev.EventsOn("session:gates", (gates) => {
  const had = state.gates.length;
  state.gates = gates || [];
  renderGate();
  if (state.gates.length > had && !document.hasFocus()) {
    const g = state.gates[state.gates.length - 1];
    App.Notify("Approval needed", `${g.to}: ${g.task || "see brief"}`);
  }
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
  if (!state.expectStop) showPickerError(`connection lost: ${msg}`);
  toPicker();
});

/* lifecycle buttons */

els.detach.addEventListener("click", async () => {
  await App.Detach();
  toPicker();
});

// stop is two-click: arm, then confirm within 3s
function disarmStop() {
  els.stop.classList.remove("armed");
  els.stop.textContent = "Stop everything";
}

els.stop.addEventListener("click", async () => {
  if (!els.stop.classList.contains("armed")) {
    els.stop.classList.add("armed");
    els.stop.textContent = "Click again to stop all agents";
    setTimeout(disarmStop, 3000);
    return;
  }
  state.expectStop = true;
  await App.StopSession();
});

setInterval(() => {
  if (state.attached) renderCards();
}, 1000);

toPicker();

// dev/test hook: attach straight to $CHORAGOS_DESKTOP_AUTOATTACH
App.AutoAttachDir().then((dir) => {
  if (dir) attach(dir);
});
