# AI Router 优化推进文档

> 文档状态：源码与本机验收完成；等待远程 CI 与预发布长稳
> 更新时间：2026-07-13  
> 适用范围：AI Router 核心网关、Web 控制台、应用配置、配置安全与发行工程

## 1. 文档目标

本文档用于指导 AI Router 从“核心能力可用”推进到“结构稳定、可持续扩展、可正式发布”的下一阶段建设。

当前系统已经具备多协议转换、模型路由、Provider 探测、配置热加载、Claude Code 配置写入、Web 控制台和单二进制交付能力。下一阶段不再继续堆叠页面，而是重点解决以下问题：

- 应用配置页面已经通用化，但后端仍然写死 Claude Code。
- 缺少对 Claude Code 等真实应用的安装、配置生效和完整调用验证。
- 前端文件和历史样式持续累积，导致布局与视觉调整容易互相影响。
- 部分配置看似支持热加载，实际仍受启动时对象限制。
- 明文密钥、环境变量引用、备份和文档之间缺少统一策略。
- Claude Code Token 统计仍是粗略估算。
- Git、测试、文档和发行证据需要形成可信基线。

## 2. 改造前质量基线

以下数据是 2026-07-13 开始改造时的对照基线，不代表完成后的当前状态：

- `go test ./...`：通过。
- `go test -race ./...`：通过。
- `go vet ./...`：通过。
- Go 格式检查：通过。
- 前端 TypeScript 类型检查：通过。
- 前端单元测试：通过。
- Web 核心 E2E：通过。
- `npm audit --audit-level=high`：0 个已知漏洞。
- Go 核心包覆盖率大多为 63%–88%。
- 当前二进制约 12 MB。
- 当前生产 Web JavaScript 约 920 KB，构建后 gzip 约 297 KB。
- `web/src/main.tsx` 约 2918 行。
- `web/src/styles.css` 约 5464 行，并存在多代覆盖样式。
- 改造开始时仓库尚未形成首个可信 Git 基线，文件仍处于未跟踪状态。

上述结果说明核心网关不是原型代码，但产品扩展层和前端工程需要尽快收敛。

## 3. 优化原则

1. **先建立基线，再进行结构重构。**
2. **应用配置必须平台化，Claude Code 只是第一个适配器。**
3. **真实应用验证优先于简单 HTTP 请求测试。**
4. **配置能力必须明确标注热加载、重启生效或仅当前进程有效。**
5. **允许用户直接输入 API Key，但必须明确保存位置、权限、备份和风险。**
6. **前端只保留当前产品需要的页面和样式，不继续叠加新的 UI 版本。**
7. **任何重构都不得降低现有协议矩阵、路由、流式和安全测试能力。**

## 4. 总体优先级

| 优先级 | 主题 | 目标 |
| --- | --- | --- |
| P0 | 工程与密钥基线 | 建立可回退版本，轮换已经暴露的测试密钥 |
| P1 | 应用配置平台化 | 建立统一应用适配器和 API，迁移 Claude Code |
| P1 | 真实应用验证 | 检测、写入、验证、诊断和回滚形成闭环 |
| P1 | 前端工程收敛 | 拆分组件、统一样式、删除历史代码 |
| P1 | 配置生效语义 | 明确热加载和重启边界，修复伪热加载字段 |
| P2 | 密钥与备份策略 | 兼顾直接输入体验与本地安全 |
| P2 | Token 统计准确性 | 降低 Claude Code 上下文误判风险 |
| P2 | 测试与文档治理 | 让验收文档与当前产品保持一致 |
| P3 | 轻量诊断能力 | 在不恢复无关页面的前提下提供必要排障信息 |

## 5. 推进阶段

## 阶段 0：建立可信工程基线

### 目标

确保后续所有改造都可以比较、回退、审查和发布。

### 工作项

- 初始化并确认 Git 仓库边界。
- 检查 `.gitignore`，确保以下内容不进入版本库：
  - Provider API Key。
  - 管理 Token。
  - 本机 Claude Code 配置和备份。
  - 临时构建产物、日志和诊断文件。
- 创建首个基线提交并打内部基线标签。
- 轮换曾经直接出现在对话或测试记录中的供应商密钥。
- 在提交前运行：

  ```bash
  make release-check
  make web-e2e
  npm audit --audit-level=high --prefix web
  ```

### 验收标准

- 工作区只保留明确需要的未提交改动。
- 任意文件都可以通过 Git 回退到基线版本。
- 仓库历史中不包含真实密钥。
- CI 在基线提交上完整通过。

## 阶段 1：应用配置平台化

### 目标

让“应用配置”成为真正可扩展的应用管理能力，而不是 Claude Code 页面改名。

### 后端设计

新增应用适配器接口，建议放置在 `internal/application`：

```go
type Adapter interface {
    Manifest(context.Context) Manifest
    Detect(context.Context) (Detection, error)
    Read(context.Context) (State, error)
    Preview(context.Context, DesiredConfig) (Plan, error)
    Apply(context.Context, DesiredConfig) (ApplyResult, error)
    Verify(context.Context, VerifyOptions) (VerifyResult, error)
    Backups(context.Context) ([]Backup, error)
    Rollback(context.Context, string) (ApplyResult, error)
}
```

首个实现：

```text
internal/application/claudecode
```

### 应用清单

每个应用必须提供稳定清单：

```json
{
  "id": "claude-code",
  "name": "Claude Code",
  "description": "命令行编码助手",
  "status": "available",
  "capabilities": ["detect", "configure", "verify", "rollback"],
  "config_format": "json"
}
```

### 管理 API

用通用 API 替代新增的应用专用路由：

```text
GET  /api/apps
GET  /api/apps/{id}
POST /api/apps/{id}/preview
PUT  /api/apps/{id}/config
POST /api/apps/{id}/verify
GET  /api/apps/{id}/backups
POST /api/apps/{id}/rollback
```

原接口暂时兼容：

```text
GET /api/claude-code/config
PUT /api/claude-code/config
```

兼容期内原接口调用新的 Claude Code Adapter，不再保留两套实现。

### 前端改造

- 应用列表从 `GET /api/apps` 动态加载。
- 应用名称、状态、能力和配置路径不再写死在 React 中。
- 没有 `verify` 能力的应用不显示验证按钮。
- 没有 `rollback` 能力的应用不显示备份历史。
- 应用详情按照 Manifest 和 Adapter 返回的能力渲染。
- 当前只展示已经真正实现的应用，不伪造 Codex、Cursor 等未支持状态。

### 验收标准

- Claude Code 功能完全迁移到通用 Adapter。
- 前端不再直接调用 `/api/claude-code/config`。
- 增加第二个测试 Adapter 时不需要修改通用管理 API。
- 应用列表、读取、预览、写入、备份和回滚均有独立测试。

## 阶段 2：Claude Code 真实应用验证

### 目标

从“文件写入成功”提升到“Claude Code 确实能够使用 AI Router”。

### 验证层级

#### L1：安装检测

- 检测 `claude` 可执行文件是否存在。
- 获取版本信息。
- 返回可执行文件路径，但不允许用户通过 API 指定任意命令路径。

#### L2：配置检测

- 读取 Claude Code 实际配置路径。
- 验证 AI Router 管理字段是否与预期一致。
- 验证配置文件权限和 JSON 格式。
- 明确展示保留字段数量和最近备份。

#### L3：网关链路验证

- 使用应用实际协议发送最小请求。
- 验证路由、协议转换、Provider 密钥和模型响应。
- 返回请求 ID、目标 Provider、模型和耗时。

#### L4：可选 CLI Smoke Test

- 通过固定参数调用 Claude Code 非交互命令。
- 使用严格超时和固定提示词。
- 不允许从 Web 输入任意 Shell 参数。
- 默认不读取项目文件、不执行工具、不修改工作区。
- 将 CLI 输出截断并脱敏后返回。

### 安全要求

- 只能执行 Adapter 内部声明的允许命令。
- 禁止拼接用户输入到 Shell 字符串。
- 使用 `exec.CommandContext` 和参数数组。
- 设置超时、输出上限和独立工作目录。
- 日志不得记录 API Key、完整环境变量或用户配置正文。

### 前端体验

应用详情页显示：

- 已安装/未安装。
- 应用版本。
- 配置已同步/需要写入。
- 最近验证时间。
- 验证阶段和失败原因。
- “写入配置”和“验证应用”两个独立动作。

### 验收标准

- 本机安装 Claude Code 时可以完成 L1–L3 验证。
- 开启 CLI Smoke Test 后可以完成受控的 L4 验证。
- Claude Code 不存在时返回可理解的提示，不返回 500 堆栈。
- 验证失败不会修改应用配置。

## 阶段 3：前端工程收敛

### 目标

消除多代 CSS 互相覆盖和单文件持续膨胀，避免每次调整一个页面影响其他页面。

### 建议目录

```text
web/src/
  app/
    App.tsx
    navigation.ts
    api.ts
  pages/
    ProvidersPage/
    RoutesPage/
    ApplicationsPage/
    SettingsPage/
  components/
    DataPanel/
    DataTable/
    WorkflowSteps/
    FormField/
    StatusBadge/
    Notification/
  styles/
    tokens.css
    reset.css
    shell.css
    components.css
  types/
```

### 工作项

- 将 `main.tsx` 拆分为页面和共享组件。
- 删除当前不可访问的 Overview、Playground、Logs 和旧 Claude Setup 组件。
- 删除 `styles.css` 中已经被后续版本覆盖的 UI v1–v7 历史规则。
- 建立统一设计 Token：
  - 页面边距。
  - 内容宽度。
  - 表格行高。
  - 圆角。
  - 边框颜色。
  - 字体等级。
  - 选中、成功、警告和错误状态。
- 所有页面使用同一个 `PageLayout` 和 `Panel` 容器。
- 保留 Ant Design Table，但仅按需引入组件。
- 评估路由级懒加载和 Bundle 拆分。

### 量化目标

- `main.tsx` 降至 300 行以内。
- 单个页面组件原则上不超过 500 行。
- 全局 CSS 降至 1800 行以内。
- 不再出现 `UI vN` 式覆盖区块。
- 生产 JavaScript gzip 目标低于 250 KB；无法达到时必须记录主要依赖原因。
- 四个主页面在同一视口下左右边界完全一致。

### 验收标准

- 删除任一旧样式不会改变当前页面。
- 页面切换无宽度跳动和横向溢出。
- 1280px、1440px、1920px 和移动端均通过布局检查。
- 每个主页面至少有一条独立 E2E。

## 阶段 4：配置生效语义治理

### 目标

用户必须知道某项设置是否立即生效，避免“保存成功但实际仍使用旧值”。

### 配置分类

#### 可热加载

- Provider。
- Route。
- Retry/Fallback 策略。
- 转换策略。
- 日志级别和历史容量。
- Provider 超时。

#### 需要重新构建运行对象

- `server.max_concurrent`。
- HTTP Transport 连接池相关设置。
- 可能影响全局运行对象的限制参数。

#### 需要重启进程

- `server.listen`。
- `server.admin_listen`。
- 进程级日志输出目标等无法安全在线切换的字段。

### 工作项

- 为配置字段建立生效类型元数据。
- 保存接口返回：

  ```json
  {
    "ok": true,
    "hot_reloaded": ["providers", "routes"],
    "runtime_rebuilt": ["server.max_concurrent"],
    "restart_required": ["server.listen"]
  }
  ```

- 对可以安全重建的运行对象实现在线替换。
- 对监听地址等字段显示“已保存，重启后生效”。
- 明确“运行中/已关闭”是临时状态还是持久状态。
- 如果暂停状态不持久化，页面必须提示“进程重启后恢复运行”。

### 验收标准

- 修改 `max_concurrent` 后实际并发限制发生变化，或明确要求重启。
- 修改监听地址不会错误显示为已经热加载。
- 配置保存响应和页面提示一致。

## 阶段 5：密钥、配置与备份策略统一

### 目标

保留直接在页面输入 API Key 的便利性，同时明确并控制本地泄露面。

### 推荐模式

#### 本地便利模式

- 允许 API Key 直接写入配置。
- 配置和备份强制使用 `0600` 权限。
- 页面默认遮罩，不主动重复显示完整密钥。
- 导出诊断包必须移除密钥。

#### 环境变量模式

- 保存 `${ENV_NAME}` 引用。
- 页面只显示引用名称和“已解析/缺失”。
- 不在配置备份中保存真实值。

### 工作项

- 在模型接入页明确显示当前密钥保存方式。
- 设置页打开完整 JSON 时显示敏感配置提示。
- 管理 API 返回原始 YAML 前执行明确的权限检查。
- 备份列表标注是否包含明文敏感字段。
- 文档统一为“Provider API Key 支持明文或环境变量；管理 Token 和客户端访问 Key 推荐并默认要求环境变量”。
- 增加密钥轮换说明。

### 验收标准

- 两种模式都有测试。
- 诊断、日志、错误和 Provider 列表不泄露密钥。
- 配置和应用备份权限固定为 `0600`。
- 文档与实际校验逻辑一致。

## 阶段 6：Token 统计准确性

### 目标

降低 Claude Code 长会话、中文和大型工具定义下的上下文误判风险。

### 推荐策略

按优先级执行：

1. Provider 支持原生 Token Count 时转发到原生接口。
2. 已知模型使用对应 tokenizer。
3. 未知模型使用本地估算，并明确返回 `estimated: true` 和估算策略。

### 工作项

- 为 Provider Profile 增加 Token Count 能力声明。
- 为 Token Count 建立独立接口层，不写在 Gateway Handler 中。
- 对文本、System Prompt、图片、Tool Schema 和 Tool Result 分别计数。
- 增加中文、英文、代码、大型 JSON Schema 和 Claude Code System Prompt 测试。

### 验收标准

- 已知模型误差目标低于 5%。
- 估算模式必须明确标记，不能伪装成精确结果。
- Token Count 失败不影响普通生成请求。

## 阶段 7：备份、回滚与故障恢复

### 目标

所有由 AI Router 修改的文件都具有一致的事务和恢复能力。

### 工作项

- 配置文件和应用配置共用原子写入工具。
- 备份文件名使用纳秒时间戳或随机后缀，避免同一秒覆盖。
- 写入流程统一为：
  1. 读取原文件。
  2. 生成预览和 Diff。
  3. 写备份。
  4. 写临时文件。
  5. `fsync`。
  6. 原子 Rename。
  7. 重新读取并校验。
- 应用配置页增加备份历史和回滚入口。
- 定义备份保留数量和清理策略。

### 验收标准

- 连续快速保存不会覆盖已有备份。
- 写入中断后原文件仍有效。
- 回滚后自动重新检测并显示同步状态。

## 阶段 8：测试、文档与发布治理

### 测试拆分

前端 E2E 至少拆分为：

```text
providers.spec.ts
routes.spec.ts
applications.spec.ts
settings.spec.ts
runtime.spec.ts
```

新增应用测试：

- Manifest 和能力声明。
- 应用不存在。
- 配置文件不存在。
- 配置文件损坏。
- 保留未知字段。
- 预览不写文件。
- 写入、备份和回滚。
- CLI 超时、取消和输出截断。
- 敏感数据脱敏。

### 文档治理

- 更新 README 的真实页面结构。
- 删除验收文档中已经移除的 Overview、Playground 和 Logs 页面描述。
- 区分“代码具备”“本机验证通过”“CI 验证通过”“发布环境待验证”。
- 验收结果必须附命令、时间和环境，不保留无法复现的历史结论。

### 发布门禁

每个里程碑至少通过：

```bash
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test -race ./...
make web-e2e
npm audit --audit-level=high --prefix web
```

正式版本另外要求：

- 六平台交叉构建。
- Docker 非 root Smoke。
- 真实 Provider 文本、流式和 Tool Calling Smoke。
- Claude Code 真实应用验证。
- 预发布长稳测试。

## 6. 推荐迭代顺序

### 迭代 A：基线与应用架构

- 完成阶段 0。
- 建立 Application Adapter。
- 迁移 Claude Code 后端。
- 保持当前 UI 行为不变。

### 迭代 B：真实验证闭环

- 完成 Claude Code L1–L3。
- 增加预览、验证结果、备份和回滚。
- 建立独立应用 E2E。

### 迭代 C：前端重构

- 拆分 `main.tsx`。
- 清理历史 CSS。
- 固化布局和组件规范。
- 完成四页面独立 E2E。

### 迭代 D：运行与安全语义

- 修复配置热加载边界。
- 统一密钥模式和文档。
- 完成 Token Count 改造。

### 迭代 E：发布候选

- 完成真实应用与 Provider Smoke。
- 完成长稳和跨平台验证。
- 生成 RC 版本并进入正式发布评审。

## 7. 关键验收矩阵

| 能力 | 验收条件 |
| --- | --- |
| 应用扩展 | 新增测试应用不修改通用 API 和页面框架 |
| Claude Code | 检测、配置、验证、备份、回滚完整可用 |
| 前端一致性 | 四页面边界、表格、按钮、状态样式统一 |
| 热加载 | 页面提示与实际生效范围一致 |
| 密钥 | 允许直接输入，但日志、诊断和列表不泄露 |
| Token Count | 已知模型准确，未知模型明确标记估算 |
| 可靠性 | Race、Fallback、流式和取消测试持续通过 |
| 发行 | Git、CI、跨平台、Docker 和文档证据完整 |

## 8. 风险与控制

| 风险 | 控制措施 |
| --- | --- |
| 应用适配器演变成任意命令执行平台 | 命令和参数由 Adapter 固定声明，禁止 Shell 拼接 |
| 前端重构导致功能回归 | 先补独立 E2E，再拆组件和 CSS |
| 密钥直接保存造成泄露 | `0600`、遮罩、脱敏、备份提示和可选环境变量模式 |
| 配置热加载行为变化 | 返回生效清单，增加并发和重启语义测试 |
| Tokenizer 引入大型依赖 | 能力按 Provider 可选加载，保留轻量估算回退 |
| 应用配置覆盖用户自定义字段 | Preview、结构化合并、备份、原子写入和回滚 |

## 9. 完成定义

本轮优化只有在满足以下条件时才视为完成：

- 应用配置由通用 Adapter 驱动。
- Claude Code 具备真实安装、配置和链路验证。
- 前端不再依赖多代覆盖 CSS。
- 所有配置字段具有明确的生效方式。
- 密钥策略和文档完全一致。
- Token Count 不再仅依赖固定字符比例。
- 应用配置具备可见的备份与回滚能力。
- Git、CI、E2E、Race、跨平台和发布文档形成完整证据链。

## 10. 原始启动顺序（已完成）

本轮按以下三个任务启动，现均已完成：

1. 建立 Git 基线并轮换测试密钥。
2. 创建 `internal/application` 和 Claude Code Adapter。
3. 为现有应用配置流程补充独立 E2E，再开始前端拆分。

## 11. 2026-07-13 执行结果

阶段 0–8 的源码、前端、测试、文档和本机发布门禁已经完成。最终实现包括通用 Application Adapter、Claude Code L1–L4、统一原子备份回滚、四页控制台与路由级拆包、配置生效分类、双密钥模式、Provider 原生 Token Count 与本地估算回退，以及五个独立 E2E。

本机已通过全包 Race、Vet、六平台交叉构建、5/5 Web E2E、npm/Go 漏洞检查、Docker 非 root Smoke、Qwen/MiMo 文本/流式/Tool Calling、Claude Code 真实验证和 60 秒 395,500 条并发流长稳。逐项证据见 [`docs/acceptance.md`](docs/acceptance.md) 和 [`docs/verification.md`](docs/verification.md)。

正式公开版本仍有两项环境门槛，未完成前不得标记为已发布：运行 GitHub CI/Release Workflow，以及在预发布主机完成 24 小时 Race 长稳。仓库所有者已于 2026-07-13 明确决定不轮换此前用于测试的 Xiaomi/SiliconFlow Key，该风险作为所有者接受项记录，不再作为本轮发布阻断；源码、日志、诊断和 Git 历史仍不得包含这些 Key。
