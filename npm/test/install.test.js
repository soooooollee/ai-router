"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { downloadBase } = require("../lib/install");

test("downloadBase uses the default release repository", () => {
  delete process.env.AIROUTE_REPO;
  delete process.env.AIROUTE_DOWNLOAD_BASE;
  assert.equal(
    downloadBase("0.3.4"),
    "https://github.com/soooooollee/ai-router/releases/download/v0.3.4",
  );
});

test("downloadBase follows AIROUTE_REPO for forks", () => {
  delete process.env.AIROUTE_DOWNLOAD_BASE;
  process.env.AIROUTE_REPO = "/acme/airoute/";
  assert.equal(
    downloadBase("1.0.0"),
    "https://github.com/acme/airoute/releases/download/v1.0.0",
  );
  delete process.env.AIROUTE_REPO;
});

test("downloadBase prefers an explicit mirror", () => {
  process.env.AIROUTE_REPO = "acme/airoute";
  process.env.AIROUTE_DOWNLOAD_BASE = "https://mirror.example/air/";
  assert.equal(downloadBase("1.0.0"), "https://mirror.example/air");
  delete process.env.AIROUTE_REPO;
  delete process.env.AIROUTE_DOWNLOAD_BASE;
});
