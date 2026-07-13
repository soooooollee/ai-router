import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("route creation shows the protocol-specific address", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: "路由配置" }).click();
  await page.getByRole("button", { name: "+ 添加路由" }).click();
  await page.getByLabel("选择接入模型").selectOption("mock:mock-model");
  await page.getByLabel("客户端模型名").fill("e2e-model");
  await page.getByLabel("客户端调用协议").selectOption("openai-chat");
  await expect(
    page.getByText("http://127.0.0.1:18080/v1/chat/completions"),
  ).toBeVisible();
  await page.getByRole("button", { name: "保存路由" }).click();
  await expect(
    page.getByText("e2e-model", { exact: true }).first(),
  ).toBeVisible();
});
