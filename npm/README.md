# easy-terminal

Install:

```sh
npm install -g @lijuneleven/easy-terminal
easy-terminal
```

The CLI starts the local `easy_terminal` service. Pass server flags directly:

```sh
easy-terminal --port 9090
easy-terminal --config-dir /data/easy_terminal
```

`--config-dir` controls the local config and runtime data directory.

The installer downloads the platform binary from GitHub Release first, then falls back to Gitee Release.
