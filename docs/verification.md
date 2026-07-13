# 发布验收记录

本文记录仓库当前可复现的发布门禁。它区分“代码与发行物已验证”和“需要真实凭据/时间窗口的发布认证”，避免把未执行的外部验证写成已通过。

## 已通过

| 范围 | 验证结果 | 复现命令 |
| --- | --- | --- |
| Go 格式、Vet、Race | 全部包通过 | `make release-check` |
| Web 单元、类型、构建 | Vitest、TypeScript、Vite 通过 | `make web` |
| Web 核心 E2E | Chromium：鉴权、Provider 探测/最小请求/新增、Route 新增、转换预览、实时 SSE、日志筛选、无效配置拒绝、原子保存/热加载、Diff 与回滚 | `make web-e2e` |
| 协议矩阵 | 四协议请求、响应、SSE 共 16 个方向；并行 Tools、图片、Document、Refusal、结构化输出、Reasoning、usage、扩展往返 | `go test ./internal/protocol -v` |
| 网关可靠性 | 16 方向的 429、非法 JSON、超时和流中断；Retry、Fallback、总 Deadline、取消、断流后不切换、严格有损策略 | `go test -race ./internal/gateway -v` |
| 并发与长稳 | Race 下 30 秒完成 1,968 轮 × 100 并发流，共 196,800 条；无串流、竞态或 Goroutine 泄漏 | `AIROUTE_SOAK_DURATION=30s go test -race ./internal/gateway -run TestLongSoak -count=1 -timeout=2m -v` |
| 性能与资源 | Native 网关约 0.103 ms/op；空闲 RSS 约 17 MB，低于 5 ms / 50 MB 预算（Apple M3 Pro，本机测量） | `go test ./internal/gateway -run '^$' -bench BenchmarkNativeGateway -benchmem -count=3`、`ps -o rss= -p $PID` |
| 安全 | Host/Origin/鉴权/限速、SSRF、DNS 重绑定防护、私网默认拒绝、重定向关闭、日志脱敏 | `go test -race ./internal/admin ./internal/secure ./internal/gateway` |
| 漏洞 | Go 与 npm 均无已知漏洞 | `govulncheck ./...`、`npm audit --audit-level=high` |
| 许可证 | Go 依赖检查通过；生产 Web 依赖仅 MIT/ISC | 见 CI `go-licenses` 与 `license-checker` 步骤 |
| 平台构建 | Linux、macOS、Windows 的 amd64/arm64 均完成交叉编译 | `make release-check` |
| Docker | 镜像构建、非 root 启动、健康/管理 API、挂载目录内原子保存与备份通过 | `docker build .` 与 Compose 流程 |
| Qwen 3.x / MiMo 实服务 | SiliconFlow Qwen 3.6 与 Xiaomi MiMo 2.5 的文本、SSE、Thinking、Tools、跨协议及 Claude Code 两轮 Bash Tool 工作流通过 | 见 `docs/qwen3-compatibility.md` |
| 发行元数据 | 版本注入、压缩包、校验和、SBOM、OIDC 签名、GHCR 多架构镜像已配置 | `.goreleaser.yaml`、`.github/workflows/release.yml` |

## 发布标签前的环境认证

以下项目依赖真实供应商凭据、外部 Runner 或连续时间窗口，不伪装成本地已执行：

1. 使用 OpenAI、Anthropic、Gemini 的真实 API Key 各跑一轮文本、流式、图片和 Tool Calling Smoke Test。
2. 在预发布主机执行 24 小时长稳：

   ```bash
   AIROUTE_SOAK_DURATION=24h go test -race ./internal/gateway -run TestLongSoak -count=1 -timeout=25h -v
   ```

3. 发布两个 RC 标签，通过 GitHub 的 Linux/macOS/Windows 原生 Smoke Job 与多架构 Docker Job 后再创建正式标签。

这些是“是否打正式发布标签”的运营门槛，不影响本地源码、二进制、Docker、Web 和自动化测试的完整构建。
