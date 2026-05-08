# easy_terminal

`easy_terminal` 是一个 Go 实现的 Web 终端和飞书远程会话控制工具。它把本机 PTY 终端暴露到浏览器，同时通过飞书机器人创建会话、发送输入、接收等待通知、上传附件，并支持按会话名/后缀自动执行启动预设。

## 当前能力

### Web 终端

- 在浏览器中使用 xterm.js 操作后端 PTY 会话。
- 支持多会话创建、搜索、切换、结束和删除。
- 支持直接键盘输入，也支持底部发送框批量输入；`Command+Enter` / `Ctrl+Enter` 可直接发送。
- 支持粘贴图片上传，上传成功后把本地绝对路径追加到发送框。
- 支持快速命令，常用命令会持久化到 SQLite。
- 已结束会话可查看历史输出，在线会话通过 WebSocket 实时同步。

### 会话与持久化

- 每个会话有 `running`、`waiting`、`exited`、`failed` 状态。
- 会话元信息、历史输出和快速命令存储在 SQLite。
- 服务启动时会把上次未正常结束的非终态会话标记为已退出，避免重启后出现不可连接的悬挂会话。
- 默认运行时文件：
  - `easy_terminal.db`
  - `data/uploads/`
  - `log/easy_terminal.log`

### 飞书通知

- 当会话进入 `waiting` 状态且开启通知时，向配置的飞书用户发送卡片通知。
- 通知内容优先来自浏览器终端可见快照，用于更准确截取 Codex/TUI 当前轮回复。
- 如果没有浏览器页面在线，服务会启动 headless Chrome/Chromium/Edge 打开对应会话来同步可见快照。
- 同一轮任务不会不断新发多条主通知：
  - 当前轮第一次通知会创建一条飞书卡片。
  - 当前轮后续有新内容时，会更新同一条飞书卡片。
  - 卡片内会显示 `已更新-N`。
  - 每次更新后，会回复/引用原卡片发送一条极简提示卡片，只包含 `已更新-N`。
- 用户提交新一轮输入后，会重新创建新的飞书主通知。

### 飞书回复桥

服务配置飞书应用后，会通过飞书 WebSocket 事件接收用户消息：

- `开始 会话名`、`新会话 会话名`、`/new 会话名`：创建新终端会话。
- `开始`、`新会话`、`/new`：如果配置了 `lark_default_session_name`，使用该缺省名创建会话。
- 回复某条通知消息：输入会路由到对应会话。
- 消息中包含 `sess-数字`：输入会路由到指定会话。
- 无法解析目标会话时：自动创建 `lark-session`。
- `/c` 或 `／c`：不写入终端，直接回复当前轮内容。
- `$自然语言`：调用 Command Agent 转成 shell 命令后写入终端。
- `|`、`｜`、`︱`、`￨`：把一条飞书消息拆成多轮 pipeline，后一段会在上一轮通知后继续执行。
- `\|`：输入字面量竖线。

示例：

```text
开始 demo
开始
开始 demo 1-2
sess-3 pwd
$ 查看当前目录最大的 10 个文件
pwd | ls -la | git status
/c
```

### 飞书附件

- 支持飞书图片和文件附件。
- 附件会下载到 `AGENT_MONITOR_UPLOADS_DIR/{session_id}/lark/`。
- 只发附件不带文字：先把附件路径写入终端输入行，等待下一条说明。
- 附件和文字在同一条消息里：把路径和文字合成一条输入并立即回车执行。
- 支持飞书 `post` 富文本中的图片/文件，也支持 `image` / `file` 消息里的 `text`、`caption`。
- 如果新附件+文字到来时终端里还有旧的待说明附件，会先清空旧输入再提交新输入。

### 启动预设

飞书创建会话时支持两类启动预设：

- `session_name_presets`：按会话名精确匹配。
- `session_start_presets`：按创建命令末尾数字后缀匹配。

执行顺序：

1. 新会话创建。
2. `session_pre_start_command`，如果配置了。
3. 名称预设。
4. 数字后缀预设。
5. pipeline 后续输入。

预设执行期间不会刷屏推送多条飞书通知。预设写入完成后，会进入一个短暂安静期；安静期内如果还有输出会重新计时，稳定后只发送一次最终通知。

### Command Agent

飞书消息以 `$` 开头时，会把 `$` 后的自然语言发送给外部命令行 agent，由 agent 输出一条 shell 命令。服务会执行基本安全检查，拒绝空输出和明显危险命令，例如 `rm -rf /`、`mkfs`、`shutdown`、`reboot`。

## 快速开始

```sh
cp conf/config.local.example.json conf/config.local.json
cp conf/command_agent.example.json conf/command_agent.json
make run
```

默认监听：

```text
http://localhost:8080
```

常用命令：

```sh
make run          # go run ./cmd
make build        # 构建 easy_terminal 二进制
make test         # Go 单元测试
make test-browser # 浏览器 E2E 测试
make test-all     # Go 测试 + 浏览器 E2E
make tidy         # go mod tidy
```

运行环境：

- Go 1.25 或兼容版本
- Node.js，用于 `make test-browser`
- Chrome、Chromium 或 Microsoft Edge，用于 headless 快照和浏览器测试
- macOS/Linux shell 环境；默认 shell 为 `/bin/zsh -i`

## 配置

主配置文件是 `conf/config.local.json`，建议从示例复制：

```sh
cp conf/config.local.example.json conf/config.local.json
```

示例：

```json
{
  "port": "8080",
  "lark_app_id": "xxxx",
  "lark_app_secret": "xxxx",
  "lark_notify_receive_id": "xxxx",
  "lark_mention_enabled": true,
  "lark_default_session_name": "临时",
  "fast_waiting_transition_ms": 1000,
  "conservative_waiting_transition_ms": 3000,
  "lark_notify_max_lines": 100,
  "codex_no_anchor_fallback_lines": 80,
  "session_name_presets": {
    "会话 A": {
      "commands": [
        "mkdir -p project/{{session_name_slug_raw}}",
        "cd project/{{session_name_slug_raw}}",
        "codex --dangerously-bypass-approvals-and-sandbox"
      ]
    }
  },
  "session_start_presets": {
    "1": {
      "commands": [
        "cd project/{{session_name}}",
        "codex --dangerously-bypass-approvals-and-sandbox"
      ]
    },
    "2": {
      "commands": [
        "mkdir -p project/{{session_name}}",
        "cd project/{{session_name}}",
        "codex --dangerously-bypass-approvals-and-sandbox"
      ]
    }
  }
}
```

字段说明：

| 字段 | 说明 |
| --- | --- |
| `port` | HTTP 监听端口 |
| `lark_app_id` | 飞书应用 App ID |
| `lark_app_secret` | 飞书应用 App Secret |
| `lark_notify_receive_id` | 飞书通知接收用户的 `open_id` |
| `lark_mention_enabled` | 主通知卡片是否 `@` 接收人 |
| `lark_default_session_name` | 飞书开始命令未指定名称时使用的会话名 |
| `fast_waiting_transition_ms` | 普通输出稳定后进入 waiting 的延迟 |
| `conservative_waiting_transition_ms` | Codex/TUI 等更保守场景的 waiting 延迟 |
| `lark_notify_max_lines` | 飞书通知最多保留尾部行数 |
| `codex_no_anchor_fallback_lines` | Codex TUI 无法锚定输入时的尾部回退行数 |
| `session_pre_start_command` | 每个新终端会话创建后自动执行的一条命令 |
| `session_name_presets` | 按会话名精确匹配的启动预设 |
| `session_start_presets` | 按飞书开始命令数字后缀匹配的启动预设 |

环境变量覆盖：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | 配置文件或 `8080` | HTTP 端口 |
| `TERMINAL_WORKING_DIR` | 用户 home | 新终端默认工作目录 |
| `AGENT_MONITOR_DB` | `./easy_terminal.db` | SQLite 数据库路径 |
| `AGENT_MONITOR_UPLOADS_DIR` | `./data/uploads` | 上传目录 |
| `AGENT_MONITOR_LOG_DIR` | `./log` | 日志目录 |
| `LARK_APP_ID` | 配置文件 | 飞书 App ID |
| `LARK_APP_SECRET` | 配置文件 | 飞书 App Secret |
| `LARK_NOTIFY_RECEIVE_ID` | 配置文件 | 飞书接收用户 open_id |
| `LARK_MENTION_ENABLED` | 配置文件 | 是否 @ 接收人 |
| `LARK_DEFAULT_SESSION_NAME` | 配置文件 | 飞书 `开始` 命令的缺省会话名 |
| `SESSION_PRE_START_COMMAND` | 配置文件 | 新会话预启动命令 |
| `CHROME_BIN` | 自动查找 | headless 浏览器路径 |

## 启动预设模板变量

预设命令支持以下变量：

- `{{session_name}}`：shell quote 后的会话名。
- `{{session_name_raw}}`：原始会话名。
- `{{session_name_slug}}`：适合路径使用的 slug，并经过 shell quote。
- `{{session_name_slug_raw}}`：未 quote 的 slug。
- `{{session_id}}` / `{{session_id_raw}}`：会话 ID。
- `{{preset_codes}}` / `{{preset_codes_raw}}`：飞书命令中的数字后缀。
- `{{timestamp}}` / `{{timestamp_raw}}`：当前 RFC3339 时间。

飞书输入：

```text
开始 会话 A
开始 测试项目 1-2
```

匹配规则：

- `开始 会话 A` 精确匹配 `session_name_presets["会话 A"]`。
- `开始 测试项目 1-2` 创建名为 `测试项目` 的会话，并依次运行预设 `1`、`2`。
- `12` 是预设 `12`，不是 `1` 和 `2`；只有 `1-2` 才会拆成两个预设。

## Command Agent 配置

配置文件是 `conf/command_agent.json`：

```sh
cp conf/command_agent.example.json conf/command_agent.json
```

示例：

```json
{
  "enabled": true,
  "agent": "gemini",
  "prompt": "你是一个命令行助手。将用户的自然语言请求转换为一条可以直接在终端执行的 shell 命令。"
}
```

字段说明：

- `enabled`：是否启用。
- `agent`：外部命令名或路径。
- `prompt`：发送给 agent 的提示词。

## HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/sessions` | 获取会话列表 |
| `POST` | `/api/sessions` | 创建会话，请求体：`{"name":"demo"}` |
| `PATCH` | `/api/sessions/{id}` | 开关等待通知，请求体：`{"notify_on_waiting":true}` |
| `DELETE` | `/api/sessions/{id}` | 删除会话和上传目录 |
| `POST` | `/api/sessions/{id}/finish` | 标记在线会话结束 |
| `GET` | `/api/sessions/{id}/output` | 获取会话输出历史 |
| `GET` | `/api/sessions/{id}/ws` | WebSocket 连接在线会话 |
| `POST` | `/api/sessions/{id}/uploads` | 上传图片，multipart 字段名 `file` |
| `GET` | `/api/quick-commands` | 获取快速命令 |
| `POST` | `/api/quick-commands` | 创建快速命令 |
| `DELETE` | `/api/quick-commands/{id}` | 删除快速命令 |

WebSocket 客户端消息：

```json
{"type":"input","data":"pwd\r"}
{"type":"resize","cols":120,"rows":36}
{"type":"snapshot","data":"terminal visible text"}
```

服务端会发送：

- 二进制终端输出。
- `{"type":"snapshot_request"}`：请求浏览器回传当前可见快照。

## 运行数据

`.gitignore` 已忽略运行时文件：

- `easy_terminal`
- `*.db`
- `*.db-wal`
- `*.db-shm`
- `data/`
- `log/`
- `conf/config.local.json`
- `conf/command_agent.json`

## 常见问题

### 页面提示通知不可用

检查 `lark_app_id`、`lark_app_secret`、`lark_notify_receive_id` 是否完整。`lark_notify_receive_id` 需要是接收用户的 `open_id`。

### 飞书能收到通知，但回复没有进入会话

确认飞书应用启用了机器人能力、消息事件订阅和必要权限。服务日志会输出 reply bridge 的启动和路由错误。

### 通知内容不完整

Codex/TUI 程序依赖浏览器可见快照。保持 Web 会话打开最稳定；没有打开页面时，服务会尝试启动 headless 浏览器。若找不到浏览器，设置 `CHROME_BIN`。

### headless 快照出现终端噪声

服务会过滤浏览器/xterm 自动回写的终端能力响应，避免污染 TUI。若仍出现异常，优先确认本地 Chrome 版本和 `CHROME_BIN` 指向。

### 新会话默认目录不对

设置 `TERMINAL_WORKING_DIR`，或者通过 `session_pre_start_command`、`session_name_presets`、`session_start_presets` 自动 `cd`。

### 端口冲突

```sh
PORT=9090 make run
```
