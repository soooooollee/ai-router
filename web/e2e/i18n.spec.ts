import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("switches the management console between Chinese and English", async ({ page }) => {
  await login(page);
  await page.getByLabel("语言").selectOption("en-US");
  await expect(page.getByRole("button", { name: "Overview" })).toBeVisible();
  for (const section of ["Overview", "Models", "Routes", "Applications", "Request Logs", "Settings"]) {
    await page.getByRole("button", { name: section }).click();
    await page.waitForTimeout(section === "Applications" ? 500 : 150);
    const untranslated = await page.locator("main").evaluate((root) => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      const values: string[] = [];
      let node = walker.nextNode();
      while (node) {
        if (!node.parentElement?.closest("pre,code,textarea") && /[\u3400-\u9fff]/.test(node.nodeValue || "")) values.push((node.nodeValue || "").trim());
        node = walker.nextNode();
      }
      return values.filter(Boolean);
    });
    expect(untranslated, `untranslated text in ${section}`).toEqual([]);
  }
  await page.getByRole("button", { name: "Models" }).click();
  await page.getByRole("button", { name: "Delete" }).first().click();
  await expect(page.locator(".confirm-dialog")).toContainText("If a route still uses this service");
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
