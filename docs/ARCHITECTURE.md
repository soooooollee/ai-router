# AI Router 架构说明

本文描述 AI Router（`github.com/soooooollee/ai-router`，命令 `air`）的整体结构、数据流、模块边界与已知问题，供新人和改动前的定位使用。协议字段级的细节以代码和 `schemas/config.v1.schema.json` 为准。

## 1. 它是什么

一个**本地 AI 协议转换网关 + 客户端配置托管器**，单个 Go 二进制，内嵌 React 控制台。

- **数据面**（默认 `127.0.0.1:12666`）：接收 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini Generate Content 四种客户端协议，转换成上游 provider 所需的协议后转发（4×4 任意组合）。
- **控制面**（默认 `127.0.0.1:12667`）：Web 控制台与管理 API，负责 provider/route/密钥/日志管理，并直接改写本地 AI 工具的配置文件，让它们指向本网关。
- **CLI**（`air`）：初始化、前后台运行、配置校验、连通性探测、协议转换、客户端状态备份恢复。

术语：

| 术语 | 含义 |
|---|---|
| 客户端协议 | 请求打到网关时使用的协议，由 URL 路径决定 |
| 上游协议 | provider 实际接受的协议，由 `providers[].protocol` 决定 |
| IR | 协议无关的中间表示，`internal/protocol/ir` |
| target | 路由解析出的一个「provider + model」投递目标 |
| 诊断（diagnostic） | 转换过程中的信息损失记录，带 `severity/code/path/action` |

## 2. 进程与拓扑

`air serve` 在同一个进程里起两个 `http.Server`（`cmd/airoute/main.go` 的 `serve`）：

```
客户端 ──► gateway (:12666) ──► 上游 provider
              │  /healthz /readyz /metrics 免鉴权、不占并发
              └─ 并发信号量、鉴权、限流、配额、重试、观测

浏览器 ──► admin (:12667) ──► 同一进程内的内存态（Config Store / Logs / Metrics）
              └─ 静态资源来自 //go:embed webdist/*
```

两个 server 共享同一个 `config.Store` 快照、`observe.Store` 请求历史与 `observe.Metrics`。后台运行时状态（pid、日志路径、端口）写在 `runtime.json`，不在仓库里。

## 3. 目录结构

| 路径 | 职责 | 大致规模 |
|---|---|---|
| `cmd/airoute/` | CLI 18 个子命令、后台进程管理、跨平台 runtime、`client-state` | 1.4k |
| `internal/gateway/` | 数据面入口：鉴权/限流/并发、路由、重试与 fallback、流式转发、观测。按职责分 `gateway.go`（入口与 handle）、`upstream.go`（上游交互与重试）、`stream.go`（SSE 转发）、`diagnostics.go`（strict 与纯辅助） | 1.4k |
| `internal/admin/` | 控制面：约 60 个 API、配置读写回滚、provider 探测、版本检查、内嵌前端。按主题分 `admin.go`（入口与鉴权）、`admin_config.go`、`admin_applications.go`、`admin_misc.go`、`admin_providers.go`、`clients.go` + `credentials.go` + `deployments.go`、`provider_probe{,_codex,_transport}.go` | 4.6k |
| `internal/application/` | 受管客户端适配器：`claudecode` `claudeapp` `codex` `mimocode` `pi`；`codex` 分 `adapter.go` / `config.go` / `catalog.go` / `verify.go`，`pi` 分 `adapter.go` / `config.go` / `catalog.go` | 4.1k |
| `internal/protocol/` | IR + 4 个协议适配器 + conformance 测试 | 2.4k |
| `internal/clientstore/` | bbolt 状态库：`bbolt.go`（打开/迁移/键编码）、`clients.go`（client、credential、policy）、`usage.go`（用量、保留、审计、备份） | 1.5k |
| `internal/clientauth/` | 凭据鉴权、keyring、配额预留与结算 | 0.7k |
| `internal/config/` | 配置模型、校验、热重载 Store、变更影响分类 | 0.75k |
| `internal/provider/` | 上游行为画像（qwen3 / xiaomi-mimo）、OpenCode 特判 | 0.4k |
| `internal/routing/` | 路由解析与解释 | 0.12k |
| `internal/observe/` | 请求历史环形缓冲与指标 | 0.3k |
| `internal/{streaming,ratelimit,tokencount,secure,safefile,auth}/` | SSE 编解码、令牌桶、token 估算、SSRF 防护、原子写、静态 key 兼容 | 各 <0.2k |
| `web/` | Vite + React + TypeScript 控制台，产物写入 `internal/admin/webdist/` | 6k |
| `schemas/` | 配置 JSON Schema（嵌入二进制） | — |

## 4. 数据面：一次请求的完整路径

入口 `Gateway.ServeHTTP`（`internal/gateway/gateway.go`）：

1. **免鉴权端点**：`/healthz`、`/readyz`、`metrics.path`。它们在鉴权之前返回，且不占用并发额度。
2. **协议判定**：`protocolFromPath` 按路径映射适配器——
   `/v1/chat/completions`→OpenAI Chat，`/v1/responses`→OpenAI Responses，
   `/v1/messages`→Anthropic，`*/models/{model}:generateContent|:streamGenerateContent`→Gemini。
   `/v1/models` 与 `/v1/messages/count_tokens` 是特例，不走协议转换。
3. **鉴权**：`clientauth.Manager.Authenticate` 校验 HMAC 凭据，得到 `Principal`（client、credential、policy）。未配置 ClientAccess 时退回静态 key 兼容路径。
4. **预检**：`Precheck` 检查协议白名单、CIDR、每分钟令牌桶。
5. **并发**：全局信号量，容量 `server.max_concurrent`。
6. **请求体**：`readRequestBody`，支持 gzip，上限 `server.max_body_size`。
7. **解码**：`inAdapter.DecodeRequest` 得到 `ir.Request`。
8. **配额预留**：`BeginRequest` 检查模型白名单、最大输出、并发数与每日配额。
9. **路由**：`routing.Resolve` 得到按优先级排序的 target 列表。
10. **逐 target 投递**（见下节），成功后进入响应编码。
11. **观测**：全程填充 `observe.Record`（路由、provider、attempts、诊断、usage），落到请求历史、JSONL、Prometheus 指标，并结算配额。

### 同协议与跨协议

- **同协议**（`clientProtocol == provider.Protocol`）：走 `rewriteNativeRequest`，只替换 `model` 字段后原样透传。**不经 IR**，因此无损，也不会产生转换诊断。
- **跨协议**：`outAdapter.EncodeRequest` 生成上游 payload，再经 `providerprofile.PrepareRequest` 施加 provider 画像改写（可能追加诊断）。

### 重试与故障转移

- **target 内重试**：`retry.max_attempts`，仅当状态码命中 `retry.on_status` 时继续，退避支持 `Retry-After`（秒与 HTTP-date）与指数抖动。
- **target 间 fallback**：由 `fallbackAllowed` 决定，默认 429/500/502/503/504；`context.Canceled` 永不 fallback。
- **流式的提交点是首个事件**：先预读首个事件并试解码，失败仍可换 target；一旦已向客户端发出事件，后续失败只补一条 `error` 事件，不再重试。

### 流式

`streaming.Decoder` → `outAdapter.DecodeStreamEvent` → `ir.StreamNormalizer`（合成生命周期、重编号）→ `inAdapter.EncodeStreamEvent` → `streaming.Write`。首 token 延迟、usage 合并、Gemini 工具参数缓冲都在这一段（`internal/gateway/stream.go`）。

首事件之前失败仍可换 target；**首事件之后失败**一律走 `failStream`：给客户端补一条 `error` 事件，并把该请求记为 502。HTTP 状态码此时已经发出、无法改写，但日志/指标不会再把截断的流算成 200 成功。

客户端侧的编码通过 `protocol.NewStreamEncoder(adapter)` 取得：需要逐响应状态的协议实现 `StreamEncoder`（目前只有 Gemini——它只能接受参数完整的 functionCall，必须把 `arguments.delta` 缓冲到 `content.end`），其余协议用无状态实现。gateway 因此不再包含任何协议特例。

## 5. 协议层与 IR

适配器接口（`internal/protocol/adapter.go`）只有 6 个方法，全部返回诊断切片：

```go
Protocol() ir.Protocol
DecodeRequest/EncodeRequest   (ir.Request)
DecodeResponse/EncodeResponse (ir.Response)
DecodeStreamEvent(ctx, eventName, raw) ([]ir.Event, ...)
EncodeStreamEvent(ctx, ir.Event) ([]SSE, ...)
```

IR 要点：

- `Request{Model, Instructions, Messages, Tools, ToolChoice, ResponseFormat, Sampling, Stream, Metadata, Extensions}`。
- `ContentBlock.Type` 是开放字符串（`text`/`image_url`/`image_base64`/`document`/`tool_call`/`tool_result`/`reasoning`/`refusal`/`extension`）。
- `Extensions` 保存源协议私有字段，仅在**同协议回环**时还原；跨协议一律诊断为 `dropped`。
- `Sampling` 覆盖 `temperature/top_p/top_k/max_output_tokens/stop/reasoning_effort/reasoning_enabled/reasoning_budget_tokens`。
- `EventsFromResponse` / `AggregateEvents` 让非流式复用流式语义，避免两套实现。

诊断约定：

- `Action ∈ {preserved, dropped, approximated, rejected, rewritten}`。
- `common.IsLossy` 判定「有信息损失」：`severity == "error"` 或 `action ∈ {approximated, dropped, rejected}`。
- 各编码器统一调用 `common.RequestPortabilityDiagnostics`，它是采样控制、内容块、extensions 损失的唯一汇总点。

## 6. 路由

`internal/routing/router.go`：

- 排序：`priority` 降序 → `specificity` 降序 → 稳定序；取第一个所有条件都命中的规则，不回溯。
- `match` 维度：`model`（glob）、`protocol`、`stream`、`tools`、`image`、`headers`。
- 无匹配时使用 `default_route`；仍无则 404。
- `air routes --model X --protocol Y` 可输出命中的 target 顺序与解释文本。

注意：`path.Match` 的 `*` 不跨 `/`，带斜杠的模型名需要显式写法。

## 7. 凭据、配额与状态库

- `clientstore`（bbolt，schema v6）：`clients`、`credentials`、`credential_by_hmac`、`policies`、`usage_daily`、`usage_minute`、`usage_reservations`、`audit_events`；键前缀为 `tenant\0project\0id`。
- 凭据：`sk-` + 32 字节随机，库内只存 HMAC-SHA256 与 15 位前缀；明文只在生成响应里出现一次。`managed` 类型额外用 AES-256-GCM 保存密文，可重复下发。
- 主密钥：`credential-master.key`（32 字节，`{version,id,key}`），可用 `AIROUTE_CREDENTIAL_MASTER_KEY` 外置，支持多代轮换。
- 配额：`Precheck`（RPM/burst、协议、CIDR）→ `BeginRequest`（模型、并发、日配额预留）→ `Finish`（释放并发、按真实用量结算）。
- 限流桶：内存令牌桶，`req/s` 级精度；桶数量超过 256 时顺手淘汰「已完整回填且静默 10 分钟以上」的桶，因此长期运行不会按客户端数量无限增长，且淘汰不会改变任何判定。桶仍是单进程内存态，重启清零。
- 鉴权关闭（`auth.enabled: false`）时所有请求都是匿名 principal，**不检查也不消耗上述任何策略与配额**；`air serve` 启动时会打印 warning。
- 用量保留：`usage_minute` 只保留 24 小时，由 `PruneUsage` 清理；写入路径每小时最多触发一次（启动时也会跑一遍）。`usage_daily` 是长周期记录，不清理。
- CLI 侧 `air client-state backup|verify|restore` 校验 manifest、DB 与主密钥的 SHA256，并要求实例已停止。

## 8. 应用配置托管

`internal/application` 的 `Adapter` 接口：`Manifest / Detect / Read / Preview / Apply / Verify / Backups / DeleteBackup / Rollback`，可选 `RawConfigAdapter`、`ConfigurationStatusReader`、`CleanupAdapter`。

| 适配器 | 目标 | 主要文件 |
|---|---|---|
| `claudecode` | Claude Code CLI | `~/.claude/settings.json`（env 六键） |
| `claudeapp` | Claude 桌面 3p | `Claude-3p/claude_desktop_config.json` 等三文件 |
| `codex` | Codex CLI / App | `~/.codex/config.toml` + `airoute-model-catalog.json` |
| `mimocode` | MiMo | `$XDG_CONFIG_HOME/mimocode/mimocode.json` |
| `pi` | Pi | `~/.pi/agent/{models,settings}.json` |

共同约定：只改自己拥有的键（注释块标记或固定 key 前缀），写入前备份，用 `safefile.AtomicWrite`（tmp+fsync+rename+目录 fsync），写后重读校验。

### 凭据部署（`POST /api/clients/{id}/credential-deployments`）

写多个应用时按「先全量预检、再落库、后写入」执行，失败不会留下半成品：

1. **预检**：凭据只在内存中铸造，逐个 `Preview`。任一失败则返回 `ok:false`，**不创建凭据、不写入任何文件**（响应 `credential_created:false`，不返回 secret）。
2. **落库**：全部预检通过后才写入客户端状态库。
3. **写入**：逐个 `Apply` + 可选 `Verify`；一旦某个目标失败，把**本次请求已写入**的目标按逆序 `Rollback`。
4. **收尾**：回滚全部成功时释放该凭据（revoke→delete），响应 `credential_released:true` 且不返回 secret；有回滚失败时保留凭据并返回 secret，`rolled_back:false`，由用户决定是否手工恢复。
5. **旧凭据**：只有在全部目标写入并验证成功后才按 `revoke_previous` 撤销；失败时旧密钥保持可用。

## 9. 配置与热重载

- 路径优先级：`--config` > `AIROUTE_CONFIG` > `runtimePaths()` 下的 `airoute.yaml` > 迁移当前目录的 legacy 文件。
- 校验四层：环境变量引用要求（敏感字段必须是 `${ENV}`）、嵌入的 JSON Schema（`additionalProperties:false`）、`KnownFields(true)`、语义校验（端口冲突、id 唯一、target 引用存在、公网监听需 ≥24 位 admin token），错误用 `errors.Join` 一次返回。
- 热重载：2 秒轮询文件 hash；加载失败保留旧快照并记录错误；SIGHUP 也可触发。
- `config.CompareEffects` 把字段变更分为 `hot_reloaded` / `runtime_rebuilt` / `restart_required` 三类，供控制台提示。
- 写入链路（`PUT /api/config`）：`expected_hash` 乐观锁 → 脱敏掩码回填 → 临时文件 → 重新校验 → 备份 `.bak` → 原子替换 → 失败自动回滚 → 应用运行时 → 保留最近 10 份备份。

## 10. 控制面与前端

- 鉴权：`admin.token`（Bearer 或 `x-airoute-admin-token`，常量时间比较）；存在 ClientStore 时非 loopback 一律 403；校验 `Host` 与 `Origin` 防 DNS rebinding / CSRF；失败限速。
- 端点按主题分组：状态与诊断、配置（读/校验/写/重载/备份/回滚）、providers（列表/探测/连通性）、routes（解释）、playground、applications（预览/写入/校验/备份/回滚）、clients 与 credentials、logs、metrics。
- **密钥永远脱敏**：`GET /api/config` 与 `GET /api/providers` 返回的 `admin.token`、`auth.keys[].value`、`providers[].api_key` 一律是 `••••••••`。保存时若 YAML 里仍是掩码，服务端从磁盘原文按 id/索引回填；若某个掩码找不到原值（例如在原始编辑器里新增的 provider），直接返回 422 `unrestorable_secret`，绝不把掩码写进配置文件。`logging.web_redaction` 只额外控制请求/响应体的脱敏。
- 前端：无路由库，`location.hash` 手写路由；7 个页面全部懒加载并在启动后预热；`App.tsx` 持有 `status/config/yaml/hash`，`config_version` 与本地 hash 不一致时自动重载；写配置走 `configMutation.ts`（YAML 解析 + `expected_hash` + 409 重试一次）。
- i18n：`zh-CN` / `en-US`，采用「中文原文 → 英文」的字典反查 + DOM 文本改写。改动文案时同步 `i18n.ts`。

## 11. 构建、测试与发布

- `make build`：先 `cd web && npm test && npm run build`（产物写 `internal/admin/webdist/`），再 `go build -o bin/air`。前端产物**入仓**，改动 `web/` 必须同 commit 提交重建产物。
- `make check`：gofmt + `go vet` + `go test -race`。
- 协议契约用 golden 测试固定：`tests/fixtures/golden/<protocol>.<request|response|stream>.json` 记录往返输出**和**诊断列表，任何字段丢失或诊断变化都会失败。改动协议行为后用 `go test ./internal/protocol/ -run TestCheckedInProtocolFixtures -update` 重建，并审查 diff。唯一的豁免字段是编码器取墙钟的 `created`。
- CI：vitest、Playwright e2e（拉起真实 Go 进程 + mock provider）、`go test -race`、`scripts/test-lifecycle.sh`、govulncheck、go-licenses、npm audit、license 白名单、三平台 smoke、Docker 构建。
- 版本注入：`-X main.version/commit/builtAt`（goreleaser 与 Dockerfile 注入；Makefile 本地产物为 `dev`）。
- 分发：goreleaser 产 Linux/macOS/Windows × amd64/arm64，附 SBOM 与 cosign 签名；npm 包 `airoute-cli` 在 postinstall 下载对应资产并校验 sha256；另有 install.sh 与 Homebrew tap。

## 12. 已知问题与待办

### 已修复

- `conversion.unsupported_fields: strict` 的判定原先分散在两处且不等价，另有一段依赖 `severity == "error"` 的死分支（无任何适配器会产生该 severity）。现已统一为 `strictConversionLoss`，并且 422 响应会点名被丢弃的字段，例如
  `request requires a lossy protocol conversion: unsupported_request_field (seed) [dropped]`。
- `sampling.top_k`、`sampling.reasoning_effort`、`sampling.reasoning_budget_tokens` 跨协议丢弃原先没有任何诊断，strict 模式也拦不住；现由 `common.SamplingPortabilityDiagnostics` 统一报告。
- Anthropic 的 `thinking.budget_tokens` 原先在 IR 中没有表达位、往返即丢失；现在 `SamplingOptions.ReasoningBudget` 承载，并在编码时保证 `budget_tokens < max_tokens`。
- 批量部署凭据原先先建凭据再预览、失败不回滚，会留下孤儿凭据和指向新密钥却没写完的应用。现在预检全部通过才落库，失败按逆序回滚并释放凭据，且只有全部成功才会撤销旧密钥。
- `usage_minute` 原先没有保留期限，分钟桶无限增长。现在保留 24 小时，写入路径每小时最多触发一次清理。
- 巨型文件：`gateway.go`(1393)、`admin.go`(1500)、`provider_probe.go`(1750)、`codex/adapter.go`(1161)、`admin/clients.go`(1088)、`clientstore/bbolt.go`(1058)、`pi/adapter.go`(922) 均已按职责拆分，全仓源码单文件最大 888 行。拆分只移动声明，逐字节保持函数体与注释不变（每个包都做过逐块字节比对 + 函数清单核对）。
- 流式失败原先静默截断且被记成 200；现在补 `error` 事件并记 502（见 §4）。
- 跨协议流式契约已纳入 golden：16 个 source→target 组合各有一份 `cross/*.stream.json`。为此把 Gemini 的工具参数缓冲从 gateway 移入 `gemini` 适配器（`protocol.StreamEncoder`），gateway 不再有协议特例。
- 源协议不报 `total_tokens` 时（Anthropic 从不报），编码端改用输入+输出推导，避免客户端收到 `total_tokens: 0`。
- 限流桶无淘汰；现在超过 256 个桶时淘汰已完整回填的静默桶（见 §7）。
- `GET /api/config` 明文返回密钥；现在无条件脱敏，且拒绝无法回填的掩码（见 §10）。
- 升级检查指向分叉上游；现在可用 `AIROUTE_RELEASE_REPO` 指定发布仓库。
- `go.mod` 与 CI 的 Go 版本口径已在 `go.mod` 注释、CONTRIBUTING 与本文中说明：`go` 指令是最低语言版本，CI/发行构建固定较新补丁版。

### 未修复（已知设计取舍）

| 问题 | 位置 | 影响 |
|---|---|---|
| 限流与配额是单进程内存态 | `internal/ratelimit/limiter.go` | 重启清零、多实例之间不共享；水平扩容需要在入口层再做限流 |
| `gateway.go` 仍有 888 行 | `internal/gateway/gateway.go` | 入口 + `handle` 主流程内聚；再拆会切断请求生命周期的可读性，暂不继续 |

## 13. 常见排障

```bash
air check                 # 配置是否合法
air doctor                # 端口占用、配置可读性
air routes --model X      # 路由解释
air probe --provider ID   # 上游连通性与能力探测
air status                # 运行中实例状态
air logs -f               # 后台日志
curl -s localhost:12666/healthz
curl -s localhost:12667/api/status | jq
```

请求级排障看响应头 `x-airoute-request-id`，再用 `/api/logs/{id}` 查明细；`x-airoute-diagnostic` 会列出本次转换的损失类型。
