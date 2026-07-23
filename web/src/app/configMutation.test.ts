import { afterEach, describe, expect, it, vi } from "vitest";
import { mutateLatestConfig } from "./configMutation";

function jsonResponse(status: number, body: any) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

describe("mutateLatestConfig", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("rebases on the latest configuration and retries one conflict", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(200, {
          yaml: "version: 1\nproviders: []\nroutes: []\n",
          hash: "old",
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse(409, {
          error: "configuration changed since it was loaded",
          code: "configuration_conflict",
          current_hash: "new",
        }),
      )
      .mockResolvedValueOnce(
        jsonResponse(200, {
          yaml: "version: 1\nproviders:\n  - id: concurrent\nroutes: []\n",
          hash: "new",
        }),
      )
      .mockResolvedValueOnce(jsonResponse(200, { ok: true, hash: "saved" }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await mutateLatestConfig((document) => {
      document.providers ||= [];
      document.providers.push({ id: "added" });
    });

    expect(result.hash).toBe("saved");
    expect(result.yaml).toContain("id: concurrent");
    expect(result.yaml).toContain("id: added");
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });
});
