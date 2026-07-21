import { describe, expect, it } from "vitest";
import {
  applicationGatewayURL,
  applicationRouteOptions,
  compact,
  generatedRouteID,
  generatedProviderRoutes,
  protocolName,
  providerCodexCompatibilityName,
  providerProtocolName,
  providerModelLabel,
  removeProviderReferences,
  routeIdentifier,
  routeIDTimestamp,
} from "./lib";

describe("presentation helpers", () => {
  it("uses stable human names for every supported protocol", () => {
    expect(protocolName("openai-chat")).toBe("OpenAI Chat");
    expect(protocolName("openai-responses")).toBe("OpenAI Responses");
    expect(protocolName("anthropic-messages")).toBe("Anthropic Messages");
    expect(protocolName("gemini-generate-content")).toBe("Gemini");
    expect(protocolName("future-protocol")).toBe("future-protocol");
    expect(
      providerProtocolName({
        protocol: "openai-chat",
        compatibility_mode: "codex-chat",
      }),
    ).toBe("OpenAI Chat（Codex CLI / ChatGPT App 经 AI Router 兼容）");
    expect(
      providerCodexCompatibilityName({
        codex_integration: "compatibility",
        codex_compatibility: "full",
      }),
    ).toBe("Codex 经 AI Router 完整兼容");
    expect(
      providerCodexCompatibilityName({
        codex_integration: "compatibility",
        codex_compatibility: "unverified",
      }),
    ).toBe("Codex 经 AI Router 待验证");
    expect(
      providerCodexCompatibilityName({
        codex_integration: "compatibility",
        codex_compatibility: "degraded",
        reasoning_with_tools: "disabled",
      }),
    ).toBe("Codex CLI / ChatGPT App 经 AI Router 兼容");
  });

  it("formats counters compactly", () => {
    expect(compact(0)).toBe("0");
    expect(compact(1_000)).toMatch(/1[千万Kk]?/);
  });

  it("migrates legacy default application URLs to the active gateway", () => {
    expect(
      applicationGatewayURL(
        "http://127.0.0.1:8080",
        "http://127.0.0.1:12666",
      ),
    ).toBe("http://127.0.0.1:12666");
    expect(
      applicationGatewayURL(
        "https://router.example.com",
        "http://127.0.0.1:12666",
      ),
    ).toBe("https://router.example.com");
  });

  it("filters and deduplicates routes for each application's protocol", () => {
    const routes = [
      { id: "chat", priority: 100, match: { model: "mimo", protocol: "openai-chat" }, targets: [] },
      { id: "anthropic", priority: 100, match: { model: "mimo", protocol: "anthropic-messages" }, targets: [] },
      { id: "responses", priority: 100, match: { model: "mimo", protocol: "openai-responses" }, targets: [{ provider: "compat", model: "m" }] },
      { id: "generic", priority: 100, match: { model: "generic" }, targets: [] },
      { id: "generic-specific", priority: 100, match: { model: "generic", protocol: "openai-chat" }, targets: [] },
    ];
    const providers = [
      {
        id: "compat",
        name: "Compat",
        protocol: "openai-chat",
        compatibility_mode: "codex-chat" as const,
        base_url: "https://example.com/v1",
        models: ["m"],
        api_key_set: true,
      },
    ];
    expect(applicationRouteOptions(routes, "mimo-code")).toEqual([
      { alias: "mimo", protocol: "openai-chat" },
      { alias: "generic", protocol: "openai-chat" },
    ]);
    expect(applicationRouteOptions(routes, "codex", providers)).toEqual([
      {
        alias: "mimo",
        protocol: "openai-responses",
        compatibility_mode: "codex-chat",
        integration_mode: "compatibility",
        provider_id: "compat",
        provider_name: "Compat",
        provider_base_url: "https://example.com/v1",
        provider_model: "m",
      },
      { alias: "generic" },
    ]);
    expect(applicationRouteOptions(routes, "claude-code")).toEqual([
      { alias: "mimo", protocol: "anthropic-messages" },
      { alias: "generic" },
    ]);
  });

  it("preserves full Codex compatibility in application route options", () => {
    const routes = [{
      id: "mimo-responses",
      priority: 100,
      match: { model: "mimo-v2.5-pro", protocol: "openai-responses" },
      targets: [{ provider: "mimo", model: "mimo-v2.5-pro" }],
    }];
    const providers = [{
      id: "mimo",
      name: "mimo-v2.5-pro",
      protocol: "anthropic-messages",
      codex_integration: "compatibility" as const,
      codex_compatibility: "full" as const,
      reasoning_with_tools: "supported" as const,
      base_url: "https://example.com/anthropic",
      models: ["mimo-v2.5-pro"],
      api_key_set: true,
    }];
    expect(applicationRouteOptions(routes, "codex", providers)).toEqual([{
      alias: "mimo-v2.5-pro",
      protocol: "openai-responses",
      compatibility_mode: "codex-responses",
      integration_mode: "compatibility",
      codex_compatibility: "full",
      reasoning_with_tools: "supported",
      provider_id: "mimo",
      provider_name: "mimo-v2.5-pro",
      provider_base_url: "https://example.com/anthropic",
      provider_model: "mimo-v2.5-pro",
    }]);
  });

  it("derives a safe route identifier from the selected upstream model", () => {
    expect(routeIdentifier("mimo-v2.5")).toBe("mimo-v2.5");
    expect(routeIdentifier("vendor/Qwen 3 Coder")).toBe("qwen-3-coder");
    expect(routeIdentifier("模型")).toBe("model");
  });

  it("generates a unique route ID from the alias, protocol and time", () => {
    const timestamp = routeIDTimestamp(new Date(2026, 6, 14, 11, 35, 20, 123));
    expect(timestamp).toBe("20260714113520123");
    expect(generatedRouteID("mimo-v2.5", "openai-chat", timestamp)).toBe(
      "mimo-v2.5-openai-chat-20260714113520123",
    );
  });

  it("generates every protocol route for each new provider model and skips existing matches", () => {
    const existing = [{
      id: "existing-chat",
      priority: 100,
      match: { model: "gpt-5.5", protocol: "openai-chat" },
      targets: [{ provider: "old", model: "old-model" }],
    }];
    const routes = generatedProviderRoutes(
      existing,
      "new-provider",
      ["gpt-5.5", "vendor/coder", "gpt-5.5"],
      new Date(2026, 6, 20, 14, 0, 0, 0),
    );
    expect(routes).toHaveLength(7);
    expect(routes.some((route) =>
      route.match.model === "gpt-5.5" && route.match.protocol === "openai-chat",
    )).toBe(false);
    expect(routes.filter((route) => route.match.model === "coder")).toHaveLength(4);
    expect(routes.every((route) =>
      route.targets[0].provider === "new-provider" && route.priority === 100,
    )).toBe(true);
    expect(new Set(routes.map((route) => route.id)).size).toBe(routes.length);
  });

  it("does not repeat a model service name that matches its model", () => {
    expect(providerModelLabel("mimo-v2.5", "mimo", "mimo-v2.5")).toBe(
      "mimo-v2.5",
    );
    expect(providerModelLabel("Xiaomi", "mimo", "mimo-v2.5")).toBe(
      "Xiaomi / mimo-v2.5",
    );
    expect(providerModelLabel("gpt-5.5", "gpt-5-5", "gpt-5.5")).toBe(
      "gpt-5.5",
    );
  });

  it("removes a provider and every routing reference to it", () => {
    const document = removeProviderReferences(
      {
        providers: [{ id: "openai" }, { id: "anthropic" }],
        routes: [
          { targets: [{ provider: "openai" }, { provider: "anthropic" }] },
          { targets: [{ provider: "openai" }] },
        ],
        default_route: { targets: [{ provider: "openai" }] },
      },
      "openai",
    );
    expect(document.providers).toEqual([{ id: "anthropic" }]);
    expect(document.routes).toEqual([
      { targets: [{ provider: "anthropic" }] },
    ]);
    expect(document.default_route).toBeUndefined();
  });
});
