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
  await expect(page.locator(".horizontal-sheets button")).toHaveText([
    "Claude Code",
    "Claude App",
    "Codex CLI",
    "ChatGPT App",
    "MiMo Code",
  ]);
  await expect(page.getByText("/tmp/airoute-claude-e2e.json")).toBeVisible();
  await expect(page.getByRole("button", { name: "刷新预览" })).toHaveCount(0);
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → Anthropic Messages",
	]);
  await page.getByLabel("AI Router 客户端密钥").fill("e2e-client-key");
  await page.getByLabel("默认模型").selectOption("fast");
  await expect(page.getByText(/可直接编辑合并后配置/)).toBeVisible();
  await expect(
    page.getByLabel("编辑合并后配置"),
  ).toHaveValue(/e2e-client-key/);
  const saveButton = page.getByRole("button", {
    name: "备份并写入",
    exact: true,
  });
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
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(
    /e2e-client-key/,
  );
  const editor = page.getByLabel("编辑合并后配置");
  const editedConfig = JSON.parse(await editor.inputValue());
  editedConfig.manual_preview_field = true;
  await editor.fill(JSON.stringify(editedConfig, null, 2));
  await page.getByRole("button", { name: "备份并写入修改" }).click();
  await expect(page.getByText(/已写入手动修改的配置/)).toBeVisible();
  const backupName = await backups.locator(".application-backup-row b").first().textContent();
  await backups.getByRole("button", { name: "删除" }).first().click();
  await expect(page.getByRole("heading", { name: "删除配置备份？" })).toBeVisible();
  await page.getByRole("button", { name: "删除备份" }).click();
  await expect(page.getByText(`已删除备份 ${backupName}。`)).toBeVisible();
  await expect(
    backups.locator(".application-backup-row b", { hasText: backupName ?? "" }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "验证连接" }).click();
  await expect(page.getByText("Anthropic 协议链路验证成功")).toBeVisible();

  await page.getByRole("tab", { name: "Claude App" }).click();
  await expect(page.getByRole("heading", { name: "Claude App" })).toBeVisible();
  const configPath = page.getByLabel(/configLibrary\/8f69f2f1/);
  await expect(configPath).toBeVisible();
  await expect(configPath).toHaveAttribute("title", /configLibrary\/8f69f2f1/);
  await expect(page.getByText(/保存后需重启 Claude App/)).toBeVisible();
  await expect(page.getByLabel("AI Router 客户端密钥")).toHaveValue("e2e-client-key");
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(
    /inferenceGatewayBaseUrl/,
  );

  await page.getByRole("tab", { name: "Codex CLI" }).click();
  await expect(page.getByRole("heading", { name: "Codex CLI" })).toBeVisible();
  await expect(
    page.getByLabel("/tmp/airoute-codex-e2e.toml", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/Responses 协议/)).toBeVisible();
  await expect(page.getByLabel("默认模型")).toHaveCount(1);
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → OpenAI Responses",
	]);
  await expect(page.getByText("Sonnet 角色")).toHaveCount(0);
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(
    /wire_api = "responses"/,
  );
  await page
    .getByRole("button", { name: "备份并写入", exact: true })
    .click();
  await expect(page.getByText(/已写入 \/tmp\/airoute-codex-e2e\.toml/)).toBeVisible();

  await page.getByRole("tab", { name: "ChatGPT App" }).click();
  await expect(page.getByRole("heading", { name: "ChatGPT App" })).toBeVisible();
  await expect(
    page.getByLabel("/tmp/airoute-codex-e2e.toml", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/共享 ~\/\.codex\/config\.toml/)).toBeVisible();

  await page.getByRole("tab", { name: "MiMo Code" }).click();
  await expect(page.getByRole("heading", { name: "MiMo Code" })).toBeVisible();
  await expect(page.getByText("/tmp/airoute-mimocode-e2e.json")).toBeVisible();
  await expect(page.getByText(/OpenAI 兼容协议/)).toBeVisible();
	await expect(page.getByLabel("默认模型").locator("option")).toHaveText([
		"不设置",
		"fast → 所有兼容协议",
		"mimo-v2.5 → OpenAI Chat",
	]);
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(
    /@ai-sdk\/openai-compatible/,
  );
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(/"fast"/);
  await page
    .getByRole("button", { name: "备份并写入", exact: true })
    .click();
  await expect(page.getByText(/已写入 \/tmp\/airoute-mimocode-e2e\.json/)).toBeVisible();
  await page.getByRole("button", { name: "清理旧配置" }).click();
  await expect(page.getByRole("heading", { name: "清理旧配置？" })).toBeVisible();
  await page.getByRole("button", { name: "备份并清理" }).click();
  await expect(page.getByText(/已清理旧配置/)).toBeVisible();
});
