import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("settings validates JSON and explains sensitive configuration", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("button", { name: "系统设置" }).click();
  await expect(
    page.getByRole("heading", { name: "系统设置", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("系统 JSON 配置")).toContainText('"providers"');
  await expect(page.getByText(/可能含明文 Provider API Key/)).toBeVisible();
  await page.getByRole("button", { name: "校验" }).click();
  await expect(
    page.getByText("JSON 格式和 AI Router 配置均有效"),
  ).toBeVisible();
});
