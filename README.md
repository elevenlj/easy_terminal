# easy_terminal

`easy_terminal` 是一个本机 Web 终端与飞书远程会话控制工具。它不是去接管某一个固定的云端 Agent，而是让你通过浏览器和飞书控制本机/服务器上的真实终端；只要某个 Agent、脚本或命令能在终端里运行，就可以被远程启动、查看和接管。

适合这些场景：

- 用手机在飞书里启动和继续 Codex、OpenCode、Claude Code、Gemini、Aiden 等 CLI Agent。
- 在一台电脑或服务器上同时管理多个终端会话，例如跑服务、看日志、执行测试、处理代码任务。
- 不想把所有任务都变成云端 API 调用，希望复用本机环境、已有命令、已有账号和本地模型。
- 需要实时看到 Agent 输出，中途补充指令，而不是等任务结束后才收到一条通知。

核心优势：

- 成本低：Agent 在本机或自己的服务器上执行，可以使用免费的 CLI Agent、本地模型或已有工具链。
- 控制强：控制终端就等于控制终端里可用的所有能力，包括 Git、测试、脚本、服务、文件和各种 Agent。
- 更灵活：支持多会话并行，每个会话可以绑定独立飞书群聊，也可以配置会话名预设和启动预设。
- 更实时：飞书卡片会同步终端状态，支持刷新、快捷键、中断、退出 Agent 和继续输入。
- 更轻量：Web 端负责配置和查看，飞书端负责手机操作，不要求公网回调地址；飞书基础应用能力可通过扫码配置。

完整功能说明不在 README 展开维护，请在启动后进入网页右上角“帮助”，或直接阅读 [帮助文档](docs/HELP.md)。

## 启动

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

启动时会自动生成本机配置文件 `~/.easy_terminal/conf/config.local.json`。需要固定配置文件目录时：

```sh
easy_terminal --config-dir /data/easy_terminal/conf
EASY_TERMINAL_CONFIG_DIR=/data/easy_terminal/conf easy_terminal
```

构建二进制：

```sh
make build
./easy_terminal
./easy_terminal --port 9090
./easy_terminal -p 9090
./easy_terminal --config-dir /data/easy_terminal/conf
```

端口配置优先级为：启动参数 `--port` / `-p` > 环境变量 `PORT` > 配置文件 > 默认端口 `8080`。

## 查看帮助文档

启动服务后，在浏览器打开服务页面，点击右上角“帮助”查看内置帮助中心。

也可以直接查看仓库文档：

- [帮助文档](docs/HELP.md)：使用流程、飞书联动、通知机制、配置、文件处理和常见问题。
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

这些文件已在 `.gitignore` 中忽略。
