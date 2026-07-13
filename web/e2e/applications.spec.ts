import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("application manifest, preview, apply and gateway verification", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("button", { name: "应用配置" }).click();
  await expect(
    page.getByRole("heading", { name: "Claude Code" }),
  ).toBeVisible();
  await expect(page.getByText("/tmp/airoute-claude-e2e.json")).toBeVisible();
  await page.getByLabel("AI Router 客户端密钥").fill("e2e-client-key");
  await page.getByLabel("默认模型").selectOption("fast");
  await page.getByRole("button", { name: "刷新预览" }).click();
  await expect(page.getByText(/密钥已脱敏/)).toBeVisible();
  await expect(
    page.locator(".application-preview-panel pre").first(),
  ).toContainText("ANTHROPIC_MODEL");
  await page.getByRole("button", { name: "备份并写入" }).click();
  await expect(
    page.getByText(/已写入 .*airoute-claude-e2e\.json/),
  ).toBeVisible();
  await page.getByRole("button", { name: "验证连接" }).click();
  await expect(page.getByText("Anthropic 协议链路验证成功")).toBeVisible();
});
