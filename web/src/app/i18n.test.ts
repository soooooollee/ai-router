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

  it("translates the model service name and upstream model fields", () => {
    expect(translateValue("模型服务名称", "en-US")).toBe("Service name");
    expect(translateValue("模型名称", "en-US")).toBe("Model Names");
  });

  it("translates Codex compatibility guidance in both directions", () => {
    const chinese = "正在使用 AI Router 兼容转换";
    const english = "Using AI Router compatibility conversion";
    expect(translateValue(chinese, "en-US")).toBe(english);
    expect(translateValue(english, "zh-CN")).toBe(chinese);

    expect(
      translateValue(
        "Codex 使用 high 推理等级时通常会同时发送 tools（如 apply_patch、shell 等工具定义）和 reasoning_effort（用于指定模型推理强度）。如果上游 Chat 接口拒绝 tools + reasoning_effort，AI Router 会在工具请求中移除 reasoning_effort 并保留工具调用；普通对话和工具调用仍可正常使用。",
        "en-US",
      ),
    ).not.toMatch(/[\u3400-\u9fff]/);
  });

  it("translates composed provider detection diagnostics", () => {
    expect(
      translateValue(
        "Codex 经 AI Router 端到端验证超时（耗时 5.2 秒）；其余流式输出、function tools、reasoning 与多轮续接均已验证",
        "en-US",
      ),
    ).toBe(
      "Codex end-to-end verification through AI Router timed out (5.2 s); all remaining streaming, function-tool, reasoning, and multi-turn continuation capabilities were verified",
    );
    expect(
      translateValue("尚未确认 · 请求超时 · 2.0 秒", "en-US"),
    ).toBe("Not confirmed · Request timed out · 2.0 s");
  });

  it("translates client credential lifecycle and deployment controls", () => {
    for (const value of [
      "轮换客户端密钥",
      "生成或轮换专用密钥",
      "我确认该公网监听客户端不设置速率、并发或每日配额",
      "标准托管密钥不会在应用配置页重新显示完整内容",
      "最近使用 / 到期",
    ]) {
      expect(translateValue(value, "en-US")).not.toBe(value);
    }
    expect(translateValue("2 个有效密钥", "en-US")).toBe("2 active keys");
  });
});
