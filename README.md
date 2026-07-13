# AI Router

Web 控制台按“模型接入 → 路由配置 → 应用配置”组织，并提供独立的系统设置页：接入时可用真实请求自动识别 OpenAI、Anthropic、Gemini 及 Qwen/MiMo 模型族；路由页选择模型和客户端输出协议；应用配置页由通用 Adapter 驱动，当前完整支持 Claude Code 的检测、预览、写入、验证、备份和回滚；设置页编辑唯一的 YAML 配置并显示本次变更的真实生效方式。

AI Router 是一个精简的 AI 协议转换与模型路由网关。它以单个 Go 二进制运行，同时提供网关 API、CLI 和内嵌 Web 控制台；YAML 配置文件始终是唯一事实来源。

支持以下协议作为客户端入口和 Provider 出口：

- OpenAI Chat Completions
- OpenAI Responses
- Anthropic Messages
- Gemini Generate Content

支持文本、图片、Tool Calling、结构化输出、Reasoning、usage、错误映射及 SSE 流式转换。同协议请求自动使用保留原生字段的快速路径。

## 快速开始

要求：Go 1.24+；只有修改 Web 控制台时才需要 Node.js 22+。

```bash
cp examples/airoute.minimal.yaml airoute.yaml
# 编辑 airoute.yaml，将 api_key 替换为实际密钥
make build
./bin/airoute check --config airoute.yaml
./bin/airoute serve --config airoute.yaml
```

默认地址：

- 网关：`http://127.0.0.1:8080`
- Web 控制台：`http://127.0.0.1:8081`
- 健康检查：`http://127.0.0.1:8080/healthz`

OpenAI SDK 示例：

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"gpt-5-mini","messages":[{"role":"user","content":"hello"}]}'
```

Anthropic SDK 兼容入口：

```bash
curl http://127.0.0.1:8080/v1/messages \
  -H 'content-type: application/json' \
  -d '{"model":"gpt-5-mini","max_tokens":256,"messages":[{"role":"user","content":"hello"}]}'
```

配置模型别名后，客户端无需知道真实 Provider 或模型名称。完整配置见 [examples/airoute.full.yaml](examples/airoute.full.yaml)。

Qwen 3.x、SiliconFlow 与 Xiaomi MiMo 示例见 [examples/airoute.qwen3.yaml](examples/airoute.qwen3.yaml)。Provider API Key 直接保存在本机配置中，并会在模型接入和系统设置页面显示实际值；相关配置文件与备份使用 `0600` 权限。

Claude Code 接入流程：先在“模型接入”完成 Provider 测试并保存，再在“路由配置”创建 Anthropic Messages 输出路由，最后进入“应用配置”选择该路由并写入。应用验证分为安装检测、配置同步、真实网关请求和可选的受控 CLI Smoke Test；CLI 参数由 Adapter 固定，不接受任意 Shell 命令。

## CLI

```text
airoute serve      启动网关和 Web 控制台
airoute check      校验配置
airoute convert    离线转换协议 JSON
airoute doctor     检查配置和监听端口
airoute models     列出 Provider 模型
airoute routes     列出路由
airoute probe      探测 Provider
airoute status     查询运行状态
airoute ui         打开 Web 控制台
airoute version    输出构建版本
```

离线转换示例：

```bash
airoute convert --from openai-chat --to anthropic-messages request.json
```

## Docker

```bash
mkdir -p data
cp examples/airoute.docker.yaml data/airoute.yaml
AIROUTE_UID="$(id -u)" AIROUTE_GID="$(id -g)" docker compose up --build
```

Compose 挂载整个 `data/` 目录，使 Web 的原子保存、备份和回滚在容器内仍然可用；Linux 下通过当前 UID/GID 保持文件归属。管理端口仅绑定本机。若配置中的 Provider 指向本机或私网地址，需要对该 Provider 明确设置 `allow_private_url: true`。

## 设计

所有协议先解码为 Canonical Request IR；所有流式响应先解码为 Canonical Event IR。因此增加一种协议只需增加一个 Adapter，而不是实现与所有现有协议的两两转换。无法无损表达的字段由 `strict`、`warn` 或 `drop` 策略处理，并产生稳定的诊断码。

进一步说明：

- [配置](docs/configuration.md)
- [协议转换](docs/protocols.md)
- [Qwen 3.x / MiMo 兼容验证](docs/qwen3-compatibility.md)
- [路由与 Fallback](docs/routing.md)
- [安全](docs/security.md)
- [管理 API](docs/openapi.yaml)
- [配置迁移](docs/migrations.md)
- [发布验收记录](docs/verification.md)
- [构建计划验收审计](docs/acceptance.md)
- [完整构建计划](AI_ROUTER_BUILD_PLAN.md)
- [本轮优化推进与完成状态](AI_ROUTER_OPTIMIZATION_ROADMAP.md)

## 验证

```bash
make release-check
make web-e2e
```

测试覆盖四协议请求、响应和流式事件的 16 种转换方向，以及网关端到端转换、重试、Fallback、五个独立 Web 工作流和通用应用 Adapter。

Anthropic Token Count 入口优先转发给具备原生计数能力的目标 Provider；不可用时使用独立的 Unicode 词法估算器，并返回 `estimated: true`、策略名和 System/消息/Tools/媒体拆分，普通生成请求不会受计数失败影响。

正式发行前运行持续压力测试：

```bash
AIROUTE_SOAK_DURATION=24h go test ./internal/gateway -run TestLongSoak -count=1 -timeout=25h
```

## License

MIT
