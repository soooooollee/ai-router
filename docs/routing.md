# 路由与可靠性

规则按优先级、匹配具体程度和声明顺序稳定排序。支持模型 glob、入站协议、流式、Tools、图片和 Header 条件。

```yaml
routes:
  - id: coding
    priority: 100
    match:
      model: coding-*
      tools: true
    targets:
      - provider: anthropic
        model: claude-sonnet-4-5
      - provider: openai
        model: gpt-5
```

`targets` 按顺序执行。网络错误或配置在 `retry.on_status` 中的状态会重试；当前目标耗尽后按独立的 `fallback` 策略决定是否进入下一个目标。默认只允许网络错误、超时、429 和 5xx；400、鉴权、权限与安全拒绝默认不 Fallback。可通过 `fallback.on_network_error`、`on_timeout`、`on_status` 和 `on_error_codes` 精确调整。已经向客户端发送有效流式内容后不会切换 Provider。

退避采用指数增长、随机 jitter，并尊重最长一分钟的 `Retry-After`。所有尝试受 Provider Timeout 和客户端取消控制。

管理 API 支持两种 Route Explain：`GET /api/routes/explain?protocol=openai-chat&model=fast&tools=true` 使用请求特征，`POST /api/routes/explain` 接收完整协议请求。两者都返回排序解释、最终目标、Canonical Request 与兼容性诊断。
