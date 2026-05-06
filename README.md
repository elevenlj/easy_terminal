# easy_terminal

Web-based remote terminal manager built with Go, PTY, WebSocket, SQLite, xterm.js, Lark notifications, and an optional command agent.

## Run

```sh
cp conf/config.local.example.json conf/config.local.json
cp conf/command_agent.example.json conf/command_agent.json
make run
```

Open http://localhost:8080.

## Configuration

Environment variables override `conf/config.local.json`:

- `PORT`
- `TERMINAL_WORKING_DIR`
- `AGENT_MONITOR_DB`
- `AGENT_MONITOR_UPLOADS_DIR`
- `AGENT_MONITOR_LOG_DIR`
- `LARK_APP_ID`
- `LARK_APP_SECRET`
- `LARK_NOTIFY_RECEIVE_ID`
- `LARK_MENTION_ENABLED`
- `SESSION_PRE_START_COMMAND`

`lark_notify_max_lines` in `conf/config.local.json` controls how many trailing lines are kept in long Feishu notifications.
`codex_no_anchor_fallback_lines` controls how many trailing lines are sent when a Codex TUI snapshot cannot be anchored to the last input.
`session_pre_start_command` runs once inside every newly opened terminal session before user input is sent.
`session_start_presets` maps Feishu start suffix codes to startup commands. Preset codes are split only by `-`: `开始 测试 12` runs preset `12`, while `开始 测试 1-2` runs preset `1` then preset `2`, and `开始 测试 1-23-223` runs `1`, `23`, `223`. Template variables include shell-quoted `{{session_name}}`, `{{session_id}}`, `{{preset_codes}}`, `{{timestamp}}`, `{{session_name_slug}}`, plus raw variants ending in `_raw`.

## Build

```sh
make test
make test-browser
make build
```

`make test-browser` runs a real Chrome headless end-to-end test against an isolated test server. It covers session creation, WebSocket terminal input/output, notification toggle, quick command creation, Send button behavior, and Command+Enter behavior.
