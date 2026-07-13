# 协议转换

## 端点

```text
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent
GET  /v1/models
```

`count_tokens` 返回本地估算值并带 `estimated: true`，不会产生上游费用。

## Canonical IR

入站 Adapter 将 system/instructions、消息、内容块、Tools、采样参数和响应格式归一。内容块覆盖 text、图片 URL/Base64、Tool Call/Result、Reasoning、Refusal 以及实验性的 Document。出站 Adapter 只依赖该 IR。

流式响应统一为完整生命周期：`response.start`、`message.start`、`content.start`、文本/推理/Tool 参数增量、`content.end`、`usage.update`、`message.end`、`response.end`。上游遗漏开始或结束事件时，流归一器会在编码客户端协议前补齐。非流式响应也由同一事件序列聚合，避免两套语义。

## 兼容性策略

- `strict`：出现有损转换即返回 422。
- `warn`：尽可能转换，并在 `x-airoute-diagnostic` 和请求日志中记录，默认值。
- `drop`：允许丢弃，仍保留日志诊断。

同协议请求走 Native Fast Path，只重写模型并清理鉴权/Header，保留 Provider 原生字段。离线同协议转换也会按来源协议恢复未知顶层扩展；跨协议会明确诊断并丢弃。

## 能力

四个 Adapter 覆盖文本、多轮消息、图片 URL/Base64、Tool 定义、Tool Choice、Tool Call/Result、并行 Tool、Reasoning、JSON Schema 输出、usage、stop reason、错误及 SSE。目标协议没有等价字段时会近似表达或产生诊断，不静默伪装为无损转换。

## 不透明推理状态

推理摘要与供应商的不透明推理状态是两件事。AI Router 会在同协议往返时保留下列字段：

- Anthropic `thinking.signature`、`redacted_thinking` 和流式 `signature_delta`。
- Gemini Part 上的 `thoughtSignature`；跨协议注入的 Function Call 使用 Gemini 官方允许的 `skip_thought_signature_validator` 哨兵。
- OpenAI Responses reasoning item 的 `id` 与 `encrypted_content`。

这些字段不会被解释、改写或错误地移植到其他供应商。相关规范见 [OpenAI Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)、[Anthropic Extended Thinking](https://platform.claude.com/docs/en/build-with-claude/extended-thinking) 和 [Gemini Thought Signatures](https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures)。

## Qwen 3.x 与 Xiaomi MiMo

Qwen 3.x 通过 OpenAI Chat 兼容端点接入。AI Router 支持非流式/流式 `reasoning_content`、`enable_thinking`、并行 Tool Call 以及带真实 `index` 的 Tool 参数分片。思考模式下多轮 Tool Calling 会把历史 assistant `reasoning_content` 原样带回上游。

Xiaomi MiMo 同时支持 OpenAI Chat 与 Anthropic Messages Profile。官方 Base URL 分别为 `https://api.xiaomimimo.com/v1` 和 `https://api.xiaomimimo.com/anthropic`；不要把 API Key 拼接到 URL。
