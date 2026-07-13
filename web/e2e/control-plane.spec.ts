import { expect, test } from "@playwright/test";

test("complete control-plane workflow", async ({ page }) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "连接 AI Router" }),
  ).toBeVisible();
  await page.getByLabel("管理令牌").fill("e2e-admin-token-123456789012345");
  await page.getByRole("button", { name: "连接" }).click();
  await expect(
    page.getByRole("heading", { name: "模型接入", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("模型接入", { exact: true }).first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "测试" }).click();
  await expect(page.getByText("连接测试通过", { exact: true })).toBeVisible();
  await expect(
    page.locator(".ant-notification-notice-description"),
  ).toContainText("连接正常");
  await expect(page.getByRole("button", { name: /连接正常/ })).toBeVisible();
  await page.getByRole("button", { name: "+ 接入模型" }).click();
  await page.getByLabel("API 地址").fill("http://127.0.0.1:19090/v1");
  await page.getByLabel("API Key").fill("test-key");
  await page.getByLabel("Model Names").fill("secondary-model");
  await page.getByLabel("允许访问本机或私网地址").check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText(/识别成功：OpenAI/)).toBeVisible();
  await page.getByRole("button", { name: "确认接入并加入模型列表" }).click();
  await expect(page.getByText("secondary-model").first()).toBeVisible();

  await page.getByRole("button", { name: "路由配置" }).click();
  await page.getByRole("button", { name: "+ 添加路由" }).click();
  await page
    .getByLabel("选择接入模型")
    .selectOption("secondary-model:secondary-model");
  await page.getByLabel("路由模型名").fill("e2e-model");
  await page.getByLabel("输出协议").selectOption("openai-chat");
  await expect(
    page.getByText("http://127.0.0.1:18080/v1/chat/completions"),
  ).toBeVisible();
  await page.getByRole("button", { name: "保存路由" }).click();
  await expect(
    page.getByText("e2e-model", { exact: true }).first(),
  ).toBeVisible();

  await page.getByRole("button", { name: "应用配置" }).click();
  await expect(
    page.getByRole("heading", { name: "让本地应用使用 AI Router" }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Claude Code" }),
  ).toBeVisible();
  await expect(page.getByText("settings.json", { exact: true })).toBeVisible();
  await page.getByLabel("默认模型").selectOption("e2e-model");
  await page.getByRole("button", { name: "备份并写入" }).click();
  await expect(
    page.getByText(/已写入 .*airoute-claude-e2e\.json/),
  ).toBeVisible();

  await page.getByRole("button", { name: "系统设置" }).click();
  await expect(
    page.getByRole("heading", { name: "系统设置", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("系统 JSON 配置")).toContainText('"providers"');
  await page.getByRole("button", { name: "校验" }).click();
  await expect(
    page.getByText("JSON 格式和 AI Router 配置均有效"),
  ).toBeVisible();

  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: /运行中.*点击关闭/ }).click();
  await expect(
    page.getByRole("button", { name: /已关闭.*点击启动/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: /已关闭.*点击启动/ }).click();
  await expect(
    page.getByRole("button", { name: /运行中.*点击关闭/ }),
  ).toBeVisible();
});
