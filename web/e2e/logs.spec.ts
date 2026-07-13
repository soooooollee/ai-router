import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("logs page opens a readable conversation detail", async ({ page, request }) => {
  const response = await request.post("http://127.0.0.1:18080/v1/messages", {
    headers: { "content-type": "application/json", "x-api-key": "e2e-client-key" },
    data: { model: "fast", max_tokens: 32, messages: [{ role: "user", content: "show this complete chat" }] },
  });
  expect(response.ok()).toBeTruthy();
  const requestID = response.headers()["x-airoute-request-id"];
  await login(page);
  await page.getByRole("button", { name: "调用日志" }).click();
  await expect(page.getByRole("heading", { name: "调用日志" })).toBeVisible();
  await expect(page.getByRole("table")).toBeVisible();
  await expect(page.getByText(/显示 \d+ \/ \d+/)).toBeVisible();
  await page.getByText(requestID, { exact: true }).click();
  await expect(page.getByRole("dialog", { name: "日志详情" })).toBeVisible();
  await expect(page.getByText("show this complete chat", { exact: true })).toBeVisible();
  await expect(page.getByText("hello from mock", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "请求正文" }).click();
  await expect(page.locator(".log-raw-body")).toContainText("show this complete chat");
});
