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
  await expect(page.getByRole("button", { name: "刷新预览" })).toHaveCount(0);
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → Anthropic Messages",
	]);
  await page.getByLabel("AI Router 客户端密钥").fill("e2e-client-key");
  await page.getByLabel("默认模型").selectOption("fast");
  await expect(page.getByText(/密钥按原文显示/)).toBeVisible();
  await expect(
    page.locator(".application-preview-panel pre").first(),
  ).toContainText("e2e-client-key");
  const saveButton = page.getByRole("button", { name: "备份并写入" });
  await expect(saveButton).toHaveCSS("font-size", "13px");
  expect((await saveButton.boundingBox())?.width).toBeGreaterThanOrEqual(144);
  await saveButton.click();
  await expect(
    page.getByText(/已写入 .*airoute-claude-e2e\.json/),
  ).toBeVisible();
  await saveButton.click();
  const backups = page.locator(".application-backups");
  await expect(backups.getByText("配置备份", { exact: true })).toHaveCSS(
    "font-size",
    "13px",
  );
  await expect(backups.getByText("最近的自动备份可随时恢复")).toHaveCSS(
    "font-size",
    "12px",
  );
  await expect(backups.locator(".application-backup-row b").first()).toHaveCSS(
    "font-size",
    "13px",
  );
  await expect(
    backups.locator(".application-backup-row span").first(),
  ).toHaveCSS("font-size", "12px");
  await expect(backups.getByRole("button", { name: "恢复" }).first()).toHaveCSS(
    "font-size",
    "13px",
  );
  const currentTab = page.getByRole("tab", { name: "当前配置" });
  const mergedTab = page.getByRole("tab", { name: "合并后配置" });
  const currentTabBox = await currentTab.boundingBox();
  const mergedTabBox = await mergedTab.boundingBox();
  expect(currentTabBox?.width).toBe(mergedTabBox?.width);
  expect(currentTabBox?.height).toBe(mergedTabBox?.height);
  await currentTab.click();
  await expect(page.locator(".application-preview-panel pre").first()).toContainText(
    "ANTHROPIC_BASE_URL",
  );
  await mergedTab.click();
  await expect(page.locator(".application-preview-panel pre").first()).toContainText(
    "e2e-client-key",
  );
  const backupCount = await backups.locator(".application-backup-row").count();
  const backupName = await backups.locator(".application-backup-row b").first().textContent();
  await backups.getByRole("button", { name: "删除" }).first().click();
  await expect(page.getByRole("heading", { name: "删除配置备份？" })).toBeVisible();
  await page.getByRole("button", { name: "删除备份" }).click();
  await expect(page.getByText(`已删除备份 ${backupName}。`)).toBeVisible();
  await expect(backups.locator(".application-backup-row")).toHaveCount(
    backupCount - 1,
  );
  await page.getByRole("button", { name: "验证连接" }).click();
  await expect(page.getByText("Anthropic 协议链路验证成功")).toBeVisible();

  await page.getByRole("tab", { name: "Claude App" }).click();
  await expect(page.getByRole("heading", { name: "Claude App" })).toBeVisible();
  const configPath = page.getByLabel(/configLibrary\/8f69f2f1/);
  await expect(configPath).toBeVisible();
  await configPath.hover();
  await expect(page.getByRole("tooltip")).toContainText("configLibrary/8f69f2f1");
  await expect(page.getByText(/保存后需重启 Claude App/)).toBeVisible();
  await expect(page.getByLabel("AI Router 客户端密钥")).toHaveValue("e2e-client-key");
  await expect(page.locator(".application-preview-panel pre").first()).toContainText("inferenceGatewayBaseUrl");

  await page.getByRole("tab", { name: "Codex" }).click();
  await expect(page.getByRole("heading", { name: "Codex" })).toBeVisible();
  await expect(page.getByText("/tmp/airoute-codex-e2e.toml")).toBeVisible();
  await expect(page.getByText(/Responses 协议/)).toBeVisible();
  await expect(page.getByLabel("默认模型")).toHaveCount(1);
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → OpenAI Responses",
	]);
  await expect(page.getByText("Sonnet 角色")).toHaveCount(0);
  await expect(page.locator(".application-preview-panel pre").first()).toContainText(
    'wire_api = "responses"',
  );
  await page.getByRole("button", { name: "备份并写入" }).click();
  await expect(page.getByText(/已写入 \/tmp\/airoute-codex-e2e\.toml/)).toBeVisible();

  await page.getByRole("tab", { name: "MiMo Code" }).click();
  await expect(page.getByRole("heading", { name: "MiMo Code" })).toBeVisible();
  await expect(page.getByText("/tmp/airoute-mimocode-e2e.json")).toBeVisible();
  await expect(page.getByText(/OpenAI 兼容协议/)).toBeVisible();
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → OpenAI Chat",
	]);
  await expect(page.locator(".application-preview-panel pre").first()).toContainText(
    "@ai-sdk/openai-compatible",
  );
  await expect(page.locator(".application-preview-panel pre").first()).toContainText(
    '"fast"',
  );
  await page.getByRole("button", { name: "备份并写入" }).click();
  await expect(page.getByText(/已写入 \/tmp\/airoute-mimocode-e2e\.json/)).toBeVisible();
});
