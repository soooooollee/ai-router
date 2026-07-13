# Qwen 3.x 与 Xiaomi MiMo 兼容验证

验证日期：2026-07-12。测试使用临时环境变量，真实密钥未写入仓库、配置文件、日志正文或测试夹具。

## 实现

- `qwen3` Provider Profile，也可通过目标模型名中的 `qwen3` 自动识别。
- `enable_thinking` 与 Canonical `reasoning_enabled` / `reasoning_effort` 双向映射。
- `reasoning_content` 非流式、SSE 及多轮 assistant 历史保留。
- Qwen Tool Call、并行调用和流式 Tool `index`/参数分片。
- Claude Code 多段 Anthropic System Block 合并为 Qwen 要求的单个首位字符串 System Message。
- Provider `reasoning_mode` 策略和 `max_output_tokens` 上限；Claude Code 推荐关闭 Qwen 思考并限制单轮输出预算。
- `xiaomi-mimo` Profile，覆盖 OpenAI Chat 与 Anthropic Messages 两种官方兼容入口。

## 真实服务验证结果

| 链路 | 结果 |
| --- | --- |
| SiliconFlow `Qwen/Qwen3.6-35B-A3B` OpenAI Chat 非流式 | 通过 |
| Qwen `enable_thinking: true` / `reasoning_content` | 通过；256 token 测试全部用于推理并正常报告 `length` |
| Qwen Tool Calling | 通过，函数名、ID 和 JSON 参数完整 |
| Qwen SSE + usage + `[DONE]` | 通过 |
| Anthropic Messages 客户端 → Qwen | 通过 |
| OpenAI Responses 客户端 → Qwen | 通过 |
| Claude Code 文本 → AI Router → Qwen | 通过 |
| Claude Code Bash Tool 两轮 → AI Router → Qwen | 通过，Tool Call、执行结果和最终文本完整 |
| Xiaomi `mimo-v2.5` OpenAI Chat 非流式/流式 | 通过 |
| OpenAI Chat 客户端 → Xiaomi Anthropic 上游 | 通过 |

硅基流动测试期间曾出现一次直连 SSE 120 秒无首字节的瞬时超时；随后相同模型经 AI Router 多次流式与非流式请求均成功。Claude Code 的大型 Tool Prompt 在未限制策略时也曾出现约 207 秒首轮延迟；配置 `reasoning_mode: disabled` 与 `max_output_tokens: 1024` 后完整 Tool 往返约 5 秒完成。

## 推荐配置

见 [`examples/airoute.qwen3.yaml`](../examples/airoute.qwen3.yaml)。其中：

```yaml
profile: qwen3
reasoning_mode: disabled
max_output_tokens: 1024
```

需要深度推理时，可建立第二个指向相同服务的 Provider，设置 `reasoning_mode: enabled`，并通过独立模型别名路由，避免普通 Agent Tool 请求承担不必要的思考延迟。

## 规范依据

- [Qwen3 官方说明](https://qwenlm.github.io/blog/qwen3/)
- [Qwen Agent 配置与 Thinking/Tool Calling](https://qwenlm.github.io/Qwen-Agent/en/guide/get_started/configuration/)
- [Xiaomi MiMo Anthropic API Compatibility](https://mimo.mi.com/docs/en-US/api/chat/anthropic-api)
- [Xiaomi MiMo API 接入 FAQ](https://mimo.mi.com/docs/zh-CN/quick-start/faq/api-integration)
