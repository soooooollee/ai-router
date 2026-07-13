import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("runtime state is visible in the global header", async ({
  page,
}) => {
  await login(page);
  await expect(page.getByText("运行中", { exact: true })).toBeVisible();
});
