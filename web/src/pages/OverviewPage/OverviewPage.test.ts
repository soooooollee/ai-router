import { describe, expect, it } from "vitest";
import { formatUptime } from "./OverviewPage";

describe("overview summary", () => {
  it("formats short and long process uptimes", () => {
    expect(formatUptime(42, "zh-CN")).toBe("42 秒");
    expect(formatUptime(3720, "zh-CN")).toBe("1 小时 2 分钟");
    expect(formatUptime(90000, "zh-CN")).toBe("1 天 1 小时");
  });
});
