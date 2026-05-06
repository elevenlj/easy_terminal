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

`lark_notify_max_lines` in `conf/config.local.json` controls how many trailing lines are kept in long Feishu notifications.

## Build

```sh
make test
make test-browser
make build
```

`make test-browser` runs a real Chrome headless end-to-end test against an isolated test server. It covers session creation, WebSocket terminal input/output, notification toggle, quick command creation, Send button behavior, and Command+Enter behavior.
