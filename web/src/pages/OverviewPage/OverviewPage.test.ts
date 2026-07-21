import { describe, expect, it } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { formatUptime, OverviewPage } from "./OverviewPage";

describe("overview summary", () => {
  it("formats short and long process uptimes", () => {
    expect(formatUptime(42, "zh-CN")).toBe("42 秒");
    expect(formatUptime(3720, "zh-CN")).toBe("1 小时 2 分钟");
    expect(formatUptime(90000, "zh-CN")).toBe("1 天 1 小时");
  });

  it("keeps runtime metrics while adding configuration summary cards", () => {
    const html = renderToStaticMarkup(
      React.createElement(OverviewPage, { status: {
        status: "running",
        version: "test",
        uptime_seconds: 42,
        config_version: "hash",
        gateway_url: "http://127.0.0.1:12666",
        providers: 3,
        models: 4,
        routes: 12,
        applications_configured: 2,
        applications_total: 4,
        logs: 27,
        logs_capacity: 1000,
        logging_persist: true,
        metrics: {
          requests: 8, errors: 1, in_flight: 0, retries: 0, fallbacks: 0,
          input_tokens: 120, output_tokens: 80, timeouts: 0, cancellations: 0,
          diagnostics: 0, p50_latency_ms: 20, p95_latency_ms: 50,
        },
      } }),
    );
    expect(html).toContain("模型数量");
    expect(html).toContain("已配置应用");
    expect(html).toContain("日志持久化");
    expect(html).not.toContain("配置概览");
    expect(html).not.toContain("请求与性能");
    expect(html).toContain("累计请求");
    expect(html).toContain("Token 消耗");
  });
});
