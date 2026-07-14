import { describe, expect, it } from "vitest";
import {
  applicationGatewayURL,
  applicationRouteOptions,
  compact,
  generatedRouteID,
  protocolName,
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
      { id: "responses", priority: 100, match: { model: "mimo", protocol: "openai-responses" }, targets: [] },
      { id: "generic", priority: 100, match: { model: "generic" }, targets: [] },
      { id: "generic-specific", priority: 100, match: { model: "generic", protocol: "openai-chat" }, targets: [] },
    ];
    expect(applicationRouteOptions(routes, "mimo-code")).toEqual([
      { alias: "mimo", protocol: "openai-chat" },
      { alias: "generic", protocol: "openai-chat" },
    ]);
    expect(applicationRouteOptions(routes, "codex")).toEqual([
      { alias: "mimo", protocol: "openai-responses" },
      { alias: "generic" },
    ]);
    expect(applicationRouteOptions(routes, "claude-code")).toEqual([
      { alias: "mimo", protocol: "anthropic-messages" },
      { alias: "generic" },
    ]);
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

  it("does not repeat a model service name that matches its model", () => {
    expect(providerModelLabel("mimo-v2.5", "mimo", "mimo-v2.5")).toBe(
      "mimo-v2.5",
    );
    expect(providerModelLabel("Xiaomi", "mimo", "mimo-v2.5")).toBe(
      "Xiaomi / mimo-v2.5",
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
