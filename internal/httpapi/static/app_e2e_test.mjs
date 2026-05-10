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
    this.onclick = null;
    this.onchange = null;
    this.oninput = null;
    this.onkeydown = null;
    this.onsubmit = null;
    this.parent = null;
    this._bySelector = new Map();
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
        ".finish-btn",
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
  }

  get innerHTML() {
    return this._innerHTML || "";
  }

  querySelector(selector) {
    return this._bySelector.get(selector) || null;
  }

  appendChild(child) {
    child.parent = this;
    this.children.push(child);
    return child;
  }

  focus() {
    this.focused = true;
  }

  clear() {
    this.cleared = true;
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
}

const ids = [
  "sessions",
  "quick-list",
  "composer-input",
  "composer",
  "new-session",
  "session-name",
  "session-search",
  "show-ended",
  "quick-form",
  "quick-text",
  "quick-dialog",
  "quick-cancel",
  "config-open",
  "config-dialog",
  "config-form",
  "config-cancel",
  "config-error",
  "cfg-fast-waiting",
  "cfg-conservative-waiting",
  "cfg-lark-max-lines",
  "cfg-lark-app-id",
  "cfg-lark-app-secret",
  "cfg-lark-receive-id",
  "cfg-lark-default-session-name",
  "cfg-lark-mention-enabled",
  "cfg-prestart-command",
  "cfg-drop-patterns",
  "cfg-session-name-presets",
  "cfg-session-start-presets",
  "active-title",
  "terminal",
];
const elements = Object.fromEntries(ids.map((id) => [id, new FakeElement(id)]));

const fetchCalls = [];
const sentMessages = [];

class FakeWebSocket {
  static OPEN = 1;
}

const context = {
  console,
  setInterval() {},
  clearTimeout,
  setTimeout,
  TextDecoder,
  FormData: class {},
  WebSocket: FakeWebSocket,
  Terminal: class {
    loadAddon() {}
    open() {}
    onData() {}
    dispose() {}
    write() {}
    clear() {}
    get buffer() {
      return { active: { length: 0, getLine() { return null; } } };
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
    addEventListener() {},
  },
  window: {
    addEventListener() {},
    removeEventListener() {},
  },
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
        lark_notify_max_lines: 300,
        lark_app_id: "app-id",
        lark_app_secret: "secret",
        lark_notify_receive_id: "ou_1",
        lark_mention_enabled: true,
        lark_default_session_name: "临时",
        session_pre_start_command: "",
        lark_notify_drop_line_patterns: [],
        session_name_presets: {},
        session_start_presets: {},
      });
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

app.state.active = "sess-1";
app.state.socket = {
  readyState: FakeWebSocket.OPEN,
  send(payload) {
    sentMessages.push(JSON.parse(payload));
  },
};

elements["composer-input"].value = "echo button";
elements.composer.requestSubmit();
assert.deepEqual(sentMessages.pop(), { type: "input", data: "echo button\r" });
assert.equal(elements["composer-input"].value, "");

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
assert.deepEqual(sentMessages.pop(), { type: "input", data: "echo command-enter\r" });

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
elements["cfg-lark-default-session-name"].value = "临时";
elements["cfg-lark-mention-enabled"].checked = false;
elements["cfg-prestart-command"].value = "source ~/.zshrc";
elements["cfg-drop-patterns"].value = "noise\ndebug";
elements["cfg-session-name-presets"].value = JSON.stringify({ "会话 A": { commands: ["pwd"] } });
elements["cfg-session-start-presets"].value = JSON.stringify({ "1": { commands: ["codex"] } });
await app.saveConfig();
const configPatch = fetchCalls.find((call) => call.path === "/api/config" && call.options.method === "PATCH");
assert.ok(configPatch, "config form should PATCH /api/config");
const patchedConfig = JSON.parse(configPatch.options.body);
assert.equal(patchedConfig.fast_waiting_transition_ms, 450);
assert.equal(patchedConfig.lark_app_id, "new-app");
assert.equal(patchedConfig.lark_mention_enabled, false);
assert.deepEqual(patchedConfig.lark_notify_drop_line_patterns, ["noise", "debug"]);
assert.deepEqual(patchedConfig.session_start_presets, { "1": { commands: ["codex"] } });

console.log("frontend e2e ok");
