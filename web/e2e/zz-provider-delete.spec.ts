import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("deleting a provider also removes route references", async ({ page }) => {
  await login(page);

  const providerRow = page.getByRole("row").filter({ hasText: "Mock Provider" });
  await providerRow.getByRole("button", { name: /删\s*除/ }).click();
  await page.getByRole("button", { name: "删除模型服务" }).click();

  await expect(page.getByText("Mock Provider", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "路由配置" }).click();
  await expect(page.getByText("fast", { exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "应用配置" }).click();
  const emptyRouteNotice = page.getByText(
    "请先在“路由配置”中至少创建一条精确模型路由。",
  );
  await expect(emptyRouteNotice).toBeVisible();
  await expect(emptyRouteNotice).toHaveCSS("font-size", "13px");
  await expect(emptyRouteNotice).toHaveCSS("font-weight", "600");
});
