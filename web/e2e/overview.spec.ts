import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("overview presents configuration and classic runtime summaries", async ({
  page,
}) => {
  await login(page);
  await page.getByRole("button", { name: "运行概览" }).click();

  await expect(page.getByRole("heading", { name: "运行概览" })).toBeVisible();
  await expect(page.getByText(/模型、路由、应用、日志/)).toBeVisible();
  await expect(page.getByText("模型数量", { exact: true })).toBeVisible();
  await expect(page.getByText("路由数量", { exact: true })).toBeVisible();
  await expect(page.getByText("已配置应用", { exact: true })).toBeVisible();
  await expect(page.getByText("日志数量", { exact: true })).toBeVisible();
  await expect(page.getByText("日志持久化", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "配置概览" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "请求与性能" })).toHaveCount(0);
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
