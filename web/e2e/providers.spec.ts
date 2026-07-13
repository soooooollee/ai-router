import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("provider detection and local API key display", async ({ page }) => {
  await login(page);
  await expect(page.getByText("e2e-provider-key", { exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "全部" })).toHaveCount(0);
  await page.getByRole("button", { name: /删\s*除/ }).first().click();
  await expect(page.getByRole("heading", { name: "删除模型服务？" })).toBeVisible();
  await page.getByRole("button", { name: "取消" }).click();
  await page.getByRole("button", { name: "测试" }).click();
  await expect(page.getByText("连接测试通过", { exact: true })).toBeVisible();
  await expect(
    page.locator(".ant-notification-notice-description"),
  ).toContainText("连接正常");

  await page.getByRole("button", { name: "+ 接入模型" }).click();
  await expect(page.getByText("检测到本机或局域网模型服务")).toBeHidden();
  await page.getByLabel("API 地址").fill("http://127.0.0.1:19090/v1");
  await expect(page.getByText("检测到本机或局域网模型服务")).toBeVisible();
  await page.getByLabel("API Key").fill("e2e-new-provider-key");
  await expect(page.getByText(/明文保存到本机/)).toBeVisible();
  await page.getByLabel("Model Names").fill("secondary-model");
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText(/请先确认允许 AI Router 访问/)).toBeVisible();
  await page.getByLabel("确认访问本机或私网模型服务").check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText(/识别成功：OpenAI/)).toBeVisible();
  await page.getByRole("button", { name: "确认接入并加入模型列表" }).click();
  await expect(page.getByText("secondary-model").first()).toBeVisible();
});
