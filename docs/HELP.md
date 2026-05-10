# easy_terminal 帮助文档

本文档是 easy_terminal 的主要使用说明。README 只保留启动入口；功能、配置和排障说明集中维护在这里。

## 快速开始

1. 复制配置文件：

   ```sh
   cp conf/config.local.example.json conf/config.local.json
   ```

2. 启动服务：

   ```sh
   make run
   ```

3. 打开 Web 页面，首次进入时会看到新手引导。点击“去配置”进入配置页面，优先完成飞书、等待通知和启动预设。

4. 创建会话后即可在网页终端里输入命令；配置飞书后，也可以从飞书创建和继续会话。

## 首次配置引导

首次进入 Web 端时，系统会弹出新手引导，提示先进入配置页面。这个提示只在当前浏览器出现一次：

- 点击“去配置”：关闭引导并打开配置页面。
- 点击“稍后”：关闭引导，之后不再自动弹出。
- 配置入口始终在左下角齿轮按钮。

建议首次配置顺序：

1. 在“飞书”页签中填写或扫码生成 App ID、App Secret、通知接收 ID。
2. 点击“测试飞书配置”，确认可以发送测试通知。
3. 在“等待通知”页签里调整通知最大行数、过滤正则和自定义快捷键。
4. 在“启动预设”页签里选择常用 Agent，或手动维护会话启动命令。
5. 点击 Save 保存到 `conf/config.local.json`。

## 网页终端

- 左侧是当前活跃会话列表，可以创建、搜索、切换和删除会话。
- 黑色区域是 xterm 终端，支持常规键盘输入、复制粘贴、方向键和 TUI 程序操作。
- 下方输入框适合发送多行文本。点击 Send 或按 `Command+Enter` / `Ctrl+Enter` 会提交并自动回车。
- 快速命令会持久化到 SQLite，点击命令片段可填入输入框，点击 `+` 可新增。
- 页面会把终端可见快照同步给后端，用于更准确地生成飞书等待通知。

## 飞书联动

配置飞书应用后，服务会通过飞书 WebSocket 事件接收消息，并把消息路由到对应终端会话。

常用输入：

```text
开始 demo
开始
开始 demo 1-2
sess-3 pwd
pwd | ls -la | git status
/c
```

规则：

- `开始 会话名`、`新会话 会话名`、`/new 会话名`：创建新终端会话。
- `开始`、`新会话`、`/new`：使用 `lark_default_session_name` 作为默认会话名。
- 绑定到终端会话的飞书群聊里，普通消息默认进入该会话。
- 回复某条通知消息时，输入会路由到对应会话。
- 消息中包含 `sess-数字` 时，输入会路由到指定会话。
- 无法解析目标会话时，会自动创建 `lark-session`。
- `/c` 或 `／c` 不写入终端，直接回复当前轮内容。
- `|`、`｜`、`︱`、`￨` 会把一条飞书消息拆成多轮 pipeline；后一段会在上一轮通知后继续执行。
- `\|` 表示字面量竖线。

## 飞书会话群聊

在主机器人聊天里发送 `开始 会话名` 时，服务会尝试创建一个独立飞书群聊并绑定到该终端会话。群名由 `lark_session_chat_prefix + 会话名` 组成，例如 `ET · demo`。

- 独立群聊创建成功后，这个群聊就是该会话的手机端入口。
- 多个终端会话会自然出现在飞书聊天列表里，可用飞书原生未读、置顶、搜索和最近消息管理。
- 创建群聊失败时，服务会退回到单机器人聊天模式，仍可通过回复通知或 `sess-数字` 路由。

## 等待通知

会话开启“通知”后，每次输入会把状态切回 `running`；终端输出稳定一段时间后进入 `waiting`，系统会准备飞书等待通知。

推送位置：

- 如果会话绑定了独立群聊，通知发到该群聊。
- 否则通知发到 `lark_notify_receive_id` 对应用户。

通知内容：

- 优先使用浏览器中 xterm 的可见快照。
- 没有浏览器页面在线时，服务会尝试启动 headless Chrome、Chromium 或 Edge 打开对应会话获取快照。
- 每次提交输入时，会记录输入文本和输入前的可见快照；推送时优先从本次输入回显之后截取当前轮回复。
- 找不到输入锚点时，使用本轮原始输出兜底。
- 推送前会清理终端控制字符、重复行、Codex/TUI 临时状态行，并应用 `lark_notify_drop_line_patterns`。
- 正文最多保留 `lark_notify_max_lines` 行，同时有总字符保护；被截断时会带 `[truncated]` 前缀。

更新规则：

- 当前轮第一次通知会创建一张主卡片。
- 当前轮后续有新内容时，会更新同一张主卡片。
- 卡片内显示 `已更新-N`。
- 每次内容更新后，会回复原卡片发送一条极简提示卡片，只包含 `已更新-N`。
- 卡片快捷键和“刷新消息”按钮会继续锚定当前卡片。
- 用户提交新一轮输入后，会重新创建新的飞书主通知。

## 飞书卡片快捷键

系统快捷键固定显示在飞书通知卡片第一行：

- `Ctrl-C`
- `Esc`
- `Enter`
- `刷新消息`

自定义快捷键通过 `lark_custom_shortcuts` 配置，显示在系统快捷键下一行。点击后会把对应指令提交到当前会话。

## 飞书附件

- 支持飞书图片和文件附件。
- 附件会下载到 `AGENT_MONITOR_UPLOADS_DIR/{session_id}/lark/`。
- 只发附件不带文字时，服务会先把附件路径写入终端输入行，等待下一条说明。
- 附件和文字在同一条消息里时，服务会把路径和文字合成一条输入并立即回车执行。
- 支持飞书 `post` 富文本中的图片/文件，也支持 `image` / `file` 消息里的 `text`、`caption`。
- 如果新附件加文字到来时终端里还有旧的待说明附件，会先清空旧输入再提交新输入。

## 网页图片粘贴

- 在终端区域粘贴图片，会上传到当前会话目录，并把图片路径写入终端。
- 在下方输入框粘贴图片，会把路径插入输入框。
- 上传路径可给本机命令或终端程序读取，例如在路径后补充“请分析这张图”。
- 删除会话时，会同步删除该会话的上传目录。

## 启动预设

飞书创建会话时支持两类启动预设：

- `session_name_presets`：按会话名精确匹配。
- `session_start_presets`：按会话名后面的数字匹配，例如 `开始 demo 1`。

执行顺序：

1. 新会话创建。
2. `session_pre_start_command`，如果配置了。
3. 名称预设。
4. 会话名后的数字预设。
5. pipeline 后续输入。

预设执行期间不会刷屏推送多条飞书通知。预设写入完成后，会进入短暂安静期；安静期内如果还有输出会重新计时，稳定后只发送一次最终通知。

### 模板变量

预设命令支持以下变量：

- `{{session_name}}`：shell quote 后的会话名。
- `{{session_name_raw}}`：原始会话名。
- `{{session_name_slug}}`：适合路径使用的 slug，并经过 shell quote。
- `{{session_name_slug_raw}}`：未 quote 的 slug。
- `{{session_id}}` / `{{session_id_raw}}`：会话 ID。
- `{{preset_codes}}` / `{{preset_codes_raw}}`：飞书开始命令中会话名后面的数字。
- `{{timestamp}}` / `{{timestamp_raw}}`：当前 RFC3339 时间。

匹配示例：

- `开始 会话 A` 精确匹配 `session_name_presets["会话 A"]`。
- `开始 测试项目 1-2` 创建名为 `测试项目` 的会话，并依次运行预设 `1`、`2`。
- `12` 是预设 `12`，不是 `1` 和 `2`；只有 `1-2` 才会拆成两个预设。

## 配置文件

主配置文件是 `conf/config.local.json`，建议从示例复制：

```sh
cp conf/config.local.example.json conf/config.local.json
```

常用字段：

| 字段 | 说明 |
| --- | --- |
| `port` | HTTP 监听端口 |
| `lark_app_id` | 飞书应用 App ID |
| `lark_app_secret` | 飞书应用 App Secret |
| `lark_notify_receive_id` | 飞书通知接收用户的 `open_id` |
| `lark_mention_enabled` | 主通知卡片是否 `@` 接收人 |
| `lark_default_session_name` | 飞书开始命令未指定名称时使用的会话名 |
| `lark_session_chat_prefix` | 飞书独立群聊名称前缀，默认 `ET · ` |
| `fast_waiting_transition_ms` | 普通输出稳定后进入 waiting 的延迟 |
| `conservative_waiting_transition_ms` | Codex/TUI 等更保守场景的 waiting 延迟 |
| `lark_notify_max_lines` | 飞书通知最多保留尾部行数 |
| `lark_notify_drop_line_patterns` | 飞书通知按行删除规则；每项包含 `title` 和 `pattern` |
| `lark_custom_shortcuts` | 飞书通知卡片上的自定义快捷键 |
| `session_pre_start_command` | 每个新终端会话创建后自动执行的命令 |
| `session_name_presets` | 按会话名精确匹配的启动预设 |
| `session_start_presets` | 按飞书开始命令中会话名后面的数字匹配的启动预设 |

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
| `LARK_SESSION_CHAT_PREFIX` | 配置文件或 `ET · ` | 飞书独立群聊名称前缀 |
| `SESSION_PRE_START_COMMAND` | 配置文件 | 新会话预启动命令 |
| `CHROME_BIN` | 自动查找 | headless 浏览器路径 |

## HTTP API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/sessions` | 获取会话列表 |
| `POST` | `/api/sessions` | 创建会话，请求体：`{"name":"demo"}` |
| `PATCH` | `/api/sessions/{id}` | 开关等待通知，请求体：`{"notify_on_waiting":true}` |
| `DELETE` | `/api/sessions/{id}` | 删除会话和上传目录 |
| `GET` | `/api/sessions/{id}/output` | 获取在线会话当前输出 |
| `GET` | `/api/sessions/{id}/ws` | WebSocket 连接在线会话 |
| `POST` | `/api/sessions/{id}/uploads` | 上传图片，multipart 字段名 `file` |
| `GET` | `/api/quick-commands` | 获取快速命令 |
| `POST` | `/api/quick-commands` | 创建快速命令 |
| `DELETE` | `/api/quick-commands/{id}` | 删除快速命令 |
| `GET` | `/api/config` | 获取运行时配置 |
| `PATCH` | `/api/config` | 保存运行时配置到本机配置文件 |
| `POST` | `/api/config/lark-test` | 发送飞书配置测试通知 |
| `POST` | `/api/lark-app-registration` | 开始扫码创建飞书应用 |
| `POST` | `/api/lark-app-registration/poll` | 轮询扫码创建结果 |
| `GET` | `/api/lark-app-registration/qr` | 根据确认链接生成二维码 |

WebSocket 客户端消息：

```json
{"type":"input","data":"pwd\r"}
{"type":"submit","data":"多行输入"}
{"type":"resize","cols":120,"rows":36}
{"type":"snapshot","data":"terminal visible text"}
```

服务端会发送：

- 二进制终端输出。
- `{"type":"snapshot_request"}`：请求浏览器回传当前可见快照。

## 运行数据

默认运行时文件：

- `easy_terminal.db`
- `data/uploads/`
- `log/easy_terminal.log`
- `conf/config.local.json`

`easy_terminal.db-wal` 和 `easy_terminal.db-shm` 是 SQLite WAL 模式的运行时辅助文件。`-wal` 保存追加写入日志，`-shm` 保存共享索引和锁信息；服务运行时看到它们是正常现象。

## 常见问题

### 页面提示通知不可用

检查 `lark_app_id`、`lark_app_secret`、`lark_notify_receive_id` 是否完整。`lark_notify_receive_id` 需要是接收用户的 `open_id`。

### 飞书能收到通知，但回复没有进入会话

确认飞书应用启用了机器人能力、消息事件订阅和必要权限。服务日志会输出 reply bridge 的启动和路由错误。

### 飞书没有创建独立会话群聊

确认飞书应用已开通创建群、获取/接收群消息、发送群消息等 IM 权限，并且应用机器人可被拉入群聊。创建失败时服务会在日志里记录 `failed to create session chat`，同时回退到单机器人聊天模式。

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
