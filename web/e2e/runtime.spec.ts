import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("runtime state is visible in the global header", async ({
  page,
}) => {
  await login(page);
  await expect(page.getByText("运行中", { exact: true })).toBeVisible();
  const version = page.getByTestId("sidebar-version");
  await expect(version).toContainText("v0.2.2");
  await expect(version).toContainText("可更新到v0.2.3");
});
