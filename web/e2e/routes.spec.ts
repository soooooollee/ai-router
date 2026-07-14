import { expect, test } from "@playwright/test";
import { login } from "./helpers";

test("route creation shows the protocol-specific address", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: "路由配置" }).click();
  await page.getByRole("button", { name: "+ 添加路由" }).click();
  await expect(page.getByLabel("选择接入模型")).toHaveValue("mock:mock-model");
  await expect(page.getByLabel("客户端模型名")).toHaveValue("mock-model");
  await expect(page.getByLabel("路由 ID")).toHaveValue(
    /^mock-model-anthropic-messages-\d{17}$/,
  );
  await page.getByLabel("选择接入模型").selectOption("mock:secondary-upstream");
  await expect(page.getByLabel("客户端模型名")).toHaveValue(
    "secondary-upstream",
  );
  await expect(page.getByLabel("路由 ID")).toHaveValue(
    /^secondary-upstream-anthropic-messages-\d{17}$/,
  );
  await page.getByLabel("客户端调用协议").selectOption("openai-chat");
  await expect(page.getByLabel("路由 ID")).toHaveValue(
    /^secondary-upstream-openai-chat-\d{17}$/,
  );
	await page.getByText("高级匹配设置").click();
	await page.getByLabel("路由 ID").fill("manual-route-id");
	await page.getByLabel("客户端调用协议").selectOption("openai-responses");
	await expect(page.getByLabel("路由 ID")).toHaveValue("manual-route-id");
	await page.getByLabel("客户端调用协议").selectOption("openai-chat");
  await expect(
	page.locator(".generated-route-address code"),
  ).toBeVisible();
	await expect(page.locator(".generated-route-address code")).toHaveText(
		"http://127.0.0.1:18080/v1/chat/completions",
	);
  await page.getByRole("button", { name: "保存路由" }).click();
  await expect(
    page.getByText("secondary-upstream", { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText(/config schema:|jsonschema:/i)).toHaveCount(0);
});
