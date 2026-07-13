import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("runtime can pause and clearly reports transient state", async ({
  page,
}) => {
  await login(page);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: /运行中.*重启后恢复/ }).click();
  await expect(
    page.getByRole("button", { name: /已关闭.*点击启动/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: /已关闭.*点击启动/ }).click();
  await expect(
    page.getByRole("button", { name: /运行中.*重启后恢复/ }),
  ).toBeVisible();
});
