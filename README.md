# easy_terminal

`easy_terminal` 是一个基于 Go 的 Web 远程终端管理工具。它通过浏览器提供 PTY 终端、WebSocket 实时输入输出、SQLite 会话持久化、快速命令、图片粘贴上传、飞书等待通知和飞书消息回写，并支持可选的自然语言命令转换 agent。

## 功能概览

- Web 终端：使用 xterm.js 在浏览器中连接后端 PTY 会话。
- 多会话管理：创建、搜索、切换、结束、删除终端会话。
- 会话持久化：会话信息、历史输出和快速命令保存在 SQLite。
- 等待态通知：会话进入 `waiting` 状态后，可向飞书用户发送卡片通知。
- 飞书回复桥：可在飞书中创建会话、回复会话、发送附件、查询当前轮内容。
- 快速命令：在 Web 界面保存常用输入，一键填入发送框。
- 图片上传：在 Web 页面粘贴图片后，自动上传并把本地路径写入发送框。
- Command Agent：飞书消息以 `$` 开头时，可调用外部 CLI 把自然语言转换为 shell 命令。

## 运行环境

- Go 1.25 或兼容版本
- Node.js，用于运行浏览器端 E2E 测试
- Chrome、Chromium 或 Microsoft Edge，用于 headless 快照和 `make test-browser`
- macOS/Linux shell 环境；默认终端 shell 为 `/bin/zsh -i`

## 快速开始

```sh
cp conf/config.local.example.json conf/config.local.json
cp conf/command_agent.example.json conf/command_agent.json
make run
```

默认服务监听 `8080` 端口。浏览器打开：

```text
http://localhost:8080
```

首次运行会在项目根目录生成或使用以下运行时文件：

- `easy_terminal.db`：SQLite 数据库。
- `data/uploads/`：Web 粘贴图片和飞书附件保存目录。
- `log/easy_terminal.log`：服务日志。

## 常用命令

```sh
make run          # go run ./cmd
make build        # 构建 easy_terminal 二进制
make test         # 运行 Go 单元测试
make test-browser # 构建后运行浏览器 E2E 测试
make test-all     # 运行 Go 测试和浏览器 E2E 测试
make tidy         # go mod tidy
```

构建完成后也可以直接运行：

```sh
./easy_terminal
```

## 配置文件

主配置文件为 `conf/config.local.json`。建议从示例文件复制后再修改：

```sh
cp conf/config.local.example.json conf/config.local.json
```

示例结构：

```json
{
  "port": "8080",
  "lark_app_id": "",
  "lark_app_secret": "",
  "lark_notify_receive_id": "",
  "lark_mention_enabled": true,
  "fast_waiting_transition_ms": 300,
  "conservative_waiting_transition_ms": 700,
  "lark_notify_max_lines": 300,
  "codex_no_anchor_fallback_lines": 80,
  "session_pre_start_command": "",
  "session_start_presets": {}
}
```

字段说明：

- `port`：HTTP 服务监听端口，默认 `8080`。
- `lark_app_id`：飞书应用 App ID。
- `lark_app_secret`：飞书应用 App Secret。
- `lark_notify_receive_id`：飞书通知接收人的 `open_id`。
- `lark_mention_enabled`：通知卡片里是否 `@` 接收人。
- `fast_waiting_transition_ms`：检测到快速等待态时，延迟多少毫秒后确认进入 `waiting`。
- `conservative_waiting_transition_ms`：普通等待态确认延迟。
- `lark_notify_max_lines`：飞书长通知最多保留的尾部行数。
- `codex_no_anchor_fallback_lines`：无法用最后输入锚定 Codex TUI 快照时，回退发送的尾部行数。
- `session_pre_start_command`：每个新终端会话创建后自动执行的一条命令。
- `session_start_presets`：飞书创建会话时可按数字后缀触发的启动预设。

## 环境变量

环境变量会覆盖 `conf/config.local.json` 中的同名用途配置：

- `PORT`：覆盖监听端口。
- `TERMINAL_WORKING_DIR`：新终端默认工作目录；未设置时使用当前用户 home。
- `AGENT_MONITOR_DB`：SQLite 数据库路径，默认 `./easy_terminal.db`。
- `AGENT_MONITOR_UPLOADS_DIR`：上传目录，默认 `./data/uploads`。
- `AGENT_MONITOR_LOG_DIR`：日志目录，默认 `./log`。
- `LARK_APP_ID`：覆盖飞书应用 App ID。
- `LARK_APP_SECRET`：覆盖飞书应用 App Secret。
- `LARK_NOTIFY_RECEIVE_ID`：覆盖飞书通知接收人 `open_id`。
- `LARK_MENTION_ENABLED`：覆盖飞书通知是否 `@` 接收人，可用 `true` 或 `false`。
- `SESSION_PRE_START_COMMAND`：覆盖新会话预启动命令。
- `CHROME_BIN`：指定 headless Chrome/Chromium/Edge 可执行文件路径。

示例：

```sh
PORT=9090 TERMINAL_WORKING_DIR=/Users/eleven/project make run
```

## Web 界面使用

1. 在左侧输入会话名称，点击创建新会话。
2. 点击会话列表中的任一会话即可切换终端。
3. 终端区域支持直接键盘输入，输入会通过 WebSocket 写入后端 PTY。
4. 底部发送框可输入整段文本，点击 Send 或按 `Command+Enter` / `Ctrl+Enter` 发送并回车。
5. 会话卡片上的 `Finish` 会把在线会话标记为结束，`Delete` 会删除会话和对应上传目录。
6. 左侧搜索框可按会话名、ID、状态过滤；勾选显示已结束会话后可以查看历史会话。
7. 会话的“通知”开关控制该会话进入等待态时是否发送飞书通知。

已结束或失败的会话不能再连接 WebSocket，但仍可在页面中查看持久化输出。

## 快速命令

Web 页面底部的快速命令区域用于保存常用输入：

1. 点击 `+` 打开快速命令弹窗。
2. 输入命令或提示词并保存。
3. 点击快速命令 chip，会把文本填入发送框。
4. 点击 chip 上的关闭按钮会删除该快速命令。

快速命令存储在 SQLite 中，重启服务后仍会保留。

## 图片粘贴上传

在 Web 页面中选中任意会话后，可以直接粘贴剪贴板图片：

1. 浏览器检测到图片文件后会上传到 `/api/sessions/{id}/uploads`。
2. 服务端只接受 `image/*` MIME 类型，单次请求最大 10 MiB。
3. 上传成功后，图片的绝对路径会追加到发送框。
4. 用户仍需点击 Send 或按快捷键把路径发送进终端。

## 飞书通知

配置 `lark_app_id`、`lark_app_secret`、`lark_notify_receive_id` 后，飞书通知能力才可用。通知发送接口使用 `receive_id_type=open_id`，因此 `lark_notify_receive_id` 应填写接收用户的 `open_id`。

当会话满足以下条件时，会发送等待通知：

- 会话仍在线。
- 会话状态进入 `waiting`。
- 该会话开启了“通知”。
- 飞书应用配置完整且可用。

通知内容会优先使用浏览器终端可见快照，以便更准确地截取当前轮回复。若当前没有浏览器订阅，服务会尝试启动 headless 浏览器访问 `/?session={session_id}` 来同步快照。

## 飞书回复桥

只要配置了 `lark_app_id` 和 `lark_app_secret`，服务启动时会开启飞书 WebSocket 事件监听。飞书消息会按以下规则路由：

- `开始 会话名`、`新会话 会话名`、`/new 会话名`：创建新终端会话。
- 回复某条通知消息：输入会路由到对应会话。
- 文本中包含 `sess-数字`：输入会路由到指定会话。
- 无法解析目标会话时：自动创建名为 `lark-session` 的会话。
- `/c` 或 `／c`：不写入终端，直接回复当前轮内容。
- 以 `$` 开头：调用 Command Agent 将自然语言转换成 shell 命令后再写入终端。
- 使用 `|`、`｜`、`︱`、`￨` 分隔：按流水线方式分成多轮输入，后一段会在上一轮等待通知发出后继续执行。
- 需要输入字面量 `|` 时，用 `\|` 转义。

示例：

```text
开始 demo
开始 demo 1-2
sess-3 pwd
$ 查看当前目录最大的 10 个文件
pwd | ls -la | git status
/c
```

## 飞书启动预设

`session_start_presets` 可把飞书创建会话命令末尾的数字后缀映射成自动执行命令。

配置示例：

```json
{
  "session_start_presets": {
    "1": {
      "commands": [
        "cd project/{{session_name_slug}}",
        "codex"
      ]
    },
    "2": {
      "commands": [
        "mkdir -p project/{{session_name_slug}}",
        "cd project/{{session_name_slug}}",
        "codex"
      ]
    }
  }
}
```

飞书输入：

```text
开始 测试项目 1-2
```

执行逻辑：

- 创建名为 `测试项目` 的会话。
- 依次执行预设 `1` 和预设 `2` 中的命令。
- 预设码只按 `-` 分隔：`12` 表示预设 `12`，`1-2` 表示预设 `1` 后接预设 `2`。

可用模板变量：

- `{{session_name}}`：shell quote 后的会话名。
- `{{session_name_raw}}`：原始会话名。
- `{{session_name_slug}}`：适合路径使用的 slug，并经过 shell quote。
- `{{session_name_slug_raw}}`：未 quote 的 slug。
- `{{session_id}}` / `{{session_id_raw}}`：会话 ID。
- `{{preset_codes}}` / `{{preset_codes_raw}}`：飞书命令中的预设后缀。
- `{{timestamp}}` / `{{timestamp_raw}}`：当前 RFC3339 时间。

## Command Agent

Command Agent 配置文件为 `conf/command_agent.json`。从示例复制：

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
- `agent`：外部命令名或路径，例如 `gemini`。
- `prompt`：发送给 agent 的系统提示。

飞书文本以 `$` 开头时才会触发 Command Agent。例如：

```text
$ 找出当前目录 24 小时内修改过的 Go 文件
```

服务会把 `$` 后的文本交给 `agent`，读取标准输出作为最终 shell 命令。出于安全考虑，空输出或明显危险的命令会被拒绝，例如 `rm -rf /`、`mkfs`、`shutdown`、`reboot` 等。

## 附件处理

飞书回复桥支持接收图片和文件：

- 附件会下载到 `AGENT_MONITOR_UPLOADS_DIR/{session_id}/lark/`。
- 若只发送附件不带文本，路径会先写入终端输入行并等待下一条说明。
- 若附件和文本一起发送，会把附件路径加在文本前一起提交。
- 路径中包含空格或特殊字符时会自动 quote。

## HTTP API

主要 API：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/sessions` | 获取会话列表 |
| `POST` | `/api/sessions` | 创建会话，请求体：`{"name":"demo"}` |
| `PATCH` | `/api/sessions/{id}` | 开关等待通知，请求体：`{"notify_on_waiting":true}` |
| `DELETE` | `/api/sessions/{id}` | 删除会话和上传目录 |
| `POST` | `/api/sessions/{id}/finish` | 标记在线会话结束 |
| `GET` | `/api/sessions/{id}/output` | 获取会话输出历史 |
| `GET` | `/api/sessions/{id}/ws` | WebSocket 连接在线会话 |
| `POST` | `/api/sessions/{id}/uploads` | 上传图片，multipart 字段名为 `file` |
| `GET` | `/api/quick-commands` | 获取快速命令列表 |
| `POST` | `/api/quick-commands` | 创建快速命令 |
| `DELETE` | `/api/quick-commands/{id}` | 删除快速命令 |

WebSocket 客户端消息：

```json
{"type":"input","data":"pwd\r"}
{"type":"resize","cols":120,"rows":36}
{"type":"snapshot","data":"terminal visible text"}
```

服务端会在连接建立时发送当前输出快照。普通终端输出以二进制消息发送；需要浏览器同步可见内容时，会发送：

```json
{"type":"snapshot_request"}
```

## 数据和日志

默认路径：

- 数据库：`./easy_terminal.db`
- 上传目录：`./data/uploads`
- 日志文件：`./log/easy_terminal.log`

这些路径都可以通过环境变量覆盖。数据库使用 SQLite，服务启动时会把此前未结束的非终态会话标记为已退出，避免重启后出现不可连接的悬挂会话。

## 测试

运行单元测试：

```sh
make test
```

运行浏览器 E2E 测试：

```sh
make test-browser
```

`make test-browser` 会先构建二进制，再启动隔离测试服务，并使用真实 Chrome headless 验证会话创建、WebSocket 输入输出、通知开关、快速命令、Send 按钮和快捷键行为。

## 常见问题

### 页面提示通知不可用

检查 `lark_app_id`、`lark_app_secret`、`lark_notify_receive_id` 是否配置完整，并确认 `lark_notify_receive_id` 是用户 `open_id`。

### 飞书可以通知但回复没有进入会话

确认飞书应用已启用并订阅消息事件，且应用具备接收消息、读取消息资源、发送消息等权限。服务日志中会输出飞书 reply bridge 的启动和路由错误。

### 等待通知内容不完整

如果是 Codex 等 TUI 程序，建议保持对应 Web 会话打开。无人打开页面时服务会尝试启动 headless 浏览器，若本机找不到浏览器，请设置 `CHROME_BIN`。

### 新会话默认目录不对

设置 `TERMINAL_WORKING_DIR`，或在 `session_pre_start_command` / `session_start_presets` 中自动 `cd` 到目标目录。

### 端口冲突

通过配置文件或环境变量修改端口：

```sh
PORT=9090 make run
```
