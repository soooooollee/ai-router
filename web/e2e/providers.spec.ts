import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("provider detection and local API key display", async ({ page }) => {
  await login(page);
  await expect(page.getByText("e2e-provider-key", { exact: true })).toBeVisible();
  await expect(page.locator(".table-provider-identity > span")).toHaveCount(0);
  await expect(page.getByRole("tab", { name: "全部" })).toHaveCount(0);
  await page.getByRole("button", { name: /删\s*除/ }).first().click();
  await expect(page.getByRole("heading", { name: "删除模型服务？" })).toBeVisible();
  await page.getByRole("button", { name: "取消" }).click();
  await page.getByRole("button", { name: "测试" }).click();
  await expect(page.getByText("连接测试通过", { exact: true })).toBeVisible();
  await expect(
    page.locator(".ant-notification-notice-description"),
  ).toContainText("真实请求通过");

  await page.getByRole("button", { name: "+ 接入模型" }).click();
  await expect(page.getByText("接入协议", { exact: true })).toBeVisible();
  await expect(page.getByText("待自动识别", { exact: true })).toBeVisible();
  await expect(page.getByText("检测到本机或局域网模型服务")).toBeHidden();
  await page.getByLabel("API 地址").fill("http://127.0.0.1:19090/v1");
  await expect(page.getByText("检测到本机或局域网模型服务")).toBeVisible();
  await page.getByLabel("API Key").fill("e2e-new-provider-key");
  await expect(page.getByText(/明文保存到本机/)).toBeVisible();
  await page.getByLabel("Model Names").fill("secondary-model");
  const autoRoutes = page.getByLabel("自动生成每个模型的全部协议路由");
  await expect(autoRoutes).toBeChecked();
  await autoRoutes.uncheck();
  await expect(autoRoutes).not.toBeChecked();
  await autoRoutes.check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText(/请先确认允许 AI Router 访问/)).toBeVisible();
  await page.getByLabel("确认访问本机或私网模型服务").check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText("API 连接与响应结构验证成功", { exact: true })).toBeVisible();
  await expect(
    page
      .locator(".detected-protocol-field")
      .getByText("OpenAI Chat", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Codex CLI / ChatGPT App 经 AI Router 兼容", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".compatibility-result.degraded .lucide-triangle-alert")).toBeVisible();
  await expect(page.locator(".compatibility-result > svg")).toHaveCSS("width", "22px");
  await page.getByRole("button", { name: "确认接入并加入模型列表" }).click();
  await expect(page.getByText("secondary-model").first()).toBeVisible();
  const secondaryRow = page.getByRole("row").filter({ hasText: "secondary-model" });
  await expect(secondaryRow).toContainText("OpenAI Chat");
  await expect(secondaryRow.locator("small")).toHaveCount(0);

  await page.getByRole("button", { name: "路由配置" }).click();
  await expect(page.getByRole("heading", { name: "路由列表" })).toBeVisible();
  const generatedRows = page.locator("tbody tr").filter({
    has: page.getByText("secondary-model", { exact: true }),
  });
  await expect(generatedRows).toHaveCount(4);
  await expect(generatedRows.getByText("Anthropic Messages", { exact: true })).toHaveCount(1);
  await expect(generatedRows.getByText("OpenAI Chat", { exact: true })).toHaveCount(1);
  await expect(generatedRows.getByText("OpenAI Responses", { exact: true })).toHaveCount(1);
  await expect(generatedRows.getByText("Gemini", { exact: true })).toHaveCount(1);
  await expect(
    generatedRows.locator(".table-route-target code").filter({
      hasText: /^secondary-model$/,
    }),
  ).toHaveCount(4);

  await page.getByRole("button", { name: "模型接入" }).click();
  await page.getByRole("button", { name: "+ 接入模型" }).click();
  await page.getByLabel("API 地址").fill("http://127.0.0.1:19090/anthropic");
  await page.getByLabel("API Key").fill("e2e-mimo-key");
  await page.getByLabel("Model Names").fill("mimo-v2.5-pro");
  await page.getByLabel("确认访问本机或私网模型服务").check();
  await page.getByRole("button", { name: "测试连接并自动识别协议" }).click();
  await expect(page.getByText("API 连接与响应结构验证成功", { exact: true })).toBeVisible();
  await expect(
    page
      .locator(".detected-protocol-field")
      .getByText("Anthropic Messages", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Claude Code", { exact: true })).toHaveCount(0);
  await expect(page.getByText("原生协议兼容", { exact: true })).toHaveCount(0);
  await expect(page.locator(".compatibility-result")).toHaveCount(0);
  await expect(page.getByText("Codex 接入方式", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel(/我确认使用/)).toHaveCount(0);
  await page.getByRole("button", { name: "确认接入并加入模型列表" }).click();
  const mimoRow = page.getByRole("row").filter({ hasText: "mimo-v2.5-pro" });
  await expect(mimoRow).toContainText("Anthropic Messages");
  await expect(mimoRow.locator("small")).toHaveCount(0);
  await page.getByRole("button", { name: "路由配置" }).click();
  const mimoRoutes = page.locator("tbody tr").filter({
    has: page.getByText("mimo-v2.5-pro", { exact: true }),
  });
  await expect(mimoRoutes).toHaveCount(4);
  await expect(
    mimoRoutes.locator(".table-route-target code").filter({
      hasText: /^mimo-v2\.5-pro$/,
    }),
  ).toHaveCount(4);
  await expect(mimoRoutes.getByText(/Codex 完整兼容/)).toHaveCount(0);
  await page.getByRole("button", { name: "模型接入" }).click();
  const savedMimoRow = page.getByRole("row").filter({ hasText: "mimo-v2.5-pro" });
  await savedMimoRow.getByRole("button", { name: /删\s*除/ }).click();
  await page.getByRole("button", { name: "删除模型服务" }).click();
  await expect(savedMimoRow).toHaveCount(0);
});
