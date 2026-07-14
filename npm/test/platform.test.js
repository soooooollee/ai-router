"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { assetName, releaseTarget } = require("../lib/platform");
const { expectedChecksum } = require("../lib/install");

test("maps supported platforms to GoReleaser asset names", () => {
  assert.equal(assetName("1.2.3", "darwin", "arm64"), "airoute_1.2.3_darwin_arm64.tar.gz");
  assert.equal(assetName("1.2.3", "linux", "x64"), "airoute_1.2.3_linux_amd64.tar.gz");
  assert.equal(assetName("1.2.3", "win32", "x64"), "airoute_1.2.3_windows_amd64.zip");
});

test("rejects unsupported platforms and architectures", () => {
  assert.throws(() => releaseTarget("freebsd", "x64"), /unsupported operating system/);
  assert.throws(() => releaseTarget("linux", "ia32"), /unsupported architecture/);
});

test("finds a checksum by exact asset name", () => {
  const checksums = [
    "abc123  airoute_1.2.3_linux_amd64.tar.gz",
    "def456  airoute_1.2.3_linux_arm64.tar.gz",
  ].join("\n");
  assert.equal(expectedChecksum(checksums, "airoute_1.2.3_linux_arm64.tar.gz"), "def456");
  assert.throws(() => expectedChecksum(checksums, "missing.tar.gz"), /was not found/);
});
