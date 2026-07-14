"use strict";

const PLATFORM_NAMES = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_NAMES = {
  x64: "amd64",
  arm64: "arm64",
};

function releaseTarget(platform = process.platform, arch = process.arch) {
  const os = PLATFORM_NAMES[platform];
  const cpu = ARCH_NAMES[arch];

  if (!os) {
    throw new Error(`unsupported operating system: ${platform}`);
  }
  if (!cpu) {
    throw new Error(`unsupported architecture: ${arch}`);
  }

  return { os, arch: cpu, extension: os === "windows" ? "zip" : "tar.gz" };
}

function assetName(version, platform = process.platform, arch = process.arch) {
  const target = releaseTarget(platform, arch);
  return `airoute_${version}_${target.os}_${target.arch}.${target.extension}`;
}

module.exports = { assetName, releaseTarget };
