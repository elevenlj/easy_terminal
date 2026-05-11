# easy_terminal

`easy_terminal` 是一个本机 Web 终端与飞书远程会话控制工具。产品功能说明不在 README 展开维护，请在启动后进入网页右上角“帮助”，或直接阅读 [帮助文档](docs/HELP.md)。

## 启动

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

首次保存 Web 配置时会生成本机配置文件 `conf/config.local.json`。

构建二进制：

```sh
make build
./easy_terminal
./easy_terminal --port 9090
./easy_terminal -p 9090
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

- Chrome、Chromium 或 Microsoft Edge：用于生成终端快照

飞书联动需要：

- 一个飞书自建应用
- 应用的 `app_id` 和 `app_secret`
- 已启用机器人能力或消息发送能力
- 飞书应用信息、消息权限和事件/长连接配置可在 Web 端通过扫码一键配置

本地 Agent 需要：

- 先在当前机器安装对应 CLI 工具，例如 `opencode`、`codex`、`claude`、`gemini` 等
- 确保这些命令可以在默认 shell 中直接执行
- 如果 Agent 依赖 API Key 或本地模型，请提前在本机环境变量或对应工具配置中完成设置

## 运行数据

默认运行时文件会写在项目目录下：

- `easy_terminal.db`
- `data/uploads/`
- `log/easy_terminal.log`
- `conf/config.local.json`

这些文件已在 `.gitignore` 中忽略。
