import { describe, expect, it } from "vitest";
import { compact, protocolName } from "./lib";

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
});
