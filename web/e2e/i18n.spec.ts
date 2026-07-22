import { expect, test } from "@playwright/test";
import { login } from "./helpers";

async function visibleChineseText(page: import("@playwright/test").Page, selector: string) {
  return page.locator(selector).evaluate((root) => {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const values: string[] = [];
    let node = walker.nextNode();
    while (node) {
      const element = node.parentElement;
      if (
        element &&
        !element.closest("pre,code,textarea") &&
        element.getClientRects().length > 0 &&
        /[\u3400-\u9fff]/.test(node.nodeValue || "")
      ) {
        values.push((node.nodeValue || "").trim());
      }
      node = walker.nextNode();
    }
    return values.filter(Boolean);
  });
}

test("switches the management console between Chinese and English", async ({ page }) => {
  await login(page);
  await page.getByLabel("语言").selectOption("en-US");
  await expect(page.getByRole("button", { name: "Overview" })).toBeVisible();
  for (const section of ["Overview", "Models", "Routes", "Applications", "Request Logs", "Settings"]) {
    await page.getByRole("button", { name: section }).click();
    await page.waitForTimeout(section === "Applications" ? 500 : 150);
    const untranslated = await visibleChineseText(page, "main");
    expect(untranslated, `untranslated text in ${section}`).toEqual([]);
  }
  await page.getByRole("button", { name: "Applications" }).click();
  await page.getByRole("tab", { name: "Codex CLI / ChatGPT App" }).click();
  await expect(page.getByLabel("Default model")).toBeVisible();
  await expect.poll(() => visibleChineseText(page, "main")).toEqual([]);
  await page.getByRole("button", { name: "Verify connection" }).click();
  await expect(page.getByText("Verification result", { exact: true })).toBeVisible();
  await expect.poll(() => visibleChineseText(page, "main")).toEqual([]);
  await page.getByLabel("Language").selectOption("zh-CN");
  await expect(page.getByText("正在使用 AI Router 兼容转换", { exact: true })).toBeVisible();
  await page.getByLabel("语言").selectOption("en-US");
  await expect(page.getByText("Using AI Router compatibility conversion", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Models" }).click();
  await page.getByRole("button", { name: "Edit" }).first().click();
  await page.getByRole("button", { name: "Test connection and detect protocol" }).click();
  await expect(page.getByText("API connection and response schema verified", { exact: true })).toBeVisible();
  await expect.poll(() => visibleChineseText(page, ".modal")).toEqual([]);
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Delete" }).first().click();
  await expect(page.locator(".confirm-dialog")).toContainText("routes left without targets will also be removed");
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Routes" }).click();
  await page.getByRole("button", { name: "Delete" }).first().click();
  await expect(page.locator(".confirm-dialog")).toContainText("Requests using this model alias");
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Models" }).click();
  await page.getByRole("button", { name: "+ Add model" }).click();
  await expect(page.locator(".modal b").filter({ hasText: "Add model service" }).first()).toBeVisible();
  await expect(page.locator(".modal")).not.toContainText(/[\u3400-\u9fff]/);
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Routes" }).click();
  await page.getByRole("button", { name: "+ Add route" }).click();
  await expect(page.getByText("Create route", { exact: true })).toBeVisible();
  await expect(page.locator(".modal")).not.toContainText(/[\u3400-\u9fff]/);
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.getByRole("button", { name: "Settings" }).click();
  await page.getByRole("tab", { name: "Logging" }).click();
  await expect(page.getByText(/Logs are (currently kept|written)/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible();
  await expect(page.getByRole("tab", { name: "Web redaction" })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("button", { name: "Settings" })).toBeVisible();
  await page.getByLabel("Language").selectOption("zh-CN");
  await expect(page.getByRole("button", { name: "系统设置" })).toBeVisible();
});
