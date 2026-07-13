# 构建计划验收审计

本文件把 `AI_ROUTER_BUILD_PLAN.md` 的最终验收项映射到可复现证据。代码完成度与正式发布前的外部环境认证分开记录。

## 产品与交付

- 单个 Go 二进制同时包含网关、CLI 和 React Web；不依赖桌面壳、数据库、Redis 或独立前端服务。
- YAML v1 是唯一事实来源；Web 对同一文件执行 Schema/语义校验、冲突检测、原子保存、热加载、备份与回滚。
- Docker 非 root 镜像、Compose、macOS/Linux/Windows amd64/arm64 构建、GoReleaser、Checksum、SBOM、签名工作流和 Changelog 均已提供。
- README、配置、协议、路由、安全、迁移、OpenAPI 与验证文档齐全。

## 协议与 IR

- OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini Generate Content 均可作为入站和出站。
- 16 个方向均覆盖请求、响应、文本流、429、非法 JSON、超时与流中断。
- Canonical IR 覆盖文本、图片 URL/Base64、Tool 定义/选择/调用/结果、并行 Tool、参数分片、Reasoning、Refusal、实验性 Document、结构化输出、usage 与 stop reason。
- 流式事件经统一生命周期归一；非流式响应由同一事件序列聚合。
- 同协议原生字段和不透明推理状态尽量保留；跨协议有损行为产生稳定 Diagnostic，`strict` 可拒绝。
- 首个有效流事件前失败允许 Fallback；提交流事件后禁止切换 Provider。

## 路由、配置与可靠性

- 模型别名、精确/glob、协议、Header、Stream、Tools、图片条件、具体度排序与默认路由均有测试。
- 有序多目标 Fallback、独立触发策略、有限重试、指数退避、jitter、`Retry-After`、Provider Timeout 和总 Deadline 均已实现。
- Runtime 直接执行内嵌 Draft 2020 JSON Schema，并执行唯一性、引用、动态模型、不可达规则、重复目标与安全语义校验。
- 文件监听与 SIGHUP 热加载失败时保留旧快照；请求生命周期固定使用开始时的配置版本。
- 客户端取消传播到上游；100 并发 SSE Race 长稳无事件串流和泄漏。

## Web、CLI 与管理 API

- Web 包含 Overview、Providers、Routes/Explain、Playground/Preview、Logs 和 Settings；覆盖 Provider 探测/最小请求、SSE 实时显示、配置 Diff 与未保存提示。
- CLI 包含 `serve`、`check`、`convert`、`doctor`、`models`、`routes`、`probe`、`status`、`ui`、`version`；查询命令支持机器可读输出，转换默认离线。
- 管理 API 覆盖状态、配置、校验、重载、备份、回滚、Provider、Route Explain、Playground、日志、指标与脱敏诊断包。

## 安全与观测

- 管理面默认回环；公网监听强制高熵 Token；恒定时间鉴权、Host/Origin 防护和登录失败限速有测试。
- Provider URL 默认拒绝私网/Loopback/Link-local/重绑定，重定向关闭，TLS 校验开启，Header 白名单和凭据隔离有测试。
- Body、Header 个数/大小、并发和 SSE 事件大小有限制；gzip 请求受解压后 Body 上限约束。
- 日志默认不采集正文；可选正文经过截断和密钥/Header/Cookie 脱敏；诊断包强制移除正文与解析后密钥。
- 结构化日志、Ring Buffer/JSONL 轮转、P50/P95、Prometheus 分类计数/直方图、Token、Retry/Fallback/超时/取消/诊断/配置加载指标均已实现。

## 自动化证据

- `make release-check`：格式、Vet、全包 Race、Web 单测/类型/构建、六平台交叉构建通过。
- `make web-e2e`：完整控制面 Chromium 工作流通过。
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` 与 `npm audit --audit-level=high`：0 已知漏洞。
- Go 与生产 Web 依赖许可证检查通过；GoReleaser 配置与 GitHub Workflow YAML 校验通过。
- Docker 构建及非 root 健康、就绪、管理 API Smoke 通过。
- 30 秒 Race 长稳：1,968 轮 × 100 并发流，共 196,800 条；Native 网关约 0.103 ms/op；空闲 RSS 约 17 MB。

详细命令与结果见 [`verification.md`](verification.md)。

## 正式发布外部门槛

以下三项不是源码缺口，需要发行环境、真实凭据或连续时间窗口，不能由本地构建伪造：真实 OpenAI/Anthropic/Gemini Smoke、预发布主机 24 小时 Race 长稳、连续两个 RC 的 GitHub 原生平台与多架构镜像认证。执行方法和判定标准已写入 `verification.md`，正式打标签前必须完成。
