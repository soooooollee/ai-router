# 配置

AI Router 只使用一份版本化 YAML 配置。Web 控制台读取和原子写回同一个文件；保存前会校验、备份，失败时继续使用旧的不可变配置快照。

## 本地密钥

```yaml
api_key: sk-local-example
```

Provider `api_key` 直接保存在本机配置中。模型接入页和系统设置页会显示实际值，便于单机使用和维护；配置文件与备份强制使用 `0600` 权限，仍应视为敏感文件。旧版 `${ENV_NAME}` 写法可继续读取，但控制台会展示解析后的实际值，保存配置后会转为明文值。管理 Token、客户端访问 Key，以及名称中带 authorization/key/token/cookie/secret 的敏感 Header 仍按各自的安全规则处理。

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

## 配置生效方式

服务每两秒检测文件内容 Hash，也响应 SIGHUP。只有完整解析和语义校验通过后才替换运行快照；进行中的请求不切换快照。Web 保存响应会把变更分为三类：

- `hot_reloaded`：Provider、Route、Retry/Fallback、转换策略、鉴权、指标、Provider 超时、日志级别和历史配置等立即生效。
- `runtime_rebuilt`：`server.max_concurrent` 会在线替换并发控制对象。
- `restart_required`：监听地址、Header 读取器进程参数、管理面开关和日志格式已保存，但重启后生效。

运行中开关只控制当前进程，不写入配置；关闭后网关返回 503，进程重启会恢复运行。控制台会明确显示“重启后恢复”。

主配置与应用配置都使用同一原子写入流程：创建唯一备份、写临时文件、`fsync`、原子 Rename、重新读取校验，并保留最近 10 份应用备份。

## Schema

编辑器可使用 [`schemas/config.v1.schema.json`](../schemas/config.v1.schema.json) 获得字段提示。服务启动、热加载、Web 校验和保存都实际执行这份内嵌的 Draft 2020 Schema，然后检查重复 ID、重复 Fallback 目标、不可达规则、无效引用、Provider URL、动态模型声明、管理面公网鉴权和空路由等跨字段语义。

## 指标标签

Prometheus 指标默认按入站协议、Provider 和状态分组。只有模型集合受控时才开启 `metrics.model_labels: true`；默认关闭模型标签，避免动态模型名造成高基数。
