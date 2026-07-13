# 优化路线图验收审计

本文件将 [`AI_ROUTER_OPTIMIZATION_ROADMAP.md`](../AI_ROUTER_OPTIMIZATION_ROADMAP.md) 的完成定义映射到当前源码和可复现验证。证据分为“代码具备”“本机通过”“CI 配置具备”和“发布环境待执行”，不把未执行的外部流程写成已通过。

## 阶段完成矩阵

| 阶段 | 当前实现 | 权威证据 |
| --- | --- | --- |
| 0 工程基线 | 基线提交、内部标签、忽略规则和密钥扫描流程已建立 | Git 提交 `00e35d2`、标签 `optimization-baseline-20260713`、`.gitignore` |
| 1 应用平台 | 通用 Registry、Manifest、能力和七个管理 API 已实现；旧 Claude API 委托给 Adapter | `internal/application`、`internal/admin/admin.go`、`docs/openapi.yaml` |
| 2 Claude Code | L1 安装、L2 配置、L3 网关、可选 L4 固定命令 Smoke 均实现 | `internal/application/claudecode` 及单元、API、E2E、真实本机验证 |
| 3 前端收敛 | 四个主页面、共享组件、路由级懒加载、统一 Token；历史页面和 UI vN CSS 已删除 | `web/src/app`、`web/src/pages`、`web/src/styles`、五个 E2E 文件 |
| 4 生效语义 | 保存返回热加载/运行对象重建/重启三类；并发控制、Transport、日志级别可在线替换 | `internal/config/effects.go`、`internal/gateway/gateway.go`、设置页及测试 |
| 5 密钥策略 | Provider 明文/环境变量两种模式；列表脱敏；配置和备份 0600；敏感提示 | 配置、管理 API、Provider/Settings 页面、安全测试 |
| 6 Token Count | Provider 原生计数优先，独立 CJK/英文/代码/Tools/媒体估算回退并显式标记 | `internal/tokencount`、Provider capability、网关测试 |
| 7 故障恢复 | 主配置和应用配置共用原子写入、唯一备份、校验、保留和回滚 | `internal/safefile` 及主配置/Claude Adapter 测试 |
| 8 治理 | 文档、OpenAPI、五个独立 Web E2E、CI/Release Workflow 和本验收记录同步 | `docs`、`.github/workflows`、最终发布门禁输出 |

## 产品与控制台

- 单个 Go 二进制内嵌网关、CLI 与 React 控制台；YAML v1 是唯一事实来源。
- 控制台只保留模型接入、路由配置、应用配置、系统设置四页。每页使用相同内容边界和紧凑单行表格，并分别拥有 Playwright 场景。
- 应用页从 `GET /api/apps` 动态加载 Manifest；只有 Adapter 声明的能力才显示预览、验证或回滚操作。
- 当前不伪造未支持应用；Claude Code 是首个完整 Adapter，新增测试 Adapter 不要求修改通用 API。
- `main.tsx` 为 12 行，各页面不超过 500 行，主 CSS 为 424 行且不存在 `UI vN` 覆盖块。
- 生产构建已按路由拆分，最大单个 JavaScript chunk gzip 小于 250 KB。首次进入时的共享依赖总量仍约 295 KB，主要来自 Ant Design Table/表单体系、React 和 YAML 浏览器解析器；继续压缩需要替换 UI 组件库，超出本轮“保留 Ant Design Table”的约束。

## 运行、安全与协议

- Provider、Route、Retry/Fallback、转换、鉴权、指标、日志和超时字段热加载；`max_concurrent` 在线重建；监听器和进程级字段明确要求重启。
- “运行中/已关闭”是当前进程状态；关闭时网关返回 503，重启后恢复，页面和 API 都标明不持久化。
- Provider API Key 可明文或 `${ENV_NAME}`；管理 Token、客户端 Key 和敏感 Header 默认要求环境变量。API、日志、诊断和 Diff 不返回真实密钥。
- 四个协议 Adapter、16 个转换方向、SSE 生命周期、Tools、图片、Reasoning、结构化输出、usage、Retry/Fallback 与取消行为继续由 Go 测试覆盖。
- Anthropic Token Count 原生接口成功时返回 `estimated: false`；失败或不支持时返回 `estimated: true`、`unicode-lexical-v1` 和组件拆分。

## 本机发布门禁

最终执行结果和环境记录见 [`verification.md`](verification.md)。本轮要求的本机门禁为：

```bash
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test -race ./...
make web-e2e
npm audit --audit-level=high --prefix web
make release-check
```

另行执行 Docker 非 root Smoke、真实 Provider 文本/流式/Tool Calling、Claude Code L1–L4 和预发布长稳测试。

## CI 与正式发布边界

- `.github/workflows/ci.yml` 已配置 Linux 全量门禁、Chromium E2E、三种原生操作系统 Smoke 和 Docker Build。
- `.github/workflows/release.yml` 已配置六平台制品、Checksum、SBOM、OIDC 签名与 amd64/arm64 镜像。
- 没有远程仓库或 GitHub Runner 时，只能证明 Workflow 配置存在及本机等价命令通过，不能声称 GitHub CI 已运行。
- 仓库所有者已于 2026-07-13 明确接受不轮换对话中测试 Key 的风险，因此不再把轮换列为本轮阻断；仓库和发行物仍通过扫描确保不包含真实 Key。
