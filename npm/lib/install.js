"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const https = require("node:https");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const { assetName } = require("./platform");

const MAX_REDIRECTS = 8;

function request(url, destination, redirects = 0) {
  if (redirects > MAX_REDIRECTS) {
    return Promise.reject(new Error(`too many redirects while downloading ${url}`));
  }

  return new Promise((resolve, reject) => {
    const transport = url.startsWith("https:") ? https : http;
    const headers = { "user-agent": "airoute-npm-installer" };
    const token = process.env.AIROUTE_GITHUB_TOKEN || process.env.GITHUB_TOKEN;
    if (token && url.includes("github.com")) {
      headers.authorization = `Bearer ${token}`;
    }

    const req = transport.get(url, { headers }, (response) => {
      if (response.statusCode >= 300 && response.statusCode < 400 && response.headers.location) {
        response.resume();
        const next = new URL(response.headers.location, url).toString();
        request(next, destination, redirects + 1).then(resolve, reject);
        return;
      }
      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`download failed with HTTP ${response.statusCode}: ${url}`));
        return;
      }

      const output = fs.createWriteStream(destination, { mode: 0o600 });
      response.pipe(output);
      output.on("finish", () => output.close(resolve));
      output.on("error", reject);
    });
    req.on("error", reject);
  });
}

function sha256(filename) {
  const hash = crypto.createHash("sha256");
  hash.update(fs.readFileSync(filename));
  return hash.digest("hex");
}

function expectedChecksum(checksums, asset) {
  const line = checksums
    .split(/\r?\n/)
    .find((entry) => entry.trim().split(/\s+/).at(-1)?.replace(/^\*/, "") === asset);
  if (!line) {
    throw new Error(`checksum for ${asset} was not found`);
  }
  return line.trim().split(/\s+/)[0];
}

function run(command, args) {
  const result = spawnSync(command, args, { encoding: "utf8", stdio: "pipe" });
  if (result.status !== 0) {
    const detail = (result.stderr || result.stdout || "").trim();
    throw new Error(`${command} failed${detail ? `: ${detail}` : ""}`);
  }
}

function extract(archive, destination, platform = process.platform) {
  if (platform === "win32") {
    const escapedArchive = archive.replace(/'/g, "''");
    const escapedDestination = destination.replace(/'/g, "''");
    run("powershell.exe", [
      "-NoLogo",
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      `Expand-Archive -LiteralPath '${escapedArchive}' -DestinationPath '${escapedDestination}' -Force`,
    ]);
    return;
  }
  run("tar", ["-xzf", archive, "-C", destination]);
}

async function install(options = {}) {
  const packageRoot = options.packageRoot || path.resolve(__dirname, "..");
  const packageVersion = require(path.join(packageRoot, "package.json")).version;
  const version = (process.env.AIROUTE_VERSION || packageVersion).replace(/^v/, "");
  const asset = assetName(version);
  const base = (process.env.AIROUTE_DOWNLOAD_BASE ||
    `https://github.com/soooooollee/ai-router/releases/download/v${version}`).replace(/\/$/, "");
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "airoute-npm-"));
  const archive = path.join(temporary, asset);
  const checksumsPath = path.join(temporary, "checksums.txt");
  const vendor = path.join(packageRoot, "vendor");
  const binaryName = process.platform === "win32" ? "air.exe" : "air";
  const legacyBinaryName = process.platform === "win32" ? "airoute.exe" : "airoute";

  try {
    process.stdout.write(`Downloading AI Router v${version} for ${process.platform}/${process.arch}...\n`);
    await Promise.all([
      request(`${base}/${asset}`, archive),
      request(`${base}/checksums.txt`, checksumsPath),
    ]);

    const expected = expectedChecksum(fs.readFileSync(checksumsPath, "utf8"), asset);
    const actual = sha256(archive);
    if (actual !== expected) {
      throw new Error(`checksum verification failed for ${asset}`);
    }

    fs.rmSync(vendor, { recursive: true, force: true });
    fs.mkdirSync(vendor, { recursive: true });
    extract(archive, vendor);

    const binary = path.join(vendor, binaryName);
    const legacyBinary = path.join(vendor, legacyBinaryName);
    if (!fs.existsSync(binary) && fs.existsSync(legacyBinary)) {
      fs.renameSync(legacyBinary, binary);
    }
    if (!fs.existsSync(binary)) {
      throw new Error(`release archive did not contain ${binaryName}`);
    }
    if (process.platform !== "win32") {
      fs.chmodSync(binary, 0o755);
    }
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
}

module.exports = { expectedChecksum, extract, install, request, sha256 };
