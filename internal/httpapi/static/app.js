const state = {
  sessions: [],
  active: null,
  socket: null,
  term: null,
  fit: null,
  quick: [],
  config: null,
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
    const requestedID = new URLSearchParams(location.search).get("session");
    const requested = requestedID ? state.sessions.find((s) => s.id === requestedID && s.live) : null;
    const firstLive = requested || state.sessions.find((s) => s.live);
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
    if (typeof ev.data === "string") {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === "snapshot_request") {
          syncSnapshotNow();
          return;
        }
      } catch {}
    }
    const text = typeof ev.data === "string" ? ev.data : new TextDecoder().decode(ev.data);
    state.term.write(text);
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

async function loadConfig() {
  state.config = await api("/api/config");
  renderConfig();
}

function renderConfig() {
  const cfg = state.config;
  if (!cfg) return;
  $("cfg-fast-waiting").value = cfg.fast_waiting_transition_ms;
  $("cfg-conservative-waiting").value = cfg.conservative_waiting_transition_ms;
  $("cfg-lark-max-lines").value = cfg.lark_notify_max_lines;
  $("cfg-lark-app-id").value = cfg.lark_app_id || "";
  $("cfg-lark-app-secret").value = cfg.lark_app_secret || "";
  $("cfg-lark-receive-id").value = cfg.lark_notify_receive_id || "";
  $("cfg-lark-default-session-name").value = cfg.lark_default_session_name || "";
  $("cfg-lark-mention-enabled").checked = Boolean(cfg.lark_mention_enabled);
  $("cfg-prestart-command").value = cfg.session_pre_start_command || "";
  $("cfg-drop-patterns").value = (cfg.lark_notify_drop_line_patterns || []).join("\n");
  $("cfg-session-name-presets").value = JSON.stringify(cfg.session_name_presets || {}, null, 2);
  $("cfg-session-start-presets").value = JSON.stringify(cfg.session_start_presets || {}, null, 2);
  $("config-error").textContent = "";
}

function readNumber(id) {
  const n = Number($(id).value);
  if (!Number.isFinite(n)) throw new Error("配置里存在无效数字");
  return Math.trunc(n);
}

function readConfigForm() {
  let namePresets;
  let startPresets;
  try {
    namePresets = JSON.parse($("cfg-session-name-presets").value || "{}");
    startPresets = JSON.parse($("cfg-session-start-presets").value || "{}");
  } catch {
    throw new Error("启动预设必须是有效 JSON");
  }
  return {
    lark_app_id: $("cfg-lark-app-id").value.trim(),
    lark_app_secret: $("cfg-lark-app-secret").value,
    lark_notify_receive_id: $("cfg-lark-receive-id").value.trim(),
    lark_mention_enabled: $("cfg-lark-mention-enabled").checked,
    lark_default_session_name: $("cfg-lark-default-session-name").value.trim(),
    fast_waiting_transition_ms: readNumber("cfg-fast-waiting"),
    conservative_waiting_transition_ms: readNumber("cfg-conservative-waiting"),
    lark_notify_max_lines: readNumber("cfg-lark-max-lines"),
    session_pre_start_command: $("cfg-prestart-command").value,
    lark_notify_drop_line_patterns: $("cfg-drop-patterns").value.split("\n").map((line) => line.trim()).filter(Boolean),
    session_name_presets: namePresets,
    session_start_presets: startPresets,
  };
}

async function saveConfig() {
  const cfg = readConfigForm();
  state.config = await api("/api/config", { method: "PATCH", body: JSON.stringify(cfg) });
  renderConfig();
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

$("config-open").onclick = async () => {
  try {
    if (!state.config) await loadConfig();
    renderConfig();
    $("config-dialog").showModal();
  } catch (err) {
    console.error(err);
  }
};

$("config-cancel").onclick = () => $("config-dialog").close();

$("config-form").onsubmit = async (ev) => {
  ev.preventDefault();
  try {
    await saveConfig();
    $("config-dialog").close();
  } catch (err) {
    $("config-error").textContent = err.message || String(err);
  }
};

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
loadConfig().catch(console.error);

if (typeof window !== "undefined") {
  window.easyTerminalApp = {
    state,
    sendComposer,
    renderSessions,
    setNotify,
    loadConfig,
    saveConfig,
    visibleSessions,
  };
}
