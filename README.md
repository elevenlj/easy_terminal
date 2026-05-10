# easy_terminal

`easy_terminal` 是一个本机 Web 终端与飞书远程会话控制工具。产品功能说明不在 README 展开维护，请在启动后进入网页右上角“帮助”，或直接阅读 [帮助文档](docs/HELP.md)。

## 启动

准备配置文件：

```sh
cp conf/config.local.example.json conf/config.local.json
```

开发启动：

```sh
make run
```

默认监听本机 `8080` 端口。需要换端口时：

```sh
PORT=9090 make run
```

构建二进制：

```sh
make build
./easy_terminal
```

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

## 运行要求

- Go 1.25 或兼容版本
- Node.js，用于 `make test-browser`
- Chrome、Chromium 或 Microsoft Edge，用于 headless 终端快照和浏览器测试
- macOS/Linux shell 环境；默认 shell 为 `/bin/zsh -i`

## 运行数据

默认运行时文件会写在项目目录下：

- `easy_terminal.db`
- `data/uploads/`
- `log/easy_terminal.log`
- `conf/config.local.json`

这些文件已在 `.gitignore` 中忽略。
