# 配置

AI Router 只使用一份版本化 YAML 配置。Web 控制台读取和原子写回同一个文件；保存前会校验、备份，失败时继续使用旧的不可变配置快照。

## 环境变量

```yaml
api_key: ${OPENAI_API_KEY}
token: ${AIROUTE_ADMIN_TOKEN}
```

缺少无默认值的环境变量时拒绝启动。管理 API 不返回解析后的值，Web 保存时仍保留原始表达式。

`admin.token`、客户端 Key、Provider `api_key` 以及名称中带 authorization/key/token/cookie/secret 的敏感 Header 必须使用环境变量引用；为避免 Web 编辑器和备份泄密，配置加载器会拒绝这些字段中的明文密钥。

## Server

- `listen`：网关地址。
- `admin_listen`：控制台地址，默认回环。
- `request_timeout`：单次请求总期限。
- `max_body_size`：Body 上限，支持 `MiB` 等单位。
- `max_concurrent`：最大并发请求数。
- `max_headers`：请求 Header 个数上限，默认 100。
- `max_header_bytes`：HTTP Header 上限。

## Provider

每个 Provider 必须声明唯一 `id`、协议、Base URL、模型和凭据。四种协议标识为：

```text
openai-chat
openai-responses
anthropic-messages
gemini-generate-content
```

`profile` 用于隔离供应商/模型族的线级差异，当前支持 `generic`、`qwen3` 和 `xiaomi-mimo`。Qwen 3.x 模型名也会自动识别；显式配置更适合动态模型 Provider。`reasoning_mode` 可设为 `auto`、`enabled` 或 `disabled`；Claude Code 搭配 Qwen 时推荐 `disabled`，避免大型 System Prompt 触发很长的思考阶段。`max_output_tokens` 可为 Provider 设置安全上限，适合把 Agent 客户端默认的超大生成预算限制到该服务的实用范围。

Qwen 3.x Profile 会把统一的 `reasoning_enabled` 或 `reasoning_effort` 映射为 `enable_thinking`，并保留响应及后续 Tool Calling 历史里的 `reasoning_content`。Xiaomi MiMo Profile 会按其兼容接口使用 `thinking: {type: disabled}`。完整示例见 [`examples/airoute.qwen3.yaml`](../examples/airoute.qwen3.yaml)。

上游地址默认禁止解析到 Loopback、私网、Link-local 或 Unspecified IP。只对完全可信的本地服务设置：

```yaml
allow_private_url: true
```

`headers` 只合并允许转发的 Header。`request_fields` 可声明 Provider 所需的顶层缺省字段；客户端已经提供的字段优先，不会被覆盖：

```yaml
request_fields:
  store: false
  service_tier: flex
```

未知供应商字段会进入 IR 的 `extensions`。同协议往返会恢复这些字段；跨协议时不会错误移植，并产生 `request_extensions_not_portable` 诊断。

## 热加载

服务每两秒检测文件内容 Hash，也响应 SIGHUP。只有完整解析和语义校验通过后才替换运行快照；进行中的请求不切换快照。

## Schema

编辑器可使用 [`schemas/config.v1.schema.json`](../schemas/config.v1.schema.json) 获得字段提示。服务启动、热加载、Web 校验和保存都实际执行这份内嵌的 Draft 2020 Schema，然后检查重复 ID、重复 Fallback 目标、不可达规则、无效引用、Provider URL、动态模型声明、管理面公网鉴权和空路由等跨字段语义。

## 指标标签

Prometheus 指标默认按入站协议、Provider 和状态分组。只有模型集合受控时才开启 `metrics.model_labels: true`；默认关闭模型标签，避免动态模型名造成高基数。
