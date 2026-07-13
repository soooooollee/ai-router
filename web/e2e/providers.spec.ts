import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("provider detection and secret storage metadata", async ({ page }) => {
  await login(page);
  await expect(page.getByText("环境变量", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "测试" }).click();
  await expect(page.getByText("连接测试通过", { exact: true })).toBeVisible();
  await expect(
    page.locator(".ant-notification-notice-description"),
  ).toContainText("连接正常");

  await page.getByRole("button", { name: "+ 接入模型" }).click();
  await page.getByLabel("API 地址").fill("http://127.0.0.1:19090/v1");
  await page.getByLabel("API Key").fill("${PROVIDER_API_KEY}");
  await expect(page.getByText(/环境变量模式：PROVIDER_API_KEY/)).toBeVisible();
  await page.getByLabel("Model Names").fill("secondary-model");
  await page.getByLabel("允许访问本机或私网地址").check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText(/识别成功：OpenAI/)).toBeVisible();
  await page.getByRole("button", { name: "确认接入并加入模型列表" }).click();
  await expect(page.getByText("secondary-model").first()).toBeVisible();
});
