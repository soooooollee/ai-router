<div align="center">
  <a href="https://github.com/soooooollee/ai-router">
    <img src="web/public/favicon.svg" width="168" alt="AI Router">
  </a>

  <h1>AI Router</h1>

  <p><strong>一个本地网关，四种 AI 协议，连接任意模型。</strong></p>
  <p>通过一个本地入口路由 OpenAI、Anthropic、Gemini 及兼容模型 API。</p>

  <p>
    <a href="README.md">English</a> ·
    <a href="README_ZH.md">简体中文</a>
  </p>

  <p>
    <a href="https://github.com/soooooollee/ai-router/releases"><img src="https://img.shields.io/github/v/release/soooooollee/ai-router?style=flat-square&label=release&color=4f73ff" alt="Release"></a>
    <a href="https://github.com/soooooollee/ai-router/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/soooooollee/ai-router/ci.yml?branch=main&style=flat-square&label=build&color=4f73ff" alt="Build"></a>
    <a href="https://github.com/soooooollee/ai-router/blob/main/LICENSE"><img src="https://img.shields.io/github/license/soooooollee/ai-router?style=flat-square&label=license&color=4f73ff" alt="License"></a>
  </p>
</div>

## Setup

macOS 或 Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/soooooollee/ai-router/main/install.sh | sh
```

macOS、Linux 或 Windows 使用 npm：

```bash
npm install --global https://github.com/soooooollee/ai-router/releases/latest/download/airoute-cli.tgz
```

macOS 或 Linux 使用 Homebrew：

```bash
brew install soooooollee/ai-router/airoute
```

也可以使用 Go 1.24+ 从源码构建：

```bash
git clone https://github.com/soooooollee/ai-router.git
cd ai-router
make build
export PATH="$PWD/bin:$PATH"
```

创建配置并启动 AI Router：

```bash
air init
air start
```

默认配置保存在系统用户配置目录中，而不是当前工作目录：Windows 为 `%AppData%\airoute\airoute.yaml`，macOS 为 `~/Library/Application Support/airoute/airoute.yaml`，Linux 通常为 `~/.config/airoute/airoute.yaml`。如需自定义位置，可使用 `--config PATH` 或 `AIROUTE_CONFIG`。

浏览器地址为：[http://127.0.0.1:12667](http://127.0.0.1:12667/)。

模型网关地址为：[http://127.0.0.1:12666](http://127.0.0.1:12666/)。

常用命令：

```text
air init       在系统用户配置目录创建 airoute.yaml
air start      后台启动
air stop       停止后台实例
air restart    重启后台实例
air logs -f    持续查看后台日志
air serve      前台运行，用于调试
air status     查看运行状态
air ui         打开 Web 控制台
air check      校验 airoute.yaml
air doctor     执行配置诊断
air client-state backup           备份托管客户端、用量、审计数据和本地主密钥
air client-state verify --backup  恢复前校验客户端状态备份
air client-state restore --backup 在 AI Router 停止时恢复已校验的备份
air --help     查看全部命令
```

## 访问密钥

在 Web 控制台打开“访问密钥”，可以直接为每个应用、设备、SDK 或自动化任务生成独立的网关 Key，并按模型、协议、来源 CIDR、RPM、突发容量、并发、每日请求、每日 Token 和单次最大输出 Token 限制权限。完整 Key 只在生成窗口显示一次。

本地生成的 Key 会加密保存，可以在“应用配置”中按名称选择。AI Router 只在后端解密，用于预览、写入或验证 Codex/ChatGPT App、Claude Code、Claude App 和 MiMo Code 配置，完整内容不会返回浏览器。原有 `auth.keys` 会继续生效，也可以在不改变 Key 值的情况下迁移到托管状态库；迁移后的摘要型 Key 仍可鉴权，但不能用于稍后重新写入应用。

通过以下配置启用托管鉴权：

```yaml
auth:
  enabled: true
  managed_store: true
  allow_query_key: false
```

本地动态状态保存在 `gateway-state.db`，HMAC 主密钥单独保存在 `credential-master.key`。请只使用 `air client-state` 或 Web 控制台进行备份；这样数据库与主密钥会作为一个带校验清单的整体进行验证和恢复。

服务端部署可以通过 `AIROUTE_CREDENTIAL_MASTER_KEY` 提供当前主密钥，并通过 JSON 对象 `AIROUTE_CREDENTIAL_PREVIOUS_KEYS` 提供旧版本主密钥。只要有效 Credential 依赖的任一代主密钥缺失，AI Router 就会拒绝启动或备份。

## License

AI Router 使用 [MIT License](LICENSE)。
