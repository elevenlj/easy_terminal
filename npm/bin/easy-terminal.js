#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

const exeName = process.platform === "win32" ? "easy_terminal.exe" : "easy_terminal";
const binaryPath = process.env.EASY_TERMINAL_BINARY
  ? path.resolve(process.env.EASY_TERMINAL_BINARY)
  : path.resolve(__dirname, "..", "vendor", exeName);

if (!fs.existsSync(binaryPath)) {
  console.error(
    "easy-terminal binary is missing. Reinstall the package or set EASY_TERMINAL_BINARY to a local binary."
  );
  process.exit(1);
}

const child = spawn(binaryPath, process.argv.slice(2), {
  stdio: "inherit",
  env: process.env
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});

child.on("error", (err) => {
  console.error(err.message);
  process.exit(1);
});
