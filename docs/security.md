# 安全

## 默认边界

- Web 管理端默认只绑定 `127.0.0.1`。
- 管理端绑定非回环地址时要求至少 24 字符 Token。
- 管理 API 校验 Token、Host 和 Origin。
- 客户端 API Key 与管理 Token 使用恒定时间比较。
- Provider 凭据不会出现在管理 API、错误和请求日志。
- Provider API Key 可按用户选择直接写入本机 YAML，或保存为 `${ENV_NAME}` 引用；模型列表只暴露保存模式和引用名。
- 主配置、主配置备份、应用配置和应用备份统一使用 `0600` 权限；备份列表显式标注可能包含本地密钥。

## 上游请求

- 只允许 HTTP/HTTPS URL。
- 默认拒绝解析到私网、Loopback 和 Link-local 的 Provider。
- 自动重定向关闭。
- TLS 证书校验始终开启；首版不提供从配置关闭校验的危险开关。需要企业代理时使用系统 `HTTPS_PROXY` 和受信任 CA。
- 客户端 Authorization 不会直接转发。
- 自定义 Header 不能覆盖 Authorization、API Key、Host、Connection 或 Content-Length。
- 请求 Body、Header、并发、SSE 单事件和响应读取均有上限。
- 客户端断开会取消上游 Context。

## 日志

默认只保留有界内存元数据，不采集 Prompt 和响应正文。日志记录 Client Key ID 而不是值。上游错误正文只保留截断、去换行的安全片段。

管理端 `GET /api/diagnostics` 可导出带内容清单的 JSON 诊断包。导出会移除请求/响应正文，配置对象不包含解析后的 Token、API Key、自定义 Header 或 Provider 默认请求字段。

直接保存的 Key 会存在于 YAML 与备份中，因此只适用于可信的单用户机器。轮换时先更新环境变量或控制台配置，验证新 Key 后删除过期备份；任何曾经进入对话、终端历史、日志或版本库的 Key 都必须在供应商侧撤销并重新签发。

Claude Code 验证只执行 Adapter 内固定的 `claude --version` 与可选非交互 Smoke 参数，使用 `exec.CommandContext`、独立临时目录、超时和输出上限，不接受用户提供的命令名或 Shell 参数。

## 远程管理

推荐通过 SSH Tunnel 或受保护的反向代理访问管理面。不要把管理端口直接暴露到公网；即使有 Token，也应配合 TLS、网络访问控制和代理层限速。

反向代理使用域名时，需要显式加入允许列表：

```yaml
admin:
  allowed_hosts: [router.example.com]
```
