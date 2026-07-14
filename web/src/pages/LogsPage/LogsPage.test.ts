import { describe, expect, it } from "vitest";
import { requestEntries, responseEntries } from "./LogsPage";

describe("log conversation parsing", () => {
  it("keeps a Responses string input as one user message", () => {
    expect(requestEntries({ input: "Reply with OK." })).toEqual([
      { role: "user", content: "Reply with OK." },
    ]);
  });

  it("renders Responses output reasoning and text as one assistant entry", () => {
    expect(responseEntries({
      output: [
        { type: "reasoning", summary: [{ type: "summary_text", text: "Check the request." }] },
        { type: "message", role: "assistant", content: [{ type: "output_text", text: "OK" }] },
      ],
    })).toEqual([
      { role: "assistant", content: "[思考]\nCheck the request.\n\nOK" },
    ]);
  });
});
