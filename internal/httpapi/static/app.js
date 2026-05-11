const state = {
  sessions: [],
  active: null,
  socket: null,
  term: null,
  fit: null,
  quick: [],
  config: null,
  search: "",
  larkRegistrationTimer: null,
  editingPresetCommand: null,
  startupJSONDirty: false,
};

const $ = (id) => document.getElementById(id);
const MIN_TERMINAL_COLS = 80;
const MIN_TERMINAL_ROWS = 20;
const DEFAULT_TERMINAL_COLS = 120;
const DEFAULT_TERMINAL_ROWS = 36;
const DEFAULT_SESSION_NAME = "默认会话";
const DEFAULT_AGENT_PRESET_CODE = "999999";
const CONFIG_TAB_IDS = ["config-session", "config-lark", "config-notify", "config-startup"];

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
    if (!s.live || s.status === "exited" || s.status === "failed") return false;
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
  $("lark-register-panel").hidden = true;
  $("lark-register-panel").classList.add("hidden");
  $("lark-register-qr").src = "";
  $("lark-register-code").textContent = "";
  $("lark-register-link").href = "";
  $("lark-register-status").textContent = "等待扫码确认...";
  $("lark-register-start").disabled = false;
  $("lark-test-start").disabled = false;
  $("lark-test-result").innerHTML = "";
  renderDefaultAgentPresetFromStartPreset(cfg.session_start_presets || {});
  renderAgentPresetControls();
  setAgentPresetStatus(`选择默认会话 Agent 后，会更新 ${DEFAULT_AGENT_PRESET_CODE} 号启动预设；发送“开始 会话名”会默认启动它。0 号表示仅进入目录。`);
  $("preset-session-name").value = "";
  state.editingPresetCommand = null;
  setPresetStatus("");
  stopLarkRegistrationPolling();
  $("cfg-fast-waiting").value = cfg.fast_waiting_transition_ms;
  $("cfg-conservative-waiting").value = cfg.conservative_waiting_transition_ms;
  $("cfg-lark-max-lines").value = cfg.lark_notify_max_lines;
  $("cfg-lark-app-id").value = cfg.lark_app_id || "";
  $("cfg-lark-app-secret").value = cfg.lark_app_secret || "";
  $("cfg-lark-receive-id").value = cfg.lark_notify_receive_id || "";
  $("cfg-lark-default-session-name").value = cfg.lark_default_session_name || "";
  $("cfg-lark-session-chat-prefix").value = cfg.lark_session_chat_prefix || "ET · ";
  $("cfg-lark-mention-enabled").checked = Boolean(cfg.lark_mention_enabled);
  $("cfg-prestart-command").value = cfg.session_pre_start_command || "";
  $("cfg-drop-patterns").value = JSON.stringify(normalizeDropRules(cfg.lark_notify_drop_line_patterns || []), null, 2);
  $("cfg-lark-custom-shortcuts").value = JSON.stringify(cfg.lark_custom_shortcuts || [], null, 2);
  $("cfg-session-name-presets").value = JSON.stringify(cfg.session_name_presets || {}, null, 2);
  $("cfg-session-start-presets").value = JSON.stringify(cfg.session_start_presets || {}, null, 2);
  renderDropRules();
  renderCustomShortcuts();
  renderPreStartCommandRows();
  renderNamePresets();
  state.startupJSONDirty = false;
  updateStartupJSONPreview();
  $("config-error").textContent = "";
}

function setConfigTab(targetID) {
  document.querySelectorAll(".config-tab").forEach((item) => item.classList.toggle("active", item.dataset.configTarget === targetID));
  document.querySelectorAll(".config-panel").forEach((panel) => panel.classList.toggle("active", panel.id === targetID));
  updateConfigStepButtons(targetID);
}

function activeConfigTabID() {
  const active = Array.from(document.querySelectorAll(".config-tab")).find((tab) => /\bactive\b/.test(tab.className));
  return active?.dataset.configTarget || CONFIG_TAB_IDS[0];
}

function updateConfigStepButtons(targetID = activeConfigTabID()) {
  const index = CONFIG_TAB_IDS.indexOf(targetID);
  const prev = $("config-prev");
  const next = $("config-next");
  if (!prev || !next) return;
  prev.disabled = index <= 0;
  next.disabled = index < 0 || index >= CONFIG_TAB_IDS.length - 1;
}

function moveConfigStep(delta) {
  const index = CONFIG_TAB_IDS.indexOf(activeConfigTabID());
  if (index < 0) return;
  const nextIndex = Math.min(CONFIG_TAB_IDS.length - 1, Math.max(0, index + delta));
  if (nextIndex !== index) setConfigTab(CONFIG_TAB_IDS[nextIndex]);
}

async function openConfigDialog(targetID = "config-session") {
  if (!state.config) await loadConfig();
  renderConfig();
  setConfigTab(targetID);
  $("config-dialog").showModal();
}

async function maybeShowOnboarding() {
  if (!state.config || state.config.onboarding_completed) return;
  if ($("config-dialog").open || $("help-dialog").open) return;
  state.config = await api("/api/config", { method: "PATCH", body: JSON.stringify({ ...state.config, onboarding_completed: true }) });
  await openConfigDialog("config-session");
}

function readNumber(id) {
  const n = Number($(id).value);
  if (!Number.isFinite(n)) throw new Error("配置里存在无效数字");
  return Math.trunc(n);
}

function readConfigForm() {
  if (state.startupJSONDirty) syncStartupJSONPreview({ throwOnError: true });
  const namePresets = parseJSONObject($("cfg-session-name-presets").value || "{}", "会话名预设 JSON");
  const startPresets = parseJSONObject($("cfg-session-start-presets").value || "{}", "开始命令后缀预设 JSON");
  const dropRules = parseJSONArray($("cfg-drop-patterns").value || "[]", "通知过滤正则 JSON")
    .map((item) => ({
      title: String(item?.title || "").trim(),
      pattern: String(item?.pattern || "").trim(),
    }))
    .filter((item) => item.title || item.pattern);
  const customShortcuts = parseJSONArray($("cfg-lark-custom-shortcuts").value || "[]", "飞书自定义快捷键 JSON")
    .map((item) => ({
      label: String(item?.label || "").trim(),
      command: String(item?.command || "").trim(),
    }))
    .filter((item) => item.label && item.command);
  return {
    lark_app_id: $("cfg-lark-app-id").value.trim(),
    lark_app_secret: $("cfg-lark-app-secret").value,
    lark_notify_receive_id: $("cfg-lark-receive-id").value.trim(),
    lark_mention_enabled: $("cfg-lark-mention-enabled").checked,
    lark_default_session_name: $("cfg-lark-default-session-name").value.trim(),
    lark_session_chat_prefix: $("cfg-lark-session-chat-prefix").value.trim(),
    fast_waiting_transition_ms: readNumber("cfg-fast-waiting"),
    conservative_waiting_transition_ms: readNumber("cfg-conservative-waiting"),
    lark_notify_max_lines: readNumber("cfg-lark-max-lines"),
    session_pre_start_command: $("cfg-prestart-command").value,
    lark_notify_drop_line_patterns: dropRules,
    lark_custom_shortcuts: customShortcuts,
    onboarding_completed: Boolean(state.config?.onboarding_completed),
    session_name_presets: namePresets,
    session_start_presets: startPresets,
  };
}

function parseJSONArray(text, label) {
  let value;
  try {
    value = JSON.parse(text || "[]");
  } catch (err) {
    throw new Error(`${label} 格式不正确：${err.message}`);
  }
  if (!Array.isArray(value)) {
    throw new Error(`${label} 必须是 JSON 数组`);
  }
  return value;
}

function normalizeDropRules(value) {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => {
      if (typeof item === "string") return { title: "", pattern: item.trim() };
      return {
        title: String(item?.title || "").trim(),
        pattern: String(item?.pattern || "").trim(),
      };
    });
}

function readDropRulesForUI() {
  try {
    const rules = JSON.parse($("cfg-drop-patterns").value || "[]");
    return normalizeDropRules(rules);
  } catch {
    return null;
  }
}

function writeDropRulesFromUI(rules) {
  $("cfg-drop-patterns").value = JSON.stringify(normalizeDropRules(rules || []), null, 2);
  renderDropRules();
}

function renderDropRules() {
  const list = $("drop-rule-list");
  list.innerHTML = "";
  const rules = readDropRulesForUI();
  if (!rules) {
    const err = document.createElement("div");
    err.className = "preset-empty";
    err.textContent = "通知过滤正则 JSON 格式不正确。";
    list.appendChild(err);
    return;
  }
  if (rules.length === 0) {
    const empty = document.createElement("div");
    empty.className = "preset-empty";
    empty.textContent = "还没有过滤规则。";
    list.appendChild(empty);
    return;
  }
  rules.forEach((rule, index) => {
    const row = document.createElement("div");
    row.className = "drop-rule-row";
    const title = document.createElement("input");
    title.className = "drop-rule-title";
    title.placeholder = "标题";
    title.value = rule.title || "";
    const pattern = document.createElement("input");
    pattern.className = "drop-rule-pattern";
    pattern.placeholder = "正则表达式";
    pattern.value = rule.pattern || "";
    const remove = document.createElement("button");
    remove.className = "drop-rule-remove";
    remove.type = "button";
    remove.textContent = "删除";
    title.oninput = () => updateDropRule(index, title.value, pattern.value);
    pattern.oninput = () => updateDropRule(index, title.value, pattern.value);
    remove.onclick = () => deleteDropRule(index);
    row.appendChild(title);
    row.appendChild(pattern);
    row.appendChild(remove);
    list.appendChild(row);
  });
}

function addDropRule() {
  const rules = readDropRulesForUI();
  if (!rules) return;
  rules.push({ title: "", pattern: "" });
  writeDropRulesFromUI(rules);
}

function updateDropRule(index, title, pattern) {
  const rules = readDropRulesForUI();
  if (!rules || !rules[index]) return;
  rules[index] = { title, pattern };
  $("cfg-drop-patterns").value = JSON.stringify(rules, null, 2);
}

function deleteDropRule(index) {
  const rules = readDropRulesForUI();
  if (!rules) return;
  rules.splice(index, 1);
  writeDropRulesFromUI(rules);
}

function readCustomShortcutsForUI() {
  try {
    const shortcuts = JSON.parse($("cfg-lark-custom-shortcuts").value || "[]");
    if (!Array.isArray(shortcuts)) return null;
    return shortcuts;
  } catch {
    return null;
  }
}

function writeCustomShortcutsFromUI(shortcuts) {
  $("cfg-lark-custom-shortcuts").value = JSON.stringify(shortcuts || [], null, 2);
  renderCustomShortcuts();
}

function renderCustomShortcuts() {
  const list = $("custom-shortcut-list");
  list.innerHTML = "";
  const shortcuts = readCustomShortcutsForUI();
  if (!shortcuts) {
    const err = document.createElement("div");
    err.className = "preset-empty";
    err.textContent = "自定义快捷键 JSON 格式不正确。";
    list.appendChild(err);
    return;
  }
  if (shortcuts.length === 0) {
    const empty = document.createElement("div");
    empty.className = "preset-empty";
    empty.textContent = "还没有自定义快捷键。";
    list.appendChild(empty);
    return;
  }
  shortcuts.forEach((shortcut, index) => {
    const row = document.createElement("div");
    row.className = "custom-shortcut-row";
    const label = document.createElement("input");
    label.className = "custom-shortcut-label";
    label.placeholder = "按钮名称";
    const command = document.createElement("input");
    command.className = "custom-shortcut-command";
    command.placeholder = "要提交的指令";
    const remove = document.createElement("button");
    remove.className = "custom-shortcut-remove";
    remove.type = "button";
    remove.textContent = "删除";
    label.value = shortcut?.label || "";
    command.value = shortcut?.command || "";
    label.oninput = () => updateCustomShortcut(index, label.value, command.value);
    command.oninput = () => updateCustomShortcut(index, label.value, command.value);
    remove.onclick = () => deleteCustomShortcut(index);
    row.appendChild(label);
    row.appendChild(command);
    row.appendChild(remove);
    list.appendChild(row);
  });
}

function addCustomShortcut() {
  const shortcuts = readCustomShortcutsForUI();
  if (!shortcuts) return;
  shortcuts.push({ label: "", command: "" });
  writeCustomShortcutsFromUI(shortcuts);
}

function updateCustomShortcut(index, label, command) {
  const shortcuts = readCustomShortcutsForUI();
  if (!shortcuts || !shortcuts[index]) return;
  shortcuts[index] = { label, command };
  $("cfg-lark-custom-shortcuts").value = JSON.stringify(shortcuts, null, 2);
}

function deleteCustomShortcut(index) {
  const shortcuts = readCustomShortcutsForUI();
  if (!shortcuts) return;
  shortcuts.splice(index, 1);
  writeCustomShortcutsFromUI(shortcuts);
}

function parseJSONObject(text, label) {
  let value;
  try {
    value = JSON.parse(text || "{}");
  } catch (err) {
    throw new Error(`${label} 格式不正确：${err.message}`);
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} 必须是 JSON 对象`);
  }
  return value;
}

async function saveConfig() {
  const cfg = readConfigForm();
  state.config = await api("/api/config", { method: "PATCH", body: JSON.stringify(cfg) });
  renderConfig();
}

async function testLarkConfig() {
  $("lark-test-start").disabled = true;
  renderLarkTestResult({ steps: [{ name: "测试中", ok: true, message: "正在发送飞书测试通知..." }] });
  try {
    const result = await api("/api/config/lark-test", { method: "POST", body: JSON.stringify(readConfigForm()) });
    renderLarkTestResult(result);
  } finally {
    $("lark-test-start").disabled = false;
  }
}

function renderLarkTestResult(result) {
  const box = $("lark-test-result");
  box.innerHTML = "";
  for (const step of result.steps || []) {
    const row = document.createElement("div");
    row.className = `lark-test-step ${step.ok ? "ok" : "fail"}`;
    row.innerHTML = `<div><strong></strong><span></span></div>`;
    row.querySelector("strong").textContent = step.name || "";
    row.querySelector("span").textContent = step.message || "";
    box.appendChild(row);
  }
}

function agentPresetCommand() {
  return agentCommandForPreset($("cfg-agent-preset").value, $("cfg-agent-custom-command").value);
}

function agentCommandForPreset(preset, customCommand = "") {
  if (preset === "custom") return customCommand.trim();
  return {
    codex: "codex --dangerously-bypass-approvals-and-sandbox",
    opencode: "opencode --dangerously-skip-permissions",
    claude: "claude --dangerously-skip-permissions",
    gemini: "gemini --yolo",
  }[preset] || "";
}

function presetForAgentCommand(command = "") {
  const normalized = String(command || "").trim();
  for (const preset of ["codex", "opencode", "claude", "gemini"]) {
    if (normalized === agentCommandForPreset(preset)) return { preset, customCommand: "" };
  }
  if (normalized) return { preset: "custom", customCommand: normalized };
  return { preset: "", customCommand: "" };
}

function renderDefaultAgentPresetFromStartPreset(startPresets = {}) {
  const commands = Array.isArray(startPresets?.[DEFAULT_AGENT_PRESET_CODE]?.commands) ? startPresets[DEFAULT_AGENT_PRESET_CODE].commands : [];
  const firstCommand = commands.find((command) => String(command || "").trim());
  const matched = presetForAgentCommand(firstCommand || "");
  $("cfg-agent-preset").value = matched.preset;
  $("cfg-agent-custom-command").value = matched.customCommand;
}

function renderAgentPresetControls() {
  const custom = $("cfg-agent-preset").value === "custom";
  $("cfg-agent-custom-command").hidden = !custom;
  $("cfg-agent-custom-command").classList.toggle("hidden", !custom);
}

function setAgentPresetStatus(message, ok = null) {
  const el = $("agent-preset-status");
  el.textContent = message || "";
  el.classList.toggle("ok", ok === true);
  el.classList.toggle("fail", ok === false);
}

function ensureDefaultAgentPreset() {
  const presets = readStartPresetsForUI();
  if (!presets) {
    setAgentPresetStatus("启动预设 JSON 格式不正确，先修正后再更新默认 Agent。", false);
    return;
  }
  const command = agentPresetCommand();
  if (!command) {
    delete presets[DEFAULT_AGENT_PRESET_CODE];
    writeStartPresetsFromUI(presets);
    setAgentPresetStatus(`已清空 ${DEFAULT_AGENT_PRESET_CODE} 号默认 Agent 预设。`, true);
    return;
  }
  presets[DEFAULT_AGENT_PRESET_CODE] = { commands: [command] };
  writeStartPresetsFromUI(presets);
  setAgentPresetStatus(`已更新 ${DEFAULT_AGENT_PRESET_CODE} 号默认 Agent 预设：${command}`, true);
}

function renderOnboardingAgentControls() {
  const custom = $("onboarding-agent-preset").value === "custom";
  $("onboarding-agent-custom-command").hidden = !custom;
  $("onboarding-agent-custom-command").classList.toggle("hidden", !custom);
}

function setOnboardingAgentStatus(message, ok = null) {
  const el = $("onboarding-agent-status");
  el.textContent = message || "";
  el.classList.toggle("ok", ok === true);
  el.classList.toggle("fail", ok === false);
}

function setDefaultAgentPresetInConfig(cfg, preset, customCommand, defaultSessionName = "") {
  const command = agentCommandForPreset(preset, customCommand);
  const sessionName = (defaultSessionName || cfg.lark_default_session_name || DEFAULT_SESSION_NAME).trim() || DEFAULT_SESSION_NAME;
  const presets = cfg.session_start_presets && typeof cfg.session_start_presets === "object" && !Array.isArray(cfg.session_start_presets)
    ? { ...cfg.session_start_presets }
    : {};
  if (command) {
    presets[DEFAULT_AGENT_PRESET_CODE] = { commands: [command] };
  }
  return { ...cfg, lark_default_session_name: sessionName, session_start_presets: presets, onboarding_completed: true };
}

async function completeOnboarding(options = {}) {
  if (!state.config) await loadConfig();
  const preset = options.skip ? "" : $("onboarding-agent-preset").value;
  const defaultSessionName = $("onboarding-default-session-name").value.trim() || DEFAULT_SESSION_NAME;
  const customCommand = $("onboarding-agent-custom-command").value.trim();
  if (preset === "custom" && !customCommand) {
    setOnboardingAgentStatus("请填写自定义 Agent 启动命令，或选择跳过。", false);
    return;
  }
  state.config = setDefaultAgentPresetInConfig(state.config, preset, customCommand, defaultSessionName);
  state.config = await api("/api/config", { method: "PATCH", body: JSON.stringify(state.config) });
  $("onboarding-dialog").close();
  if (!options.skip) {
    renderConfig();
    setConfigTab("config-session");
    $("config-dialog").showModal();
  }
}

function readNamePresetsForUI() {
  try {
    const presets = JSON.parse($("cfg-session-name-presets").value || "{}");
    if (!presets || typeof presets !== "object" || Array.isArray(presets)) return null;
    return presets;
  } catch {
    return null;
  }
}

function readStartPresetsForUI() {
  try {
    const presets = JSON.parse($("cfg-session-start-presets").value || "{}");
    if (!presets || typeof presets !== "object" || Array.isArray(presets)) return null;
    return presets;
  } catch {
    return null;
  }
}

function writeNamePresetsFromUI(presets) {
  $("cfg-session-name-presets").value = JSON.stringify(presets || {}, null, 2);
  renderNamePresets();
  state.startupJSONDirty = false;
  updateStartupJSONPreview();
}

function writeStartPresetsFromUI(presets) {
  $("cfg-session-start-presets").value = JSON.stringify(presets || {}, null, 2);
  state.startupJSONDirty = false;
  updateStartupJSONPreview();
}

function setPresetStatus(message, ok = null) {
  const el = $("preset-status");
  el.textContent = message || "";
  el.classList.toggle("ok", ok === true);
  el.classList.toggle("fail", ok === false);
}

function saveNamePresetFromForm() {
  const presets = readNamePresetsForUI();
  if (!presets) {
    setPresetStatus("会话名预设 JSON 格式不正确，先修正后再添加会话名。", false);
    return;
  }
  const name = $("preset-session-name").value.trim();
  if (!name) {
    setPresetStatus("请填写会话名。", false);
    return;
  }
  if (presets[name]) {
    setPresetStatus(`“${name}”已存在，可以直接在下方添加命令。`, null);
    return;
  }
  presets[name] = { commands: [] };
  writeNamePresetsFromUI(presets);
  $("preset-session-name").value = "";
  setPresetStatus(`已添加会话名“${name}”。`, true);
}

function clearNamePresetForm() {
  $("preset-session-name").value = "";
  state.editingPresetCommand = null;
  renderNamePresets();
  setPresetStatus("");
}

function addPresetCommand(name, command) {
  const presets = readNamePresetsForUI();
  if (!presets?.[name]) return;
  const value = command.trim();
  if (!value) {
    setPresetStatus(`请填写“${name}”要添加的命令。`, false);
    return;
  }
  if (!Array.isArray(presets[name].commands)) presets[name].commands = [];
  presets[name].commands.push(value);
  writeNamePresetsFromUI(presets);
  setPresetStatus(`已为“${name}”添加命令。`, true);
}

function editPresetCommand(name, index) {
  state.editingPresetCommand = { name, index };
  renderNamePresets();
  setPresetStatus(`正在编辑“${name}”第 ${index + 1} 条命令。`, null);
}

function updatePresetCommand(name, index, command) {
  const presets = readNamePresetsForUI();
  const commands = presets?.[name]?.commands;
  if (!Array.isArray(commands)) return;
  const value = command.trim();
  if (!value) {
    setPresetStatus("命令不能为空。", false);
    return;
  }
  commands[index] = value;
  state.editingPresetCommand = null;
  writeNamePresetsFromUI(presets);
  setPresetStatus(`已更新“${name}”第 ${index + 1} 条命令。`, true);
}

function cancelEditPresetCommand() {
  state.editingPresetCommand = null;
  renderNamePresets();
  setPresetStatus("");
}

function addCommandRow(container, value = "", onChange) {
  const row = document.createElement("div");
  row.className = "preset-command-row";
  row.innerHTML = `
    <input class="preset-command-input" placeholder="codex">
    <button class="preset-command-remove" type="button">删除</button>
  `;
  const input = row.querySelector(".preset-command-input");
  input.value = value;
  input.oninput = () => {
    if (typeof onChange === "function") onChange();
  };
  row.querySelector(".preset-command-remove").onclick = () => {
    row.remove();
    if (typeof onChange === "function") onChange();
    if (container.querySelectorAll(".preset-command-input").length === 0) {
      addCommandRow(container, "", onChange);
    }
  };
  container.appendChild(row);
}

function renderPreStartCommandRows() {
  const commands = $("cfg-prestart-command").value.split("\n");
  const list = $("prestart-command-list");
  list.innerHTML = "";
  const rows = commands.some((line) => line.trim()) ? commands : [""];
  for (const command of rows) addCommandRow(list, command, syncPreStartCommandsFromRows);
}

function addPreStartCommandRow(value = "") {
  addCommandRow($("prestart-command-list"), value, syncPreStartCommandsFromRows);
  syncPreStartCommandsFromRows();
}

function syncPreStartCommandsFromRows() {
  $("cfg-prestart-command").value = [...$("prestart-command-list").querySelectorAll(".preset-command-input")]
    .map((input) => input.value.trim())
    .filter(Boolean)
    .join("\n");
  state.startupJSONDirty = false;
  updateStartupJSONPreview();
}

function deletePresetCommand(name, index) {
  const presets = readNamePresetsForUI();
  const commands = presets?.[name]?.commands;
  if (!Array.isArray(commands)) return;
  commands.splice(index, 1);
  writeNamePresetsFromUI(presets);
  setPresetStatus(`已删除“${name}”的第 ${index + 1} 条命令。`, true);
}

function deletePresetSession(name) {
  const presets = readNamePresetsForUI();
  if (!presets?.[name]) return;
  delete presets[name];
  if (state.editingPresetCommand?.name === name) state.editingPresetCommand = null;
  writeNamePresetsFromUI(presets);
  setPresetStatus(`已删除会话名“${name}”。`, true);
}

function renderNamePresets() {
  const list = $("preset-list");
  list.innerHTML = "";
  const presets = readNamePresetsForUI();
  if (!presets) {
    setPresetStatus("会话名预设 JSON 格式不正确，列表无法同步。", false);
    return;
  }
  const names = Object.keys(presets).sort((a, b) => a.localeCompare(b));
  if (names.length === 0) {
    const empty = document.createElement("div");
    empty.className = "preset-empty";
    empty.textContent = "还没有会话名预设。";
    list.appendChild(empty);
    return;
  }
  for (const name of names) {
    const commands = Array.isArray(presets[name]?.commands) ? presets[name].commands : [];
    const item = document.createElement("article");
    item.className = "preset-item";
    const head = document.createElement("div");
    head.className = "preset-item-head";
    const title = document.createElement("strong");
    title.textContent = name;
    const deleteSession = document.createElement("button");
    deleteSession.type = "button";
    deleteSession.className = "preset-session-delete";
    deleteSession.textContent = "删除会话名";
    deleteSession.onclick = () => deletePresetSession(name);
    head.appendChild(title);
    head.appendChild(deleteSession);
    item.appendChild(head);
    const display = document.createElement("div");
    display.className = "preset-command-display";
    item.appendChild(display);
    if (commands.length === 0) {
      const empty = document.createElement("div");
      empty.className = "preset-command-display-row";
      empty.textContent = "未配置命令";
      display.appendChild(empty);
    }
    commands.forEach((command, index) => {
      const row = document.createElement("div");
      row.className = "preset-command-display-row";
      const editing = state.editingPresetCommand?.name === name && state.editingPresetCommand.index === index;
      const commandEl = editing ? document.createElement("input") : document.createElement("code");
      if (editing) {
        commandEl.className = "preset-inline-command-input";
        commandEl.value = command;
      } else {
        commandEl.textContent = command;
      }
      const actions = document.createElement("div");
      actions.className = "preset-item-actions";
      if (editing) {
        const save = document.createElement("button");
        save.type = "button";
        save.textContent = "保存";
        save.onclick = () => updatePresetCommand(name, index, commandEl.value);
        const cancel = document.createElement("button");
        cancel.type = "button";
        cancel.textContent = "取消";
        cancel.onclick = cancelEditPresetCommand;
        actions.appendChild(save);
        actions.appendChild(cancel);
      } else {
        const edit = document.createElement("button");
        edit.className = "preset-edit";
        edit.type = "button";
        edit.textContent = "编辑";
        edit.onclick = () => editPresetCommand(name, index);
        const remove = document.createElement("button");
        remove.className = "preset-delete";
        remove.type = "button";
        remove.textContent = "删除";
        remove.onclick = () => deletePresetCommand(name, index);
        actions.appendChild(edit);
        actions.appendChild(remove);
      }
      row.appendChild(commandEl);
      row.appendChild(actions);
      display.appendChild(row);
    });
    const addRow = document.createElement("div");
    addRow.className = "preset-command-add-row";
    const input = document.createElement("input");
    input.className = "preset-new-command-input";
    input.placeholder = `给“${name}”添加命令`;
    const add = document.createElement("button");
    add.type = "button";
    add.textContent = "添加命令";
    add.onclick = () => addPresetCommand(name, input.value);
    addRow.appendChild(input);
    addRow.appendChild(add);
    item.appendChild(addRow);
    list.appendChild(item);
  }
}

function startupJSONValue() {
  return {
    session_pre_start_command: $("cfg-prestart-command").value,
    session_name_presets: parseJSONObject($("cfg-session-name-presets").value || "{}", "会话名预设 JSON"),
    session_start_presets: parseJSONObject($("cfg-session-start-presets").value || "{}", "开始命令后缀预设 JSON"),
  };
}

function updateStartupJSONPreview() {
  if (state.startupJSONDirty) return;
  $("startup-json-preview").value = JSON.stringify(startupJSONValue(), null, 2);
}

function syncStartupJSONPreview(options = {}) {
  const preview = $("startup-json-preview");
  let value;
  try {
    value = JSON.parse(preview.value || "{}");
  } catch (err) {
    const msg = `启动配置 JSON 格式不正确：${err.message}`;
    if (options.throwOnError) throw new Error(msg);
    $("config-error").textContent = msg;
    return false;
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    const msg = "启动配置 JSON 必须是对象";
    if (options.throwOnError) throw new Error(msg);
    $("config-error").textContent = msg;
    return false;
  }
  const preStart = value.session_pre_start_command ?? "";
  if (typeof preStart !== "string") {
    const msg = "session_pre_start_command 必须是字符串";
    if (options.throwOnError) throw new Error(msg);
    $("config-error").textContent = msg;
    return false;
  }
  const namePresets = value.session_name_presets ?? {};
  const startPresets = value.session_start_presets ?? {};
  if (!namePresets || typeof namePresets !== "object" || Array.isArray(namePresets)) {
    const msg = "session_name_presets 必须是对象";
    if (options.throwOnError) throw new Error(msg);
    $("config-error").textContent = msg;
    return false;
  }
  if (!startPresets || typeof startPresets !== "object" || Array.isArray(startPresets)) {
    const msg = "session_start_presets 必须是对象";
    if (options.throwOnError) throw new Error(msg);
    $("config-error").textContent = msg;
    return false;
  }
  $("cfg-prestart-command").value = preStart;
  $("cfg-session-name-presets").value = JSON.stringify(namePresets, null, 2);
  $("cfg-session-start-presets").value = JSON.stringify(startPresets, null, 2);
  state.startupJSONDirty = false;
  renderPreStartCommandRows();
  renderNamePresets();
  $("config-error").textContent = "";
  if (!options.keepText) updateStartupJSONPreview();
  return true;
}

function toggleStartupJSONPreview() {
  const preview = $("startup-json-preview");
  const hidden = !preview.hidden;
  preview.hidden = hidden;
  preview.classList.toggle("hidden", hidden);
  $("startup-json-toggle").textContent = hidden ? "查看启动配置 JSON" : "隐藏启动配置 JSON";
  if (!hidden) updateStartupJSONPreview();
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
  sendWS({ type: "submit", data: text });
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
    await openConfigDialog();
  } catch (err) {
    console.error(err);
  }
};

$("config-cancel").onclick = () => {
  stopLarkRegistrationPolling();
  $("config-dialog").close();
};

$("config-prev").onclick = () => moveConfigStep(-1);
$("config-next").onclick = () => moveConfigStep(1);

$("config-form").onsubmit = async (ev) => {
  ev.preventDefault();
  try {
    await saveConfig();
    $("config-dialog").close();
  } catch (err) {
    $("config-error").textContent = err.message || String(err);
  }
};

$("lark-register-start").onclick = () => startLarkRegistration().catch((err) => {
  $("lark-register-status").textContent = err.message || String(err);
});

$("lark-test-start").onclick = () => testLarkConfig().catch((err) => {
  renderLarkTestResult({ steps: [{ name: "测试失败", ok: false, message: err.message || String(err) }] });
  $("lark-test-start").disabled = false;
});

$("cfg-agent-preset").onchange = () => {
  renderAgentPresetControls();
  setAgentPresetStatus("");
  ensureDefaultAgentPreset();
};

$("cfg-agent-custom-command").onchange = ensureDefaultAgentPreset;

$("preset-save").onclick = saveNamePresetFromForm;
$("preset-clear").onclick = clearNamePresetForm;
$("prestart-command-add").onclick = () => addPreStartCommandRow("");
$("drop-rule-add").onclick = addDropRule;
$("custom-shortcut-add").onclick = addCustomShortcut;
$("startup-json-toggle").onclick = toggleStartupJSONPreview;
$("startup-json-preview").oninput = () => {
  state.startupJSONDirty = true;
  syncStartupJSONPreview({ keepText: true });
};
$("cfg-session-name-presets").oninput = () => {
  state.startupJSONDirty = false;
  renderNamePresets();
  updateStartupJSONPreview();
};
$("cfg-session-start-presets").oninput = () => {
  state.startupJSONDirty = false;
  updateStartupJSONPreview();
};
$("cfg-prestart-command").oninput = () => {
  state.startupJSONDirty = false;
  renderPreStartCommandRows();
  updateStartupJSONPreview();
};

document.querySelectorAll(".config-tab").forEach((tab) => {
  tab.onclick = () => {
    setConfigTab(tab.dataset.configTarget);
  };
});

$("onboarding-agent-preset").onchange = () => {
  renderOnboardingAgentControls();
  setOnboardingAgentStatus("");
};

$("onboarding-config").onclick = async () => {
  try {
    await completeOnboarding();
  } catch (err) {
    setOnboardingAgentStatus(err.message || String(err), false);
  }
};

$("onboarding-later").onclick = async () => {
  try {
    await completeOnboarding({ skip: true });
  } catch (err) {
    setOnboardingAgentStatus(err.message || String(err), false);
  }
};

async function startLarkRegistration() {
  stopLarkRegistrationPolling();
  $("lark-register-start").disabled = true;
  $("lark-register-panel").hidden = false;
  $("lark-register-panel").classList.remove("hidden");
  $("lark-register-status").textContent = "正在创建扫码任务...";
  const reg = await api("/api/lark-app-registration", { method: "POST", body: JSON.stringify({ brand: "feishu" }) });
  $("lark-register-code").textContent = reg.user_code;
  $("lark-register-link").href = reg.verification_uri_complete;
  $("lark-register-qr").src = `/api/lark-app-registration/qr?text=${encodeURIComponent(reg.verification_uri_complete)}`;
  $("lark-register-status").textContent = "请用飞书扫码确认创建应用...";
  $("lark-register-panel").hidden = false;
  $("lark-register-panel").classList.remove("hidden");
  pollLarkRegistration(reg);
}

function pollLarkRegistration(reg) {
  const intervalMs = Math.max(Number(reg.interval || 5), 2) * 1000;
  const startedAt = Date.now();
  const expiresAt = startedAt + Math.max(Number(reg.expires_in || 3600), 60) * 1000;
  const tick = async () => {
    if (Date.now() >= expiresAt) {
      $("lark-register-status").textContent = "二维码已过期，请重新开始。";
      $("lark-register-start").disabled = false;
      return;
    }
    try {
      const result = await api("/api/lark-app-registration/poll", {
        method: "POST",
        body: JSON.stringify({ brand: reg.brand || "feishu", device_code: reg.device_code }),
      });
      if (result.pending) {
        const left = Math.max(0, Math.ceil((expiresAt - Date.now()) / 1000));
        $("lark-register-status").textContent = `等待扫码确认，剩余 ${left} 秒...`;
        state.larkRegistrationTimer = setTimeout(tick, intervalMs);
        return;
      }
      applyLarkRegistrationResult(result);
    } catch (err) {
      $("lark-register-status").textContent = err.message || String(err);
      $("lark-register-start").disabled = false;
    }
  };
  state.larkRegistrationTimer = setTimeout(tick, intervalMs);
}

function applyLarkRegistrationResult(result) {
  $("cfg-lark-app-id").value = result.app_id || "";
  $("cfg-lark-app-secret").value = result.app_secret || "";
  if (result.lark_notify_receive_id) $("cfg-lark-receive-id").value = result.lark_notify_receive_id;
  $("lark-register-status").textContent = "扫码成功，已填入飞书应用配置。点击“保存”。";
  $("lark-register-start").disabled = false;
  stopLarkRegistrationPolling();
}

function stopLarkRegistrationPolling() {
  if (state.larkRegistrationTimer) {
    clearTimeout(state.larkRegistrationTimer);
    state.larkRegistrationTimer = null;
  }
}

$("help-open").onclick = () => $("help-dialog").showModal();
$("help-close").onclick = () => $("help-dialog").close();

document.querySelectorAll(".help-tab").forEach((tab) => {
  tab.onclick = () => {
    const targetID = tab.dataset.helpTarget;
    document.querySelectorAll(".help-tab").forEach((item) => item.classList.toggle("active", item === tab));
    document.querySelectorAll(".help-panel").forEach((panel) => panel.classList.toggle("active", panel.id === targetID));
  };
});

function clipboardImageFile(ev) {
  const files = [...(ev.clipboardData?.files || [])];
  const fromFiles = files.find((f) => f.type.startsWith("image/"));
  if (fromFiles) return fromFiles;
  const items = [...(ev.clipboardData?.items || [])];
  const imageItem = items.find((item) => item.kind === "file" && item.type.startsWith("image/"));
  return imageItem?.getAsFile?.() || null;
}

async function handleImagePaste(ev) {
  if (ev.easyTerminalImagePasteHandled) return;
  if (!state.active) return;
  const file = clipboardImageFile(ev);
  if (!file) return;
  ev.easyTerminalImagePasteHandled = true;
  ev.preventDefault();
  const form = new FormData();
  form.append("file", file, file.name || "paste.png");
  form.append("mime_type", file.type);
  const res = await api(`/api/sessions/${state.active}/uploads`, { method: "POST", body: form });
  const target = ev.target;
  if (target === $("composer-input")) {
    $("composer-input").value += `${res.path}\n`;
  } else {
    sendWS({ type: "input", data: `${res.path} ` });
    state.term?.focus?.();
  }
}

$("terminal").addEventListener("paste", handleImagePaste, true);
document.addEventListener("paste", handleImagePaste);

setInterval(loadSessions, 3000);
loadSessions().catch(console.error);
loadQuick().catch(console.error);
loadConfig().then(() => setTimeout(() => maybeShowOnboarding().catch(console.error), 250)).catch(console.error);

if (typeof window !== "undefined") {
  window.easyTerminalApp = {
    state,
    sendComposer,
    renderSessions,
    setNotify,
    loadConfig,
    saveConfig,
    testLarkConfig,
    maybeShowOnboarding,
    openConfigDialog,
    ensureDefaultAgentPreset,
    saveNamePresetFromForm,
    renderNamePresets,
    addPreStartCommandRow,
    visibleSessions,
  };
}
