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
  await expect(page.getByText(/包括密钥/)).toBeVisible();
  await expect(page.getByRole("tab")).toHaveCount(3);
  await expect(page.getByRole("tab", { name: "运行指标" })).toHaveCount(0);
  await page.getByRole("button", { name: "校验" }).click();
  await expect(
    page.getByText("JSON 格式和 AI Router 配置均有效"),
  ).toBeVisible();
  await page.getByRole("tab", { name: "日志记录" }).click();
  await expect(page.getByLabel("日志持久化")).toBeVisible();
  await expect(page.getByLabel("网页脱敏")).toHaveCount(0);
  await expect(page.getByLabel("记录聊天正文")).toBeChecked();
  await expect(page.getByText(/日志当前只保存在进程内存/)).toBeVisible();
  await page.getByLabel("记录聊天正文").uncheck();
  await page.getByRole("button", { name: "保存配置" }).click();
  await expect(page.getByText(/聊天正文记录已关闭.*后续新请求将不再记录/)).toBeVisible();
  await page.getByRole("tab", { name: "网页脱敏" }).click();
  await expect(page.getByText(/只影响网页展示/)).toBeVisible();
  await page.getByLabel("网页脱敏").check();
  await page.getByRole("button", { name: "保存配置" }).click();
  await expect(page.getByText(/网页脱敏已保存并备份/)).toBeVisible();
  await page.getByRole("tab", { name: "完整配置" }).click();
  await expect(page.getByLabel("系统 JSON 配置")).toContainText("••••••••");
  await expect(page.getByLabel("系统 JSON 配置")).not.toContainText("e2e-provider-key");
  await page.getByRole("button", { name: "模型接入" }).click();
  await expect(page.locator(".provider-key-value").first()).toHaveText("••••••••");
});
