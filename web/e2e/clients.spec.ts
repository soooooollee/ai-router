import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("generates, manages, and selects access keys for applications", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await login(page);
  await page.getByRole("button", { name: "访问密钥", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "访问密钥", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("旧版静态密钥", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "生成密钥" }).click();
  await page.getByLabel("密钥名称").fill("E2E Codex");
  await page.getByLabel("说明").fill("Browser test client");
  await page.getByLabel("允许模型").fill("fast");
  await page.getByLabel("每分钟请求").fill("30");
  await page.getByLabel("最大并发").fill("2");
  await page.getByRole("button", { name: "生成 Key" }).click();

  await expect(
    page.getByRole("heading", { name: "访问密钥已生成" }),
  ).toBeVisible();
  const secret = page.locator(".secret-copy-row code");
  await expect(secret).toContainText("sk-");
  const completeKey = await secret.textContent();
  await page.getByRole("button", { name: "复制", exact: true }).click();
  await expect(page.getByRole("button", { name: "已复制" })).toBeVisible();
  await page.getByRole("button", { name: "我已保存，关闭" }).click();
  await expect(page.getByText("E2E Codex", { exact: true })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(completeKey || "missing-key");
  const browserStorage = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(browserStorage).not.toContain(completeKey || "missing-key");
  await page.reload();
  await expect(page.locator("body")).not.toContainText(completeKey || "missing-key");

  const row = page.getByRole("row", { name: /E2E Codex/ }).first();
  await row.getByRole("button", { name: /权\s*限/ }).click();
  await expect(page.getByRole("heading", { name: "编辑访问权限" })).toBeVisible();
  await page.getByLabel("每日请求").fill("100");
  await page.getByRole("button", { name: "保存权限" }).click();
  await expect(page.getByRole("heading", { name: "编辑访问权限" })).toHaveCount(0);

  await expect(row.getByRole("button", { name: /轮\s*换/ })).toBeVisible();
  await row.getByRole("button", { name: /轮\s*换/ }).click();
  await expect(page.getByRole("heading", { name: "轮换访问密钥" })).toBeVisible();
  await page.getByRole("button", { name: "生成新 Key" }).click();
  await expect(page.getByRole("heading", { name: "访问密钥已生成" })).toBeVisible();
  await expect(page.locator(".secret-copy-row code")).toContainText("sk-");
  await page.getByRole("button", { name: "我已保存，关闭" }).click();

  const latestRow = page.getByRole("row", { name: /E2E Codex/ }).last();
  await latestRow.getByRole("button", { name: /停\s*用/ }).click();
  await expect(latestRow.getByRole("button", { name: /启\s*用/ })).toBeVisible();
  await latestRow.getByRole("button", { name: /启\s*用/ }).click();
  await expect(latestRow.getByRole("button", { name: /停\s*用/ })).toBeVisible();

  await page.getByRole("button", { name: "应用配置" }).click();
  const keyField = page.getByLabel("AI Router 访问密钥");
  await expect(keyField).toHaveCount(1);
  expect(await keyField.locator("option").count()).toBeGreaterThanOrEqual(2);
  await keyField.selectOption((await keyField.locator("option").last().getAttribute("value")) || "");
  await expect(page.locator(".application-client-credential")).toHaveCount(0);
  await expect(page.getByLabel("编辑合并后配置")).toHaveValue(/••••••••/);
  await expect(page.getByLabel("编辑合并后配置")).not.toHaveValue(new RegExp(completeKey || "missing-key"));
  await page.getByRole("button", { name: "备份并写入", exact: true }).click();
  await expect(page.getByText(/已写入 .*airoute-claude-e2e\.json/)).toBeVisible();

  await page.getByRole("button", { name: "访问密钥", exact: true }).click();

  await page.getByRole("button", { name: /迁\s*移/ }).click();
  await expect(page.getByText("旧密钥已迁移", { exact: true })).toBeVisible();
  await expect(page.locator(".legacy-key-panel")).toHaveCount(0);

	await latestRow.getByRole("button", { name: /撤\s*销/ }).click();
	await page.getByRole("button", { name: "确认撤销" }).click();
	await expect(latestRow.getByRole("button", { name: /删\s*除/ })).toBeVisible();
	await latestRow.getByRole("button", { name: /删\s*除/ }).click();
	await expect(page.getByRole("heading", { name: "删除访问密钥？" })).toBeVisible();
	await page.getByRole("button", { name: "确认删除" }).click();
	await expect(page.getByRole("row", { name: /E2E Codex/ })).toHaveCount(1);
});
