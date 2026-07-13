# 优化发布验收记录

记录日期：2026-07-13

环境：macOS arm64、Go 1.24.5、Node.js 22.22.2、npm 10.9.7、Chromium、OrbStack Docker 29.4.0、Claude Code 2.1.207

验收对象：`AI_ROUTER_OPTIMIZATION_ROADMAP.md`

本文件只记录本轮实际执行并可复现的结果。最终命令结果与远程 CI 证据写入下表；尚未执行的发布环境操作单独标记。

## 代码与本机验证

| 范围 | 结果 | 复现命令/证据 |
| --- | --- | --- |
| 应用 Adapter | Race 通过；Manifest、未知应用、缺失/损坏配置、保留字段、只读预览、写入/备份/回滚、CLI 超时/取消/截断/脱敏均有直接断言 | `go test -race ./internal/application/... ./internal/admin` |
| 配置生效与安全 | Race 通过；三类 effect、在线并发/Transport/日志级别、0600、环境变量/明文、路径穿越均通过 | `go test -race ./internal/config ./internal/safefile ./internal/gateway` |
| Token Count | Race 通过；原生精确值和中文/英文/代码/Tools/媒体估算回退均通过 | `go test -race ./internal/tokencount ./internal/provider ./internal/gateway` |
| Web 单元/类型/构建 | 2 个 Vitest、TypeScript 和 Vite 生产构建通过 | `make web` |
| Web 独立 E2E | 5/5 通过（Provider、Route、Application、Settings、Runtime），最终回归耗时 8.9 秒 | `make web-e2e` |
| 多视口 UI | 1280、1440、1920 与 620 px 无全局横向溢出；四页边界一致 | 本机 Chromium DOM/截图检查 |
| Go 格式/Vet/Race | 全包通过 | `make release-check`，最终回归 2026-07-13 16:01 CST |
| 六平台交叉构建 | Linux/macOS/Windows amd64/arm64 全部成功 | `make release-check` |
| 漏洞 | npm 0 个漏洞；Go 无可达漏洞 | `npm audit --audit-level=high --prefix web`、`govulncheck ./...` |
| Docker 非 root | 镜像构建成功；UID 100；配置 0600；health、ready、鉴权 admin 均为 200 | `docker build -t airoute:optimization-check .` 及本机容器 Smoke |
| 本机 Race 长稳 | 60 秒通过，3,955 轮 × 100 并发流，共 395,500 条 | `AIROUTE_SOAK_DURATION=60s go test -race ./internal/gateway -run '^TestLongSoak$' -count=1 -timeout=2m -v` |
| 24 小时 Race 长稳 | 仓库所有者确认本机通过 | 2026-07-13 用户确认；原始日志未入库，因此不声明未经记录的吞吐量或精确起止时间 |

## 真实链路验证

| 链路 | 结果 | 验收内容 |
| --- | --- | --- |
| SiliconFlow Qwen 3.x | 最新二进制通过 | 文本 200；SSE 6 个事件并 `[DONE]`；强制 Tool Calling 1 个合法 JSON 参数；追踪 Header 指向 Qwen 目标 |
| Xiaomi MiMo | 最新二进制通过 | OpenAI 文本 200；SSE 9 个事件并 `[DONE]`；Tool Calling 1 个合法 JSON 参数；Anthropic 生成 200 |
| Token Count 实服务 | 回退行为通过 | Xiaomi 原生计数端点未返回可用结果，自动返回 `estimated: true` 与 `unicode-lexical-v1`，生成链路不受影响；原生精确路径由网关 Mock 测试验证 |
| Claude Code | 最新二进制 L1–L4 全部通过 | 安装约 555 ms、配置同步、真实网关约 889 ms、受控 CLI 约 3.29 秒 |

真实请求只记录 Provider、模型、HTTP 状态、耗时和请求 ID，不记录 Key、完整 Prompt、响应正文或应用配置正文。

## 前端量化结果

- `web/src/main.tsx`：12 行。
- 最大页面组件：474 行，全部低于 500 行。
- `web/src/styles.css`：424 行；不存在历史 `UI vN` 区块。
- 路由级构建已拆分；最大单个 JS chunk gzip 约 187 KB。
- 首屏共享依赖合计 gzip 约 295 KB，主要是 Ant Design、React 与 YAML；在保留 Ant Design Table 的本轮约束下记录为已知依赖成本。

## CI 与发布环境

| 项目 | 状态 | 说明 |
| --- | --- | --- |
| GitHub CI Workflow | 远程完整通过 | 主分支 Run [`29234871105`](https://github.com/soooooollee/ai-router/actions/runs/29234871105)，2026-07-13；Test、Playwright、Race、Vet、Build、漏洞/许可证、三平台 Smoke 与 Docker 全部成功 |
| GitHub Release Workflow | 发布前门槛完成，等待正式标签 | 24 小时 Race 已由仓库所有者确认通过；下一步验证 OIDC、GHCR 与六平台发行制品 |
| 外部 Key 轮换 | 所有者接受风险 | 2026-07-13 明确决定不轮换，不再作为本轮阻断；仓库与发行物继续禁止包含真实 Key |
| 24 小时预发布长稳 | 已完成 | 仓库所有者确认本机 24 小时 Race 通过，并接受其作为本轮正式发布门槛证据 |

全部发布前验证已经完成；允许创建正式标签，Release Workflow 的制品、签名、SBOM 和镜像结果将在执行后补录。
