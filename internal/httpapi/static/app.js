const state = {
  sessions: [],
  active: null,
  socket: null,
  term: null,
  fit: null,
  quick: [],
  search: "",
  showEnded: false,
};

const $ = (id) => document.getElementById(id);
const MIN_TERMINAL_COLS = 80;
const MIN_TERMINAL_ROWS = 20;
const DEFAULT_TERMINAL_COLS = 120;
const DEFAULT_TERMINAL_ROWS = 36;

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: options.body instanceof FormData ? {} : { "Content-Type": "application/json" },
    ...options,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error || msg;
    } catch {}
    throw new Error(msg);
  }
  if (res.status === 204) return null;
  return res.json();
}

async function loadSessions() {
  state.sessions = await api("/api/sessions");
  renderSessions();
  if (!state.active) {
    const firstLive = state.sessions.find((s) => s.live);
    if (firstLive) selectSession(firstLive.id);
  }
}

function visibleSessions() {
  const q = state.search.trim().toLowerCase();
  return state.sessions.filter((s) => {
    const ended = !s.live || s.status === "exited" || s.status === "failed";
    if (ended && !state.showEnded) return false;
    if (!q) return true;
    return `${s.name} ${s.id} ${s.status}`.toLowerCase().includes(q);
  });
}

function renderSessions() {
  $("sessions").innerHTML = "";
  for (const s of visibleSessions()) {
    const el = document.createElement("article");
    el.className = `session session-${s.status} ${state.active === s.id ? "active" : ""}`;
    const updated = new Date(s.updated_at).toLocaleString();
    el.innerHTML = `
      <div class="session-head">
        <div class="session-name"></div>
        <div class="session-actions">
          <button class="link-btn finish-btn" type="button">Finish</button>
          <button class="link-btn delete-btn" type="button">Delete</button>
        </div>
      </div>
      <div class="meta"><span class="status-${s.status}">${s.status}</span> · ${updated}</div>
      <label class="notify-row">
        <input class="notify-input" type="checkbox">
        <span>通知</span>
        <span class="notify-state"></span>
      </label>
    `;
    el.querySelector(".session-name").textContent = s.name;
    el.querySelector(".notify-input").checked = Boolean(s.notify_on_waiting);
    el.querySelector(".notify-state").textContent = s.notifications_available ? (s.notify_on_waiting ? "已启用" : "未启用") : "不可用";
    el.querySelector(".finish-btn").onclick = async (ev) => {
      ev.stopPropagation();
      await finishSession(s.id);
    };
    el.querySelector(".delete-btn").onclick = async (ev) => {
      ev.stopPropagation();
      await deleteSession(s.id);
    };
    el.querySelector(".notify-row").onclick = (ev) => {
      ev.stopPropagation();
    };
    el.querySelector(".notify-input").onclick = (ev) => {
      ev.stopPropagation();
    };
    el.querySelector(".notify-input").onchange = async (ev) => {
      ev.stopPropagation();
      await setNotify(s.id, ev.target.checked);
    };
    el.onclick = () => selectSession(s.id);
    $("sessions").appendChild(el);
  }
}

function currentSession() {
  return state.sessions.find((s) => s.id === state.active);
}

function initTerminal() {
  if (state.term) state.term.dispose();
  state.term = new Terminal({
    cols: DEFAULT_TERMINAL_COLS,
    rows: DEFAULT_TERMINAL_ROWS,
    cursorBlink: true,
    convertEol: true,
    fontFamily: "Menlo, Consolas, monospace",
    fontSize: 13,
    theme: { background: "#12110f", foreground: "#f4f1e8", cursor: "#f4f1e8" },
  });
  state.fit = new FitAddon.FitAddon();
  state.term.loadAddon(state.fit);
  state.term.open($("terminal"));
  fitTerminalSafely();
  requestAnimationFrame(() => fitTerminalSafely());
  setTimeout(() => fitTerminalSafely(), 120);
  state.term.onData((data) => {
    sendWS({ type: "input", data });
    scheduleSnapshot();
    if (data.includes("\r") || data.includes("\n")) {
      setTimeout(() => syncSnapshotNow(), 80);
      setTimeout(() => syncSnapshotNow(), 260);
    }
  });
  window.removeEventListener("resize", resizeTerm);
  window.addEventListener("resize", resizeTerm);
}

function selectSession(id) {
  const sess = state.sessions.find((s) => s.id === id);
  if (!sess) return;
  state.active = id;
  $("active-title").textContent = `${sess.name}(${sess.status})`;
  renderSessions();
  if (sess.live) {
    connectWS(id);
  } else {
    if (state.socket) state.socket.close();
    initTerminal();
    api(`/api/sessions/${id}/output`).then((out) => state.term.write(out.content || ""));
  }
}

function connectWS(id) {
  if (state.socket) state.socket.close();
  initTerminal();
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const ws = new WebSocket(`${proto}://${location.host}/api/sessions/${id}/ws`);
  state.socket = ws;
  ws.binaryType = "arraybuffer";
  ws.onopen = () => resizeTerm();
  ws.onmessage = (ev) => {
    const text = typeof ev.data === "string" ? ev.data : new TextDecoder().decode(ev.data);
    state.term.write(text);
    requestAnimationFrame(() => scheduleSnapshot());
  };
  ws.onclose = () => setTimeout(loadSessions, 300);
}

function sendWS(obj) {
  if (state.socket && state.socket.readyState === WebSocket.OPEN) {
    state.socket.send(JSON.stringify(obj));
  }
}

function resizeTerm() {
  if (!state.fit || !state.term) return;
  fitTerminalSafely();
  if (state.term.cols >= MIN_TERMINAL_COLS && state.term.rows >= MIN_TERMINAL_ROWS) {
    sendWS({ type: "resize", cols: state.term.cols, rows: state.term.rows });
  }
}

function fitTerminalSafely() {
  if (!state.fit || !state.term) return;
  try {
    state.fit.fit();
  } catch {
    state.term.resize(DEFAULT_TERMINAL_COLS, DEFAULT_TERMINAL_ROWS);
    return;
  }
  if (state.term.cols < MIN_TERMINAL_COLS || state.term.rows < MIN_TERMINAL_ROWS) {
    state.term.resize(
      Math.max(state.term.cols || 0, DEFAULT_TERMINAL_COLS),
      Math.max(state.term.rows || 0, DEFAULT_TERMINAL_ROWS)
    );
  }
}

let snapshotTimer = null;
function scheduleSnapshot() {
  clearTimeout(snapshotTimer);
  snapshotTimer = setTimeout(() => syncSnapshotNow(), 120);
}

function syncSnapshotNow() {
  if (!state.term) return;
  const lines = [];
  const buffer = state.term.buffer.active;
  for (let i = 0; i < buffer.length; i++) {
    lines.push(buffer.getLine(i)?.translateToString(true) || "");
  }
  sendWS({ type: "snapshot", data: lines.join("\n") });
}

async function loadQuick() {
  state.quick = await api("/api/quick-commands");
  renderQuick();
}

function renderQuick() {
  $("quick-list").innerHTML = "";
  for (const q of state.quick) {
    const chip = document.createElement("div");
    chip.className = "quick-chip";
    chip.title = q.text;
    chip.innerHTML = `<span></span><button class="chip-close" type="button" title="Delete">×</button>`;
    chip.querySelector("span").textContent = q.text;
    chip.onclick = () => {
      $("composer-input").value = q.text;
      $("composer-input").focus();
    };
    chip.querySelector(".chip-close").onclick = async (ev) => {
      ev.stopPropagation();
      await api(`/api/quick-commands/${q.id}`, { method: "DELETE" });
      await loadQuick();
    };
    $("quick-list").appendChild(chip);
  }
  const add = document.createElement("button");
  add.className = "add-quick";
  add.type = "button";
  add.title = "Add Quick Command";
  add.textContent = "+";
  add.onclick = () => $("quick-dialog").showModal();
  $("quick-list").appendChild(add);
}

async function setNotify(id, enabled) {
  await api(`/api/sessions/${id}`, { method: "PATCH", body: JSON.stringify({ notify_on_waiting: enabled }) });
  await loadSessions();
}

async function finishSession(id) {
  await api(`/api/sessions/${id}/finish`, { method: "POST" });
  if (state.active === id) state.active = null;
  await loadSessions();
}

async function deleteSession(id) {
  await api(`/api/sessions/${id}`, { method: "DELETE" });
  if (state.active === id) {
    state.active = null;
    if (state.socket) state.socket.close();
    if (state.term) state.term.clear();
    $("active-title").textContent = "No session";
  }
  await loadSessions();
}

$("new-session").onsubmit = async (ev) => {
  ev.preventDefault();
  const name = $("session-name").value.trim();
  if (!name) return;
  const s = await api("/api/sessions", { method: "POST", body: JSON.stringify({ name }) });
  $("session-name").value = "";
  await loadSessions();
  selectSession(s.id);
};

$("session-search").oninput = (ev) => {
  state.search = ev.target.value;
  renderSessions();
};

$("show-ended").onchange = (ev) => {
  state.showEnded = ev.target.checked;
  renderSessions();
};

$("composer").onsubmit = (ev) => {
  ev.preventDefault();
  sendComposer();
};

$("composer-input").onkeydown = (ev) => {
  if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) {
    ev.preventDefault();
    sendComposer();
  }
};

function sendComposer() {
  const input = $("composer-input");
  const text = input.value;
  if (!text || !state.active) return;
  sendWS({ type: "input", data: text + "\r" });
  input.value = "";
  input.focus();
}

$("quick-form").onsubmit = async (ev) => {
  ev.preventDefault();
  const text = $("quick-text").value.trim();
  if (!text) return;
  const name = text.length > 40 ? `${text.slice(0, 37)}...` : text;
  await api("/api/quick-commands", { method: "POST", body: JSON.stringify({ name, text }) });
  $("quick-text").value = "";
  $("quick-dialog").close();
  await loadQuick();
};

$("quick-cancel").onclick = () => $("quick-dialog").close();

document.addEventListener("paste", async (ev) => {
  if (!state.active) return;
  const file = [...(ev.clipboardData?.files || [])].find((f) => f.type.startsWith("image/"));
  if (!file) return;
  const form = new FormData();
  form.append("file", file, file.name || "paste.png");
  form.append("mime_type", file.type);
  const res = await api(`/api/sessions/${state.active}/uploads`, { method: "POST", body: form });
  $("composer-input").value += `${res.path}\n`;
});

setInterval(loadSessions, 3000);
loadSessions().catch(console.error);
loadQuick().catch(console.error);

if (typeof window !== "undefined") {
  window.easyTerminalApp = {
    state,
    sendComposer,
    renderSessions,
    setNotify,
    visibleSessions,
  };
}
