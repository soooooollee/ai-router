#!/usr/bin/env node

"use strict";

const path = require("node:path");
const { spawn } = require("node:child_process");

const binary = path.resolve(
  __dirname,
  "..",
  "vendor",
  process.platform === "win32" ? "air.exe" : "air",
);

const child = spawn(binary, process.argv.slice(2), { stdio: "inherit" });
child.on("error", (error) => {
  process.stderr.write(`air: ${error.message}\nTry reinstalling with: npm install --global airoute-cli@latest\n`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
