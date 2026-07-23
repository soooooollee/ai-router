import { parse, stringify } from "yaml";
import { APIError, api } from "./api";

export async function mutateLatestConfig(
  mutate: (document: any) => void,
): Promise<{ yaml: string; hash: string }> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const current = await api("/api/config");
    const document = parse(current.yaml) || {};
    mutate(document);
    const yaml = stringify(document);
    try {
      const saved = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml, expected_hash: current.hash }),
      });
      return { yaml, hash: saved.hash };
    } catch (error) {
      if (!(error instanceof APIError) || error.status !== 409 || attempt > 0) {
        throw error;
      }
    }
  }
  throw new Error("配置持续发生变化，请稍后重试");
}
