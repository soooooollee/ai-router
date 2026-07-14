import { describe, expect, it } from "vitest";
import { translateValue } from "./i18n";

describe("application detection localization", () => {
  it("translates backend detection messages when applications are absent", () => {
    expect(translateValue("未检测到 Claude Code 命令", "en-US")).toBe(
      "Claude Code command not found",
    );
    expect(translateValue("未检测到 Claude App", "en-US")).toBe(
      "Claude App not detected",
    );
  });

  it("translates every Claude Code detection outcome", () => {
    expect(translateValue("Claude Code 已安装", "en-US")).toBe(
      "Claude Code installed",
    );
    expect(
      translateValue("已检测到命令，但版本读取失败", "en-US"),
    ).toBe("Command detected, but version lookup failed");
  });

  it("translates the sidebar version status", () => {
    expect(translateValue("当前版本", "en-US")).toBe("Current version");
    expect(translateValue("可更新到", "en-US")).toBe("Update available:");
  });
});
