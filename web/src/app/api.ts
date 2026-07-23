export class APIError extends Error {
  status: number;
  data: any;

  constructor(status: number, data: any) {
    super(data?.error || `HTTP ${status}`);
    this.name = "APIError";
    this.status = status;
    this.data = data;
  }
}

export function api(path: string, init: RequestInit = {}) {
  const token =
    typeof sessionStorage === "undefined"
      ? ""
      : sessionStorage.getItem("airoute_token") || "";
  const headers = new Headers(init.headers);
  headers.set("content-type", "application/json");
  if (token) headers.set("authorization", `Bearer ${token}`);
  return fetch(path, { ...init, headers }).then(async (r) => {
    if (r.headers.get("content-type")?.includes("text/event-stream")) {
      const body = await r.text();
      return {
        status: Number(r.headers.get("x-airoute-playground-status") || 200),
        content_type: "text/event-stream",
        body,
        request_id: r.headers.get("x-airoute-request-id"),
      };
    }
    const data = await r.json().catch(() => ({ error: r.statusText }));
    if (!r.ok) throw new APIError(r.status, data);
    return data;
  });
}
