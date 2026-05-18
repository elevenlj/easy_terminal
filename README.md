# easy_terminal

`easy_terminal` 是一个本机 Web 终端与飞书远程会话控制工具。它不是去接管某一个固定的云端 Agent，而是让你通过浏览器和飞书控制本机/服务器上的真实终端；只要某个 Agent、脚本或命令能在终端里运行，就可以被远程启动、查看和接管。

## 核心竞争力

1. **低成本**：Agent 在本机或自己的服务器上执行，可以使用免费的 CLI Agent、本地模型或已有工具链。
2. **控制终端**：控制终端就等同于控制整个 PC 或云服务器里可用的 Git、测试、脚本、服务、文件和各种 Agent。
3. **控制本地 Agent**：一键选择本地 Agent，启动会话时直接启用；不限于 Claude Code、Codex、Gemini，也可以是公司自研的 Agent CLI，有助于复用本地环境并保障数据安全。
4. **多会话管理**：一个会话一个群聊，可在不同群聊中处理不同任务，也可以多人在同一个群聊中操控同一个 Agent 干活。
5. **协作处理问题**：例如需求进入测试阶段后，QA 可以直接把 bug 和复现信息发到群聊里，本地 Agent 收到群聊消息后在你的本机环境里继续定位和处理问题。
6. **实时查看进展**：飞书卡片会同步终端状态，支持刷新、快捷键、中断、退出 Agent 和继续输入；任务完成后也可以发送消息通知。
7. **Web 终端与飞书共通**：Web 端嵌入真实终端，配置文本输入框解决终端输入困难问题；支持添加快捷键，飞书和终端双向控制并共享会话。
8. **轻量级飞书配置**：支持通过扫码完成飞书基础配置，降低自建机器人接入成本。
9. **飞书快捷键**：在飞书上发送消息后，会回复带快捷键的消息卡片，支持 Esc、Enter、Ctrl-C 等系统快捷键，也可以自定义快捷命令。
10. **指令预设**：支持会话名预设和会话启动命令，适合固定目录、固定项目、固定 Agent 的高频工作流。

## 指令预设

1. **会话名预设**：如果开启了一个名为 `xx` 的会话，可以为它预设命令，启动前自动执行。例如先进入指定目录，再启动 Claude Code、Codex、Gemini 或其他 Agent。
2. **会话启动命令**：可以指定任何会话启动前要执行的命令。例如希望使用 `zsh`，就可以直接配置对应启动命令。

## 典型场景

- 用手机在飞书里启动和继续 Codex、OpenCode、Claude Code、Gemini、Aiden 等 CLI Agent。
- 在一台电脑或服务器上同时管理多个终端会话，例如跑服务、看日志、执行测试、处理代码任务。
- 让 QA、开发、产品在同一个飞书群里补充上下文，共同推进同一个本地 Agent 任务。
- 复用本机环境、已有命令、已有账号和本地模型，不把所有任务都改造成云端 API 调用。

## 启动

开源地址：

```text
https://gitee.com/eleven_lj/easy_terminal.git
```

快速安装：

```sh
npm install -g @lijuneleven/easy-terminal
easy-terminal
```

查看版本：

```sh
easy-terminal --version
easy-terminal -v
easy-terminal -version
```

开发启动：

```sh
make run
```

默认监听本机 `8080` 端口。需要换端口时：

```sh
PORT=9090 make run
go run ./cmd --port 9090
go run ./cmd -p 9090
```

启动时会自动生成本机配置文件 `~/.easy_terminal/conf/config.local.json`。需要固定配置和运行数据目录时：

```sh
easy_terminal --config-dir /data/easy_terminal
EASY_TERMINAL_CONFIG_DIR=/data/easy_terminal easy_terminal
```

指定后，配置文件、会话数据库、上传文件和日志都会写入该目录。

构建二进制：

```sh
make build
./easy_terminal
./easy_terminal --port 9090
./easy_terminal -p 9090
./easy_terminal --config-dir /data/easy_terminal
```

端口配置优先级为：启动参数 `--port` / `-p` > 环境变量 `PORT` > 配置文件 > 默认端口 `8080`。

## 必要配置

1. 选择本地 Agent。可以直接选择，也可以通过快捷键或会话预设启动，建议按自己的工作流配置。
2. 配置飞书。Web 端支持扫码配置，完成后即可在飞书中联动终端会话。

## 查看内置帮助

启动服务后，在浏览器打开服务页面，点击右上角“帮助”查看内置帮助中心。

也可以直接查看仓库文档：

- [架构说明](docs/ARCHITECTURE.md)：代码模块和主要实现边界。

## 常用命令

```sh
make run          # go run ./cmd
make build        # 构建 easy_terminal 二进制
make test         # Go 单元测试
make test-browser # 浏览器 E2E 测试
make test-all     # Go 测试 + 浏览器 E2E
make tidy         # go mod tidy
```

## 运行环境

必需环境：

- Go 1.25 或兼容版本
- macOS 或 Linux shell 环境
- 可交互 shell；默认使用 `/bin/zsh -i`

可选环境：

- Chrome、Chromium 或 Microsoft Edge：可选，用于浏览器端查看和辅助快照同步

飞书联动需要：

- 一个飞书自建应用
- 应用的 `app_id` 和 `app_secret`
- 已启用机器人能力或消息发送能力
- 飞书应用信息、基础消息权限、群聊消息事件和卡片回调可在 Web 端通过扫码配置；群里不 @ 机器人也要响应时，还需要在飞书后台开通“获取群组中所有消息（敏感权限）”

本地 Agent 需要：

- 先在当前机器安装对应 CLI 工具，例如 `opencode`、`codex`、`claude`、`gemini`、`aiden` 等
- 确保这些命令可以在默认 shell 中直接执行
- 如果 Agent 依赖 API Key 或本地模型，请提前在本机环境变量或对应工具配置中完成设置

## 运行数据

默认运行时文件会写在用户目录下：

- `~/.easy_terminal/easy_terminal.db`
- `~/.easy_terminal/data/uploads/`
- `~/.easy_terminal/log/easy_terminal.log`
- `~/.easy_terminal/conf/config.local.json`

使用 `--config-dir` 或 `EASY_TERMINAL_CONFIG_DIR` 时，运行数据会写在指定目录下。

这些文件已在 `.gitignore` 中忽略。
