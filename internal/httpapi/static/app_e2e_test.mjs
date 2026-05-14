import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

class FakeElement {
  constructor(id = "", tag = "div") {
    this.id = id;
    this.tag = tag;
    this.children = [];
    this.value = "";
    this.checked = false;
    this.textContent = "";
    this.className = "";
    this.title = "";
    this.type = "";
    this.dataset = {};
    this.onclick = null;
    this.onchange = null;
    this.oninput = null;
    this.onkeydown = null;
    this.onsubmit = null;
    this.parent = null;
    this._bySelector = new Map();
    this._listeners = new Map();
    this._hidden = false;
    this.classList = {
      add: (name) => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
        classes.add(name);
        if (name === "hidden") this.hidden = true;
        this.className = [...classes].join(" ");
      },
      remove: (name) => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
        classes.delete(name);
        if (name === "hidden") this.hidden = false;
        this.className = [...classes].join(" ");
      },
      toggle: (name, force) => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
      const enabled = force === undefined ? !classes.has(name) : Boolean(force);
        if (enabled) {
          classes.add(name);
          if (name === "hidden") this.hidden = true;
        } else {
          classes.delete(name);
          if (name === "hidden") this.hidden = false;
        }
        this.className = [...classes].join(" ");
        return enabled;
      },
    };
  }

  set innerHTML(value) {
    this._innerHTML = value;
    this.children = [];
    this._bySelector = new Map();
    if (value.includes("notify-input")) {
      for (const selector of [
        ".session-name",
        ".notify-input",
        ".notify-state",
        ".delete-btn",
        ".notify-row",
        ".start-btn",
      ]) {
        const child = new FakeElement("", selector === ".notify-input" ? "input" : "div");
        child.parent = this;
        this._bySelector.set(selector, child);
      }
      this._bySelector.get(".notify-input").type = "checkbox";
    }
    if (value.includes("chip-close")) {
      const span = new FakeElement("", "span");
      const close = new FakeElement("", "button");
      this._bySelector.set("span", span);
      this._bySelector.set(".chip-close", close);
    }
    if (value.includes("<strong")) {
      this._bySelector.set("strong", new FakeElement("", "strong"));
      this._bySelector.set("span", new FakeElement("", "span"));
    }
    if (value.includes("<code")) {
      this._bySelector.set("code", new FakeElement("", "code"));
    }
    if (value.includes("preset-edit")) {
      this._bySelector.set(".preset-edit", new FakeElement("", "button"));
      this._bySelector.set(".preset-delete", new FakeElement("", "button"));
    }
    if (value.includes("preset-command-input")) {
      const input = new FakeElement("", "input");
      input.className = "preset-command-input";
      const remove = new FakeElement("", "button");
      remove.className = "preset-command-remove";
      this._bySelector.set(".preset-command-input", input);
      this._bySelector.set(".preset-command-remove", remove);
    }
  }

  get innerHTML() {
    return this._innerHTML || "";
  }

  set hidden(value) {
    this._hidden = Boolean(value);
  }

  get hidden() {
    return Boolean(this._hidden);
  }

  querySelector(selector) {
    return this._bySelector.get(selector) || null;
  }

  querySelectorAll(selector) {
    if (selector === "span") return this.children.filter((child) => child.tag === "span");
    const out = [];
    const visit = (node) => {
      if (selector.startsWith(".") && node.className.split(/\s+/).includes(selector.slice(1))) out.push(node);
      for (const child of node.children || []) visit(child);
      for (const child of node._bySelector?.values?.() || []) visit(child);
    };
    visit(this);
    return out;
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  remove() {
    if (!this.parent) return;
    const index = this.parent.children.indexOf(this);
    if (index >= 0) this.parent.children.splice(index, 1);
    this.parent = null;
  }

  focus() {
    this.focused = true;
  }

  clear() {
    this.cleared = true;
  }

  getBoundingClientRect() {
    return this.rect || { left: 0, width: 8 };
  }

  requestSubmit() {
    this.onsubmit?.({ preventDefault() {} });
  }

  showModal() {
    this.open = true;
  }

  close() {
    this.open = false;
  }

  addEventListener(type, listener) {
    const listeners = this._listeners.get(type) || [];
    listeners.push(listener);
    this._listeners.set(type, listeners);
  }

  async dispatchEvent(event) {
    event.target ||= this;
    for (const listener of this._listeners.get(event.type) || []) {
      await listener(event);
    }
  }
}

const ids = [
  "sessions",
  "quick-list",
  "composer-input",
  "composer",
  "new-session",
  "session-name",
  "session-search",
  "quick-form",
  "quick-text",
  "quick-dialog",
  "quick-cancel",
  "onboarding-dialog",
  "onboarding-default-session-name",
  "onboarding-agent-preset",
  "onboarding-agent-custom-command",
  "onboarding-agent-status",
  "onboarding-config",
  "onboarding-later",
  "config-open",
  "config-dialog",
  "config-form",
  "config-cancel",
  "config-save",
  "config-prev",
  "config-next",
  "config-error",
  "help-open",
  "help-dialog",
  "help-close",
  "lark-register-start",
  "lark-register-panel",
  "lark-register-status",
  "lark-register-code",
  "lark-register-link",
  "lark-register-qr",
  "lark-app-console-link",
  "lark-copy-scope",
  "lark-copy-permission-json",
  "lark-permission-status",
  "lark-group-scope",
  "lark-test-start",
  "lark-test-result",
  "cfg-fast-waiting",
  "cfg-conservative-waiting",
  "cfg-auto-refresh-interval",
  "cfg-lark-max-lines",
  "cfg-lark-app-id",
  "cfg-lark-app-secret",
  "cfg-lark-receive-id",
  "cfg-lark-default-session-name",
  "cfg-lark-session-chat-prefix",
  "cfg-lark-mention-enabled",
  "cfg-prestart-command",
  "cfg-drop-patterns",
  "drop-rule-list",
  "drop-rule-add",
  "cfg-lark-custom-shortcuts",
  "custom-shortcut-list",
  "custom-shortcut-add",
  "cfg-session-name-presets",
  "cfg-session-start-presets",
  "cfg-agent-preset",
  "cfg-agent-custom-command",
  "agent-preset-status",
  "preset-session-name",
  "preset-save",
  "preset-clear",
  "preset-status",
  "preset-list",
  "prestart-command-list",
  "prestart-command-add",
  "startup-json-toggle",
  "startup-json-preview",
  "active-title",
  "terminal",
];
const elements = Object.fromEntries(ids.map((id) => [id, new FakeElement(id)]));
elements["lark-register-panel"].hidden = true;
elements["startup-json-preview"].hidden = true;
const helpTabs = ["help-start", "help-terminal"].map((targetID, index) => {
  const tab = new FakeElement("", "button");
  tab.dataset.helpTarget = targetID;
  tab.className = index === 0 ? "help-tab active" : "help-tab";
  return tab;
});
const configTabs = ["config-session", "config-lark", "config-notify", "config-startup"].map((targetID, index) => {
  const tab = new FakeElement("", "button");
  tab.dataset.configTarget = targetID;
  tab.className = index === 0 ? "config-tab active" : "config-tab";
  return tab;
});
const configPanels = ["config-session", "config-lark", "config-notify", "config-startup"].map((id, index) => {
  const panel = new FakeElement(id, "section");
  panel.className = index === 0 ? "config-panel active" : "config-panel";
  return panel;
});
const helpPanels = ["help-start", "help-terminal"].map((id, index) => {
  const panel = new FakeElement(id, "section");
  panel.className = index === 0 ? "help-panel active" : "help-panel";
  return panel;
});
let terminalDOMRows = [];

function terminalRow(segments) {
  const row = new FakeElement("", "div");
  row.rect = { left: 0, width: 960 };
  row.children = segments.map(([text, left]) => {
    const span = new FakeElement("", "span");
    span.textContent = text;
    span.rect = { left, width: text.length * 8 };
    return span;
  });
  row.textContent = segments.map(([text]) => text).join("");
  return row;
}

const fetchCalls = [];
const sentMessages = [];
const localStorageData = new Map();

class FakeWebSocket {
  static OPEN = 1;
}

const context = {
  console,
  setInterval() {},
  clearTimeout,
  setTimeout,
  requestAnimationFrame(callback) {
    return setTimeout(callback, 0);
  },
  TextDecoder,
  URLSearchParams,
  FormData: class {
    append() {}
  },
  WebSocket: FakeWebSocket,
  Terminal: class {
    constructor() {
      this.cols = 120;
      this.rows = 36;
    }
    loadAddon() {}
    open() {}
    onData() {}
    dispose() {}
    write(_text, callback) {
      callback?.();
    }
    clear() {}
    get buffer() {
      return { active: { length: 0, viewportY: 0, getLine() { return null; } } };
    }
  },
  FitAddon: { FitAddon: class { fit() {} } },
  location: { protocol: "http:", host: "localhost:8080" },
  document: {
    getElementById(id) {
      return elements[id];
    },
    createElement(tag) {
      return new FakeElement("", tag);
    },
    querySelectorAll(selector) {
      if (selector === "#terminal .xterm-rows > div") return terminalDOMRows;
      if (selector === ".help-tab") return helpTabs;
      if (selector === ".help-panel") return helpPanels;
      if (selector === ".config-tab") return configTabs;
      if (selector === ".config-panel") return configPanels;
      return [];
    },
    addEventListener() {},
  },
  window: {
    addEventListener() {},
    removeEventListener() {},
    localStorage: {
      getItem(key) {
        return localStorageData.get(key) || null;
      },
      setItem(key, value) {
        localStorageData.set(key, String(value));
      },
    },
  },
  navigator: {
    clipboard: {
      async writeText(text) {
        context.copiedText = text;
      },
    },
  },
  copiedText: "",
  fetch: async (path, options = {}) => {
    fetchCalls.push({ path, options });
    if (path === "/api/sessions" && !options.method) {
      return jsonResponse([]);
    }
    if (path === "/api/quick-commands" && !options.method) {
      return jsonResponse([]);
    }
    if (path === "/api/config" && !options.method) {
      return jsonResponse({
        fast_waiting_transition_ms: 300,
        conservative_waiting_transition_ms: 700,
        lark_auto_refresh_interval_ms: 5000,
        lark_notify_max_lines: 300,
        lark_app_id: "app-id",
        lark_app_secret: "secret",
        lark_notify_receive_id: "ou_1",
        lark_mention_enabled: true,
        lark_default_session_name: "默认会话",
        lark_session_chat_prefix: "ET · ",
        onboarding_completed: false,
        session_pre_start_command: "",
        lark_notify_drop_line_patterns: [],
        lark_custom_shortcuts: [],
        session_name_presets: {},
        session_start_presets: {},
      });
    }
    if (path === "/api/config" && options.method === "PATCH") {
      return jsonResponse(JSON.parse(options.body));
    }
    if (path === "/api/lark-app-registration" && options.method === "POST") {
      return jsonResponse({
        device_code: "dev-1",
        user_code: "USER-1",
        verification_uri_complete: "https://open.feishu.cn/page/cli?user_code=USER-1",
        expires_in: 3600,
        interval: 5,
        brand: "feishu",
      });
    }
    if (path === "/api/config/lark-test" && options.method === "POST") {
      return jsonResponse({
        ok: true,
        steps: [
          { name: "配置完整性", ok: true, message: "必填项已填写" },
          { name: "发送测试通知", ok: true, message: "已发送" },
        ],
      });
    }
    if (path === "/api/sessions/sess-1/uploads" && options.method === "POST") {
      return jsonResponse({ path: "/tmp/easy-terminal-test/paste.png" }, 201);
    }
    return jsonResponse({}, 200);
  },
};
context.window.window = context.window;

function jsonResponse(data, status = 200) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => data,
  };
}

vm.createContext(context);
vm.runInContext(fs.readFileSync(new URL("./app.js", import.meta.url), "utf8"), context);
await Promise.resolve();
await Promise.resolve();

const app = context.window.easyTerminalApp;
assert.ok(app, "app test API is exposed");
assert.equal(app.standardTerminal.cols, 120);
assert.equal(app.standardTerminal.rows, 36);
assert.equal(app.standardTerminal.fontFamily, "Menlo, Consolas, monospace");
assert.equal(app.standardTerminal.fontSize, 13);
assert.equal(app.standardTerminal.lineHeight, 1.2);

await app.loadConfig();
await app.maybeShowOnboarding();
assert.notEqual(elements["onboarding-dialog"].open, true, "first visit should not show onboarding choice dialog");
assert.equal(elements["config-dialog"].open, true, "first visit should open config dialog directly");
assert.ok(configTabs[0].className.includes("active"), "first visit should start from session config tab");
let onboardingPatch = fetchCalls.find((call) => call.path === "/api/config" && call.options.method === "PATCH");
assert.ok(onboardingPatch, "onboarding should PATCH config");
let onboardingConfig = JSON.parse(onboardingPatch.options.body);
assert.equal(onboardingConfig.onboarding_completed, true, "onboarding should be marked as completed in config");
assert.equal(onboardingConfig.lark_default_session_name, "默认会话");
assert.equal(elements["config-prev"].disabled, true, "previous should be disabled on first config tab");
assert.equal(elements["config-next"].disabled, false, "next should be enabled on first config tab");
elements["config-next"].onclick();
assert.ok(configTabs[1].className.includes("active"), "next should move to the next config tab");
assert.equal(elements["config-prev"].disabled, false, "previous should be enabled after moving forward");
elements["config-prev"].onclick();
assert.ok(configTabs[0].className.includes("active"), "previous should move back to session config tab");
elements["config-next"].onclick();
elements["config-next"].onclick();
elements["config-next"].onclick();
assert.ok(configTabs[3].className.includes("active"), "next should stop at the last config tab");
assert.equal(elements["config-next"].disabled, true, "next should be disabled on last config tab");
elements["config-dialog"].close();
await app.maybeShowOnboarding();
assert.notEqual(elements["onboarding-dialog"].open, true, "completed onboarding should not reopen");

app.state.active = "sess-1";
app.state.socket = {
  readyState: FakeWebSocket.OPEN,
  send(payload) {
    sentMessages.push(JSON.parse(payload));
  },
};
app.state.term = {
  cols: 160,
  rows: 48,
  resize(cols, rows) {
    this.cols = cols;
    this.rows = rows;
  },
};
app.resizeTerm();
assert.deepEqual(sentMessages.pop(), { type: "resize", cols: 120, rows: 36 });

elements["composer-input"].value = "echo button";
elements.composer.requestSubmit();
assert.deepEqual(sentMessages.pop(), { type: "submit", data: "echo button" });
assert.equal(elements["composer-input"].value, "");

app.state.term = {
  cols: 120,
  rows: 2,
  buffer: {
    active: {
      length: 4,
      viewportY: 1,
      getLine(index) {
        const values = ["old hidden", "visible one", "visible two", "new hidden"];
        return { translateToString: () => values[index] };
      },
    },
  },
};
app.state.pendingTerminalWrite = Promise.resolve();
await app.syncSnapshotNow();
assert.deepEqual(sentMessages.pop(), { type: "snapshot", data: "old hidden\nvisible one\nvisible two\nnew hidden", source: "buffer" });

app.state.term = {
  cols: 120,
  rows: 2,
  buffer: {
    active: {
      length: 0,
      viewportY: 0,
      getLine() {
        return null;
      },
    },
  },
};

terminalDOMRows = [
  terminalRow([["/model", 0]]),
  terminalRow([["/model choose what model and reasoning effort to use", 0]]),
  terminalRow([["Select Model and Effort", 0]]),
  terminalRow([["Access legacy models by running codex -m <model_name>", 0]]),
  terminalRow([["› 1. gpt-5.5 (current)", 0], ["Frontier model", 240]]),
  terminalRow([["  2. gpt-5.4", 0], ["Strong model", 240]]),
  terminalRow([]),
];
await app.syncSnapshotNow();
assert.deepEqual(sentMessages.pop(), {
  type: "snapshot",
  data: "/model\n/model choose what model and reasoning effort to use\nSelect Model and Effort\nAccess legacy models by running codex -m <model_name>\n› 1. gpt-5.5 (current)        Frontier model\n  2. gpt-5.4                  Strong model",
  source: "dom",
});

terminalDOMRows = [
  terminalRow([["Select Reasoning Level for gpt-5.5", 0]]),
  terminalRow([["1. Low                  Fast responses with lighter reasoning", 0]]),
  terminalRow([["2. Medium (default)     Balances speed and reasoning depth for everyday tasks", 0]]),
  terminalRow([["3. High                 Greater reasoning depth for complex problems", 0]]),
  terminalRow([["› 4. Extra high (current)  Extra high reasoning depth for complex problems", 0]]),
  terminalRow([["Press enter to confirm or esc to go back", 0]]),
];
await app.syncSnapshotNow();
assert.deepEqual(sentMessages.pop(), {
  type: "snapshot",
  data: "Select Reasoning Level for gpt-5.5\n1. Low                  Fast responses with lighter reasoning\n2. Medium (default)     Balances speed and reasoning depth for everyday tasks\n3. High                 Greater reasoning depth for complex problems\n› 4. Extra high (current)  Extra high reasoning depth for complex problems\nPress enter to confirm or esc to go back",
  source: "dom",
});
terminalDOMRows = [
  terminalRow([["alpha", 0], ["beta", 40.3]]),
  terminalRow([["left", 0], ["right", 80]]),
];
await app.syncSnapshotNow();
assert.deepEqual(sentMessages.pop(), {
  type: "snapshot",
  data: "alphabeta\nleft      right",
  source: "dom",
});
terminalDOMRows = [];

elements["help-open"].onclick();
assert.equal(elements["help-dialog"].open, true, "help dialog should open from topbar button");
helpTabs[1].onclick();
assert.ok(helpTabs[1].className.includes("active"), "clicked help tab should become active");
assert.ok(helpPanels[1].className.includes("active"), "target help panel should become active");
elements["help-close"].onclick();
assert.equal(elements["help-dialog"].open, false, "help dialog should close");

await Promise.resolve(elements["lark-register-start"].onclick());
await Promise.resolve();
assert.equal(elements["lark-register-panel"].hidden, false, "lark registration panel should show");
assert.equal(elements["lark-register-code"].textContent, "USER-1");
assert.equal(elements["lark-register-link"].href, "https://open.feishu.cn/page/cli?user_code=USER-1");
assert.ok(elements["lark-register-qr"].src.includes("/api/lark-app-registration/qr?text="));
assert.equal(elements["lark-app-console-link"].href, "https://open.feishu.cn/app/app-id/auth");
elements["lark-copy-scope"].onclick();
await Promise.resolve();
assert.equal(context.copiedText, "im:message.group_msg");
elements["lark-copy-permission-json"].onclick();
await Promise.resolve();
assert.ok(context.copiedText.includes("im:message.group_msg"));

elements["composer-input"].value = "line one";
let prevented = false;
elements["composer-input"].onkeydown({
  key: "Enter",
  metaKey: false,
  ctrlKey: false,
  preventDefault() {
    prevented = true;
  },
});
assert.equal(prevented, false, "plain Enter should keep textarea newline behavior");
assert.equal(sentMessages.length, 0, "plain Enter should not send");

elements["composer-input"].value = "echo command-enter";
elements["composer-input"].onkeydown({
  key: "Enter",
  metaKey: true,
  ctrlKey: false,
  preventDefault() {
    prevented = true;
  },
});
assert.deepEqual(sentMessages.pop(), { type: "submit", data: "echo command-enter" });

let pastePrevented = false;
await elements.terminal.dispatchEvent({
  type: "paste",
  clipboardData: {
    files: [],
    items: [{
      kind: "file",
      type: "image/png",
      getAsFile() {
        return { name: "paste.png", type: "image/png" };
      },
    }],
  },
  preventDefault() {
    pastePrevented = true;
  },
});
await Promise.resolve();
assert.equal(pastePrevented, true, "terminal image paste should prevent default paste handling");
assert.deepEqual(sentMessages.pop(), { type: "input", data: "/tmp/easy-terminal-test/paste.png " });

app.state.sessions = [{
  id: "sess-1",
  name: "A",
  status: "running",
  live: true,
  updated_at: new Date().toISOString(),
  notify_on_waiting: false,
  notifications_available: true,
}];
app.renderSessions();
const card = elements.sessions.children[0];
assert.ok(card.className.includes("session-running"), "running session card should have running class");
const notify = card.querySelector(".notify-input");
notify.checked = true;
await notify.onchange({ stopPropagation() {}, target: notify });
assert.ok(fetchCalls.some((call) => call.path === "/api/sessions/sess-1" && call.options.method === "PATCH" && call.options.body.includes('"notify_on_waiting":true')));

await app.loadConfig();
elements["cfg-fast-waiting"].value = "450";
elements["cfg-conservative-waiting"].value = "900";
elements["cfg-lark-max-lines"].value = "120";
elements["cfg-lark-app-id"].value = "new-app";
elements["cfg-lark-app-secret"].value = "new-secret";
elements["cfg-lark-receive-id"].value = "ou_new";
elements["cfg-lark-default-session-name"].value = "默认会话";
elements["cfg-lark-session-chat-prefix"].value = "DEV ·";
elements["cfg-lark-mention-enabled"].checked = false;
elements["cfg-prestart-command"].value = "source ~/.zshrc";
elements["drop-rule-add"].onclick();
let dropRuleRow = elements["drop-rule-list"].children.find((node) => node.className === "drop-rule-row");
dropRuleRow.children[0].value = "噪声";
dropRuleRow.children[0].oninput();
dropRuleRow.children[1].value = "noise";
dropRuleRow.children[1].oninput();
elements["drop-rule-add"].onclick();
dropRuleRow = elements["drop-rule-list"].children.filter((node) => node.className === "drop-rule-row")[1];
dropRuleRow.children[0].value = "调试";
dropRuleRow.children[0].oninput();
dropRuleRow.children[1].value = "debug";
dropRuleRow.children[1].oninput();
assert.deepEqual(JSON.parse(elements["cfg-drop-patterns"].value), [
  { title: "噪声", pattern: "noise" },
  { title: "调试", pattern: "debug" },
], "drop rule editor should write JSON");
elements["custom-shortcut-add"].onclick();
const shortcutRow = elements["custom-shortcut-list"].children.find((node) => node.className === "custom-shortcut-row");
shortcutRow.children[0].value = "状态";
shortcutRow.children[0].oninput();
shortcutRow.children[1].value = "git status";
shortcutRow.children[1].oninput();
assert.deepEqual(JSON.parse(elements["cfg-lark-custom-shortcuts"].value), [{ label: "状态", command: "git status" }], "custom shortcut editor should write JSON");
elements["cfg-session-name-presets"].value = JSON.stringify({ "会话 A": { commands: ["pwd"] } });
elements["cfg-session-start-presets"].value = JSON.stringify({ "1": { commands: ["codex"] } });
app.renderNamePresets();
assert.equal(elements["preset-list"].children.length, 1, "name preset list should mirror JSON");
elements["prestart-command-list"].children[0].querySelector(".preset-command-input").value = "source ~/.zshrc";
elements["prestart-command-list"].children[0].querySelector(".preset-command-input").oninput();
assert.equal(elements["cfg-prestart-command"].value, "source ~/.zshrc", "prestart row editor should sync textarea");
elements["preset-session-name"].value = "开发";
app.saveNamePresetFromForm();
let devPreset = elements["preset-list"].children.find((child) => child.children[0]?.children?.[0]?.textContent === "开发");
let devAddRow = devPreset.children.find((node) => node.className === "preset-command-add-row");
devAddRow.children[0].value = "cd project/dev";
devAddRow.children[1].onclick();
devPreset = elements["preset-list"].children.find((child) => child.children[0]?.children?.[0]?.textContent === "开发");
devAddRow = devPreset.children.find((node) => node.className === "preset-command-add-row");
devAddRow.children[0].value = "codex";
devAddRow.children[1].onclick();
let editedNamePresets = JSON.parse(elements["cfg-session-name-presets"].value);
assert.deepEqual(editedNamePresets["开发"], { commands: ["cd project/dev", "codex"] }, "visual preset editor should write JSON");
elements["startup-json-toggle"].onclick();
assert.equal(elements["startup-json-preview"].hidden, false, "json preview should open");
assert.ok(elements["startup-json-preview"].value.includes('"开发"'), "json preview should show current presets");
elements["startup-json-preview"].value = JSON.stringify({
  session_pre_start_command: "source ~/.zshrc\nexport A=1",
  session_name_presets: { "JSON会话": { commands: ["pwd"] } },
  session_start_presets: { "2": { commands: ["opencode"] } },
}, null, 2);
elements["startup-json-preview"].oninput();
assert.equal(elements["cfg-prestart-command"].value, "source ~/.zshrc\nexport A=1", "json editor should sync prestart command");
assert.deepEqual(JSON.parse(elements["cfg-session-name-presets"].value), { "JSON会话": { commands: ["pwd"] } }, "json editor should sync name presets");
assert.deepEqual(JSON.parse(elements["cfg-session-start-presets"].value), { "2": { commands: ["opencode"] } }, "json editor should sync start presets");
elements["startup-json-preview"].value = "{";
elements["startup-json-preview"].oninput();
await assert.rejects(() => app.saveConfig(), /启动配置 JSON 格式不正确/, "invalid startup json should block save");
elements["startup-json-preview"].value = JSON.stringify({
  session_pre_start_command: "source ~/.zshrc",
  session_name_presets: { "开发": { commands: ["cd project/dev", "codex"] } },
  session_start_presets: { "1": { commands: ["codex"] } },
}, null, 2);
elements["startup-json-preview"].oninput();
elements["cfg-agent-preset"].value = "codex";
elements["cfg-agent-preset"].onchange();
let generatedStartPresets = JSON.parse(elements["cfg-session-start-presets"].value);
assert.deepEqual(generatedStartPresets["999999"], { commands: ["codex --dangerously-bypass-approvals-and-sandbox"] }, "agent preset should update default start preset 999999");
elements["cfg-agent-preset"].value = "aiden";
elements["cfg-agent-preset"].onchange();
generatedStartPresets = JSON.parse(elements["cfg-session-start-presets"].value);
assert.deepEqual(generatedStartPresets["999999"], { commands: ["aiden"] }, "aiden preset should update default start preset 999999");
app.state.config = {
  ...app.state.config,
  session_start_presets: { "999999": { commands: ["codex --dangerously-bypass-approvals-and-sandbox"] } },
};
app.openConfigDialog("config-session");
assert.equal(elements["cfg-agent-preset"].value, "codex", "agent preset should be selected from saved preset 999999");
elements["startup-json-preview"].value = JSON.stringify({
  session_pre_start_command: "source ~/.zshrc",
  session_name_presets: { "开发": { commands: ["cd project/dev", "codex"] } },
  session_start_presets: { "1": { commands: ["codex"] } },
}, null, 2);
elements["startup-json-preview"].oninput();
elements["cfg-fast-waiting"].value = "450";
elements["cfg-conservative-waiting"].value = "";
elements["cfg-auto-refresh-interval"].value = "6000";
elements["cfg-lark-max-lines"].value = "";
elements["cfg-lark-app-id"].value = "new-app";
elements["cfg-lark-mention-enabled"].checked = false;
elements["cfg-lark-session-chat-prefix"].value = "DEV ·";
elements["cfg-drop-patterns"].value = JSON.stringify([
  { title: "噪声", pattern: "noise" },
  { title: "调试", pattern: "debug" },
]);
elements["cfg-lark-custom-shortcuts"].value = JSON.stringify([{ label: "状态", command: "git status" }]);
elements["cfg-lark-default-session-name"].value = "Claude 会话";
elements["cfg-agent-preset"].value = "claude";
elements["cfg-agent-preset"].onchange();
generatedStartPresets = JSON.parse(elements["cfg-session-start-presets"].value);
assert.deepEqual(generatedStartPresets["999999"], { commands: ["claude --dangerously-skip-permissions"] }, "claude preset should update default start preset 999999");
await app.testLarkConfig();
assert.ok(fetchCalls.some((call) => call.path === "/api/config/lark-test" && call.options.method === "POST"), "lark config test should POST /api/config/lark-test");
assert.equal(elements["lark-test-result"].children.length, 2, "lark test result should render steps");
await app.saveConfig();
const configPatch = fetchCalls.filter((call) => call.path === "/api/config" && call.options.method === "PATCH").at(-1);
assert.ok(configPatch, "config form should PATCH /api/config");
const patchedConfig = JSON.parse(configPatch.options.body);
assert.equal(patchedConfig.fast_waiting_transition_ms, 450);
assert.equal(patchedConfig.conservative_waiting_transition_ms, 700);
assert.equal(patchedConfig.lark_auto_refresh_interval_ms, 6000);
assert.equal(patchedConfig.lark_notify_max_lines, 300);
assert.equal(patchedConfig.lark_app_id, "new-app");
assert.equal(patchedConfig.lark_mention_enabled, false);
assert.equal(patchedConfig.lark_session_chat_prefix, "DEV ·");
assert.deepEqual(patchedConfig.lark_notify_drop_line_patterns, [
  { title: "噪声", pattern: "noise" },
  { title: "调试", pattern: "debug" },
]);
assert.deepEqual(patchedConfig.lark_custom_shortcuts, [{ label: "状态", command: "git status" }]);
assert.deepEqual(patchedConfig.session_start_presets, { "999999": { commands: ["claude --dangerously-skip-permissions"] }, "1": { commands: ["codex"] } });

console.log("frontend e2e ok");
