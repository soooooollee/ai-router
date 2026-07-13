import { expect, type Page } from "@playwright/test";

export async function login(page: Page) {
  await page.goto("/");
  const heading = page.getByRole("heading", { name: "连接 AI Router" });
  if (await heading.isVisible()) {
    await page.getByLabel("管理令牌").fill("e2e-admin-token-123456789012345");
    await page.getByRole("button", { name: "连接" }).click();
  }
  await expect(
    page.getByRole("heading", { name: "模型接入", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Mock Provider", { exact: true })).toBeVisible();
}
