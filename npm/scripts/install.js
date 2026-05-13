#!/usr/bin/env node

const fs = require("fs");
const os = require("os");
const path = require("path");
const https = require("https");
const { pipeline } = require("stream/promises");
const { createWriteStream } = require("fs");

const packageJson = require("../package.json");

const owner = process.env.EASY_TERMINAL_GITHUB_OWNER || "elevenlj";
const repo = process.env.EASY_TERMINAL_GITHUB_REPO || "easy_terminal";
const version = packageJson.version;
const platform = process.platform;
const arch = process.arch;

const platformMap = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows"
};

const archMap = {
  x64: "amd64",
  arm64: "arm64"
};

function fail(message) {
  console.error(`easy-terminal install failed: ${message}`);
  process.exit(1);
}

if (process.env.EASY_TERMINAL_SKIP_DOWNLOAD === "1") {
  process.exit(0);
}

const targetPlatform = platformMap[platform];
const targetArch = archMap[arch];

if (!targetPlatform || !targetArch) {
  fail(`unsupported platform: ${platform}/${arch}`);
}

const ext = targetPlatform === "windows" ? ".exe" : "";
const assetName = `easy_terminal-${targetPlatform}-${targetArch}${ext}`;
const url = `https://github.com/${owner}/${repo}/releases/download/v${version}/${assetName}`;
const vendorDir = path.resolve(__dirname, "..", "vendor");
const outPath = path.join(vendorDir, targetPlatform === "windows" ? "easy_terminal.exe" : "easy_terminal");

async function download(downloadUrl, redirects = 0) {
  if (redirects > 5) {
    fail("too many redirects while downloading binary");
  }

  await fs.promises.mkdir(vendorDir, { recursive: true });

  await new Promise((resolve, reject) => {
    https
      .get(downloadUrl, (res) => {
        if ([301, 302, 303, 307, 308].includes(res.statusCode || 0)) {
          res.resume();
          download(res.headers.location, redirects + 1).then(resolve, reject);
          return;
        }

        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`download returned HTTP ${res.statusCode}`));
          return;
        }

        pipeline(res, createWriteStream(outPath)).then(resolve, reject);
      })
      .on("error", reject);
  });

  if (targetPlatform !== "windows") {
    await fs.promises.chmod(outPath, 0o755);
  }
}

download(url).catch((err) => {
  try {
    fs.rmSync(outPath, { force: true });
  } catch (_) {
  }
  fail(`${err.message}. URL: ${url}`);
});
