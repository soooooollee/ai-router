import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("overview presents the classic summary with compact styling", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("button", { name: "运行概览" }).click();

  await expect(page.getByRole("heading", { name: "运行概览" })).toBeVisible();
  await expect(page.getByText(/实时请求、Token 消耗和链路性能/)).toBeVisible();
  await expect(page.getByText("累计请求", { exact: true })).toBeVisible();
  await expect(page.getByText("成功率", { exact: true })).toBeVisible();
  await expect(page.getByText("P95 延迟", { exact: true })).toBeVisible();
  await expect(page.getByText("Token 总消耗", { exact: true })).toBeVisible();
  await expect(page.getByText("当前并发", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Token 消耗" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "链路性能" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "运行事件" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "请求趋势" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Provider 健康" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "流量去向" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "最近异常" })).toHaveCount(0);
});
