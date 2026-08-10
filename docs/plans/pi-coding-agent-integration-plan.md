# Pi Coding Agent 接入 AI Router：调研与开发计划

> 研究时间：2026-08-10 | 研究对象：Pi Coding Agent（`earendil-works/pi`）| 结论类型：产品接入与实施计划

## 一句话结论

应将 Pi 作为 AI Router 的第五个“一方应用适配器”接入，管理其全局 `~/.pi/agent/models.json` 与 `settings.json`：前者声明名为 `airoute` 的自定义 Provider 和可选路由别名，后者只设置 `defaultProvider`、`defaultModel`。首个版本不编写 Pi Extension、不触碰项目内 `.pi/settings.json`，也不绕过网关直接读取上游密钥。这样能保留 AI Router 的认证、路由、协议转换、审计和配额能力，并与现有 Claude Code、Claude App、Codex、MiMo Code 的“预览-备份-写入-验证-回滚”生命周期一致。

本文中的“Pi”特指 Pi Coding Agent，而不是 Inflection Pi、Raspberry Pi 或其他同名产品。若目标产品不同，本计划的文件路径和 Provider 模型不适用，应先重新确认集成对象。

## 研究范围和现有约束

AI Router 当前是 Go 单二进制的本地网关。`air serve` 分别监听模型网关和 Web 管理台；网关接收 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini Generate Content 四类请求，将它们解码成统一 IR，依据路由规则选择上游，再把结果转换回调用方协议。管理台中的“应用配置”不是泛化的配置编辑器，而是一组有明确所有权边界的本地文件适配器。

这一边界决定了 Pi 接入的评价标准并非“能生成一段 JSON”，而是以下六件事同时成立：

1. 用户可在管理台发现 Pi、看见实际配置路径和是否已同步。
2. 写入前能看到不泄露密钥的预览与差异；写入后保留原有 Provider、Pi 设置和用户扩展。
3. 接入凭证是 AI Router 的客户端访问密钥，所有模型调用仍穿过 `:12666` 网关；不得复制某个上游 Provider 的 API Key。
4. 选择的 Pi API 类型与 AI Router 的请求入口一致，路由中的 `match.protocol` 不能被错误绕过。
5. `models.json` 与 `settings.json` 两个文件的变更可一起备份、失败可恢复、可随时回滚。
6. 测试覆盖普通 JSON、Pi 支持的 JSONC、首次写入、已有多 Provider、配置损坏、所有协议以及受管理凭证脱敏。

当前应用层的契约已经为此准备了大部分基础：`application.Adapter` 统一要求 `Detect`、`Read`、`Preview`、`Apply`、`Verify`、备份删除和回滚；可选接口提供手动编辑和清理。Admin API 已将这些能力暴露为 `/api/apps/{id}` 系列端点，前端以 manifest 驱动标签页。Pi 不需要新建一套后端路由或单独的前端页面，而应实现同一个适配器契约。

本文采用横纵分析：纵轴追踪 Pi 从固定 Provider 到可配置模型目录、动态 Provider 与近期版本演进的过程；横轴把它与 AI Router 现有四个应用适配器、四种网关协议和配置生命周期并置比较。结论以“既有配置所有权与安全边界”为约束，而不是仅以 Pi 能否发出 HTTP 请求为准。

## 纵向分析：Pi 的配置能力为什么适合做原生适配器

Pi 的早期使用方式主要依赖内置 Provider 和环境变量。随着其成为可扩展的编码代理，项目逐步将“选模型”和“写 Provider”从固定实现中抽离出来：用户可以通过 `models.json` 注册自定义模型、代理和本地推理服务；模型级别可覆盖 API 类型和地址；Provider 可改由 Extension 提供认证或动态发现。这个方向的重点不是提供一个不透明的代理开关，而是让用户对模型目录与传输协议保有可见、可编辑的文本配置。

截至本次调研，Pi 的全局数据目录默认是 `~/.pi/agent`，可由 `PI_CODING_AGENT_DIR` 重定向。该目录中的 `models.json` 是自定义 Provider/模型目录，`settings.json` 保存默认 Provider 和默认模型等全局偏好；项目目录中的 `.pi/settings.json` 会覆盖全局 settings。Pi 在模型选择器打开时刷新 `models.json`，因此 AI Router 写完配置后通常不需要重启 Pi 才能在 `/model` 中看到新模型。Pi 的近期版本仍快速演进：2026-08-07 发布了 v0.84.1，之前的 v0.82 系列已修复模型选择器重新加载 `models.json` 的行为。这说明文件格式和可选字段应以 Pi 当前文档为准，不能把一次性的版本假设编进网关核心。

Pi 的自定义 Provider 可以声明四种与本项目直接重合的 API 类型：`openai-completions`、`openai-responses`、`anthropic-messages`、`google-generative-ai`。Provider 层可给出默认 API，模型层可单独覆盖；`baseUrl`、`apiKey` 与 headers 支持字面量、环境变量插值和 `!command`。这使 Pi 能够以标准配置访问 AI Router 的协议入口，无需实现 Extension。只有以下情况才应进入 Extension 路径：自定义 OAuth、非标准流协议、动态远程模型目录，或 Pi/网关某一侧标准 SSE 行为不兼容。

从 AI Router 的历史看，应用适配器已从简单环境变量写入发展为带数据保留和验证的受控配置管理：Claude Code 更新 `settings.json` 中的环境变量；Claude App 管理其多文件配置；Codex 同时维护 TOML 与模型目录并打包备份；MiMo Code 在 JSON 的 Provider 集合中只管理 `airoute` 节点。Pi 与 MiMo Code 同为 JSON Provider 目录，和 Codex 同为“Provider 声明加默认模型”的双文件形态。因此它不能仅复制 MiMo Code：MiMo 的单一配置文件和 OpenAI Chat 假设不足以覆盖 Pi 的协议选择和默认设置；更合适的模板是“MiMo 的 Provider 合并策略 + Codex 的双文件快照/补偿恢复策略”。

## 横向分析：Pi 与现有四类应用的差异

| 应用 | 当前受管位置 | 调用协议 | 配置特性 | Pi 接入可借鉴之处 |
|---|---|---|---|---|
| Claude Code | `~/.claude/settings.json` | Anthropic | 仅受管 `env` 键，保留 Hook、插件及其他设置 | JSON 合并、CLI 探测、网关连通性验证 |
| Claude App | 多个本地 JSON 文件 | Anthropic | 一次配置涉及多个文件，拥有专用 bundle 备份 | 文件快照、失败补偿、桌面应用重启提示 |
| Codex | `~/.codex/config.toml` + 模型目录 | OpenAI Responses | 需要模型目录、可走 command-backed 认证 | 双文件事务语义、模型别名目录、严格预检 |
| MiMo Code | XDG 下单个 JSON | OpenAI Chat | 仅管理 `provider.airoute`，合并其他 Provider | Provider 节点所有权、JSON 预览与清理 |
| Pi Coding Agent | `~/.pi/agent/models.json` + `settings.json` | 四种标准 API | `models.json` 可 JSONC，项目 settings 会覆盖全局 | 双文件快照、Provider 节点管理、协议显式选择 |

Pi 的关键差别在三个地方。

第一，它不是只认一种 wire protocol 的客户端。把 Pi 固定为 `openai-completions` 的确是最短路径，也能覆盖绝大多数代理，但会让 AI Router 的 Responses、Anthropic、Gemini 入口在 Pi 场景中变成隐式转换，用户无法借由路由的 `match.protocol` 控制调用路径。正确的用户体验应是“在配置时选择 Pi 请求协议”，然后由模型目录写入匹配的 Pi API 类型；路由选择器只显示协议为空或等于该选择的别名。

第二，Pi 配置的全局层与项目层存在覆盖关系。AI Router 应管理全局目录，因为这与现有管理台“为本机应用设置可复用连接”的定位一致，也不会在用户工作仓库中创建可能受信任提示影响的 `.pi/settings.json`。管理台需如实提示：项目内 `.pi/settings.json` 的默认模型、Provider 和资源可能覆盖全局设置；“已同步”只表示全局 AI Router Provider 和默认值正确写入，不能保证每个仓库都没有覆盖。

第三，Pi 的 `models.json` 已支持注释和尾逗号。现有 Go 适配器普遍使用 `encoding/json`，直接复制会把合法 Pi 配置误判为损坏，或在重写时静默丢失注释。这个差异不是边角问题：Pi 用户常把模型目录作为手工维护的目录。实现开始前须选择能保留 JSONC 语义的解析/重写方案，至少保证注释与尾逗号输入可被读取、未受管 Provider 的值完全保留，并在不能无损处理时停止写入、给出可操作错误，而不是退化为“格式化后覆盖”。

## 推荐的产品定义

### 受管对象和所有权

适配器 ID 定为 `pi`，显示名为“Pi Coding Agent”，配置格式显示为 `jsonc`。它受管两个位置：

* `${PI_CODING_AGENT_DIR:-~/.pi/agent}/models.json` 中 `providers.airoute` 节点；
* 同目录 `settings.json` 中 `defaultProvider` 与 `defaultModel`，且只在它们当前仍等于 AI Router 上一次写入值时由清理操作删除。

为便于测试和受控部署，适配器可额外接受 `AIROUTE_PI_AGENT_DIR` 和 `AIROUTE_PI_EXECUTABLE`。路径优先级应为显式 adapter 注入路径、`AIROUTE_PI_AGENT_DIR`、Pi 官方的 `PI_CODING_AGENT_DIR`、默认用户目录。运行 Pi 时真正生效的是官方环境变量，因此任何“使用自定义目录”的 UI 提示都应明确给出相同变量，不能只让 AI Router 自己使用私有覆盖。

`models.json` 只创建或替换 `providers.airoute`，绝不改动其他 Provider。`settings.json` 只添加默认 Provider/模型，不接管 theme、prompt、packages、skills、extensions、session、trust 或 telemetry 设置。`auth.json` 完全不读取、不修改；AI Router 的接入凭证不能混入 Pi 登录态。

### 配置模型

管理台沿用现有“AI Router 地址、受管理访问密钥、默认模型”的表单，并为 Pi 增加一个“请求协议”选择器。第一版将该选择器做成必填项，默认 OpenAI Chat。用户选择后，路由下拉仅展示以下两类 alias：未指定 `match.protocol` 的通用路由，或精确匹配所选协议的路由。一个 `models.json` 中的 `airoute` Provider 注册这些 alias，使用户还能在 Pi 的 `/model` 选择器中切换同协议路由；表单选择的 alias 写入 `defaultModel`。

协议映射应由单一函数维护：

| AI Router 客户端协议 | Pi `api` | Pi `baseUrl` | AI Router 入口 |
|---|---|---|---|
| `openai-chat` | `openai-completions` | `{gateway}/v1` | `/v1/chat/completions` |
| `openai-responses` | `openai-responses` | `{gateway}/v1` | `/v1/responses` |
| `anthropic-messages` | `anthropic-messages` | `{gateway}` | `/v1/messages` |
| `gemini-generate-content` | `google-generative-ai` | `{gateway}/v1beta` | `/v1beta/models/{model}:streamGenerateContent` |

这张表必须以真实 Pi 版本的请求测试锁定，而不是仅凭 URL 拼接推断。AI Router 同时接受带/不带 `/v1` 的 OpenAI 与 Anthropic 路径，Gemini 路由则按 `/models/…` 模式识别；表中的 Base URL 选择旨在贴合 Pi 适配器通常追加的路径。若 Pi 某版本的实际请求路径不同，应只调整映射表和测试，不改变网关的协议识别。

建议由 Pi 使用最小模型定义，例如 `{ "id": "coding" }`，把模型显示名、成本、上下文、图像和 reasoning 元数据留给后续“模型能力目录”能力。Pi 为省略字段提供默认值，而 AI Router 当前的 route schema 没有可信的模型上下文窗口、最大输出或图像能力字段。先伪造这些数据会让模型选择器显示比真实能力更强的承诺。

凭证采用当前应用配置页已经提供的“AI Router 访问密钥”选择机制。解析后把客户端 key 写入 `providers.airoute.apiKey`，文件权限固定为 `0600`，读回与预览遵循管理台的 web redaction 规则。不要使用现有 `air provider-token`：该命令的目的在于让 Codex 直接获取上游 Provider token；如果在 Pi 中使用它，会绕过 AI Router 的路由、客户端策略、可观测性与密钥隔离，和本功能目标相反。Pi 支持 `!command` 是未来改善本地凭证暴露面的候选机制，但需要另行设计仅返回托管客户端 key 的安全命令，不能复用上游 token 命令。

### 预览、应用、清理与回滚

读取时，`State.Path` 指向 `models.json`，`Managed` 同时披露 `base_url`、`api`、`model`、`models` 和 settings 中已受管的默认值；`PreservedFields` 分别统计两个文件中保留的顶层项并合并显示。预览内容使用一个显式 bundle，例如 `{ "models.json": {…}, "settings.json": {…} }`，差异按文件名分段。这样现有右侧预览/手动编辑 UI 仍可复用，且用户不会误以为只写了一个文件。

应用顺序是：解析并验证两个下一版本 → 创建包含两个原始文件存在性和内容的私有 bundle 备份 → 原子写入 `models.json` → 原子写入 `settings.json` → 重读确认 `providers.airoute`、API、Base URL 与默认值。第二步之后任一写入失败，都从内存快照恢复已变动文件；恢复也失败时返回包含恢复失败详情的错误，保留 bundle 供手动处理。备份沿用 `.airoute.bak.` 命名、最多保留十份，并标记为包含敏感配置。

清理不删除整个 Pi 目录或文件。它仅移除 `providers.airoute`；若 `defaultProvider == "airoute"`，再移除该值；若 `defaultModel` 仍为由适配器最后写入的 alias，才移除默认模型。此前用户手工切换到另一个 Provider/模型时，清理必须保留其选择。回滚恢复完整 bundle，首次写入前不存在的文件会被删除，存在的文件按原内容和权限恢复。

## 实施计划

### 阶段 0：契约尖峰与决策冻结（约 1-2 天）

目标不是写入产品代码，而是消除两项版本相关的不确定性。

1. 在隔离临时目录安装/调用当前 Pi，针对四种 `api` 创建最小 `models.json`，以记录型 HTTP test server 断言 Pi 的请求路径、认证头、SSE 接受头和模型字段；验证上表中的 Base URL。
2. 用带行注释、块注释、尾逗号、未知 Provider、未知顶层字段的 `models.json` 试验解析与重写。选定 JSONC 库或最小编辑策略，并把“未受管字段、注释、尾逗号”作为验收样例。
3. 明确 Pi 支持基线：建议以检测到的当前主版本为准，并在 manifest/验证结果中提示“已针对 Pi x.y 测试”。若安装版本低于 custom models 支持版本，适配器仍可显示，但 Apply 返回清晰的升级建议。

完成条件：四种路径实测结果、JSONC 方案、最低支持版本和 fallback 策略记录在适配器测试说明中。没有这一阶段的结果，不开始批量实现。

### 阶段 1：后端 Pi 适配器（约 2-3 天）

新增 `internal/application/pi/adapter.go` 与 `adapter_test.go`，实现完整 `application.Adapter`，并支持 `RawConfigAdapter`、`CleanupAdapter`、`ConfigurationStatusReader`。重点函数包括：

* `agentDir()`、`modelsPath()`、`settingsPath()`：遵循路径优先级并避免解析项目 `.pi` 目录；
* `Detect()`：通过 `pi --version` 在三秒超时内检测可执行文件，识别“命令存在但版本读取失败”；
* `Read()`：读取 JSONC 模型目录和 JSON settings，提取受管 Provider 状态并计算 `Synced`；
* `decodeDesired()`：校验网关地址、访问密钥、默认模型和请求协议，调用单一 `wireConfig()` 得出 Pi API 与 Base URL；
* `compose()`：深拷贝/合并 `providers.airoute`，生成有文件名边界的 bundle preview；
* `Apply()`、`ApplyRaw()`、`Cleanup()`、`Backups()`、`Rollback()`：实施双文件快照和补偿恢复；
* `Verify()`：检查安装与配置完整性，再使用所选 wire protocol 向 AI Router 发送一个最小流式请求。验证成功的含义是“Pi 会使用的协议入口、客户端 key 和当前 alias 可联通”，不执行会写文件的 Pi agent 任务。

在 `internal/admin/admin.go` 的 `application.NewRegistry(...)` 注册 `pi.New()`。不需要新增 Admin endpoint，因为现有 `/api/apps/{id}` 可自动路由到 adapter。为适配器加入与 Codex 一样的 bundle 备份格式私有常量，切勿让两个不同应用共享可误认的备份格式。

阶段 1 的单元测试至少覆盖：首次应用；保留两个文件中不受管字段；更新同一 provider 的幂等性；JSONC 文件；无效 JSON/JSONC 不写入；原子写失败补偿；`ConfigurationSynced`；清理条件；bundle 回滚；备份删除路径校验；密钥不出现在错误/日志；四种协议的 `Verify()` 请求；`PI_CODING_AGENT_DIR` 路径解析；`pi --version` 成功、缺失和超时。

### 阶段 2：应用配置页的协议意识（约 1-2 天）

修改 `web/src/pages/ApplicationsPage/ApplicationsPage.tsx`、`web/src/lib.ts` 和关联 types/tests：

1. 将 `pi` 加入标签排序和视觉表单白名单；初始说明写明“Pi 通过 models.json 注册 AI Router Provider”。
2. 给 form 增加 `wire_protocol`，显示“OpenAI Chat / OpenAI Responses / Anthropic Messages / Gemini”选择；默认 OpenAI Chat。
3. 让 `applicationRouteOptions` 接受可选 protocol 参数。对于 Pi，仅允许 protocol 为空或与选择值相同的精确别名；切换协议后如当前 model 不再可用，回落到新列表第一个 alias。
4. 将当前“模型设置”改为“Pi 模型目录”：默认模型用于 Pi 启动，所有已筛选 alias 会写入 Pi 的 `/model` 选择器。页面同时提示设置作用于全局 Pi 配置，项目 `.pi/settings.json` 可以覆盖全局默认值。
5. 扩展预览文本与手动编辑校验，使 Pi 的双文件 bundle 可读、可编辑、可由 `ApplyRaw` 验证；不会把原来的单文件适配器卷入特殊分支。
6. 保存/验证/清理提示应明确是 `models.json` 和 `settings.json`，以及退出/重新打开模型选择器即可刷新目录。

阶段 2 的前端单测覆盖 protocol-to-Pi API 映射、route filter、默认模型回退和 Pi 标签排序。Playwright 用例应新增 Pi 标签，验证可选路由随协议变化、预览脱敏、保存后出现 bundle 备份、恢复与清理成功、UI 对项目配置覆盖风险有提示。

### 阶段 3：端到端兼容与安全验收（约 2-3 天）

扩展 Go 网关测试和 Web E2E fixture，让 Pi adapter 的 Verify 对每种协议都走一次模拟上游：

* OpenAI Chat：工具调用与 SSE 文本增量；
* OpenAI Responses：reasoning/tool 生命周期与终止事件；
* Anthropic：system、tool use、tool result、thinking 可移植性诊断；
* Gemini：模型路径注入、流式事件、图像/文本请求。

测试不应假设所有上游模型具备 reasoning、图像或工具能力；它验证的是 Pi 到网关这一段的请求结构、授权、路由匹配和错误表现。对于协议转换发生字段降级的场景，断言 AI Router 返回可解释的诊断，而不是让 Pi 配置层虚构兼容性。

安全验收包括：管理员 API 在 web redaction 开启时永不返回 `apiKey`；预览、diff、CLI 检测错误、HTTP 验证错误和测试日志都不包含访问密钥；写入权限为 `0600`；恶意 backup 名称、符号链接和路径穿越被拒绝；外部项目 `.pi/settings.json` 永不写入；Cleanup 不删除用户的 Pi Provider；无上游 API key 会被写入 Pi 配置的检查。

### 阶段 4：文档、发布和维护（约 1 天）

更新中英文 README 或应用配置文档，给出最小可用 Pi 配置、支持的协议表、配置文件位置、项目 settings 覆盖规则、密钥处理规则和排障方式。添加 release note，写清这是 Pi Coding Agent 自定义 Provider 集成而非 Pi OAuth 登录集成。发布前执行 `make check`、`make web-e2e`、手工 Pi 验收脚本，并用当前稳定 Pi 版本复测。

建议建立一条轻量的兼容性维护规则：每次 Pi 大版本更新或 `models.json` schema/API 类型变化时，先重跑阶段 0 的请求快照；若仅新增可选字段，不改适配器；若 Base URL 或 JSONC 语义改变，更新映射与 fixture 后再声明支持。不要把 Pi 仓库的快速迭代扩散到网关运行路径。

## 交付物和验收标准

| 交付物 | 验收标准 |
|---|---|
| Pi adapter | 列表中出现 `pi`；Detect/Read/Preview/Apply/Verify/Backups/Rollback/Cleanup 完整可用 |
| 双文件配置 | 非受管 Provider/settings 保留；任一写入失败会恢复两个文件；回滚还原首次写入前状态 |
| 协议映射 | 四种 Pi API 类型有实测请求路径 fixture；选择器和路由过滤保持一致 |
| 凭证处理 | 只写 AI Router client key；所有 UI/API 预览按设置脱敏；不调用 `provider-token` |
| JSONC | 含注释/尾逗号的合法 Pi 目录可安全处理，或阻止写入并说明原因；不得静默抹除内容 |
| 用户体验 | Pi 排在应用标签页中；可选择协议与默认模型；能看懂双文件预览和项目覆盖提示 |
| 回归质量 | 新增 Go 单测、前端单测、Playwright 覆盖；`make check` 与 `make web-e2e` 均通过 |

## 风险、取舍与后续方向

最大风险不是协议转换，而是配置所有权。若把 Pi 当作“只有一个 JSON 文件的 OpenAI 客户端”，实现会很快，但用户最常见的自定义 Provider、默认模型和 JSONC 注释会在保存时被破坏。双文件 bundle 与 JSONC 尖峰会增加几天工作量，却把不可逆的本机配置损坏从线上风险前移为可测试问题。

第二个风险是模型元数据。Pi 可在模型目录记录 reasoning、图片、上下文窗口、成本和兼容 flags；AI Router 当前路由并不可靠地拥有这些事实。第一版采用最小模型对象，把端到端能力交给路由和 Provider 探测，避免“管理台显示支持、实际模型拒绝”的落差。后续若要管理这些字段，应先为 Provider/route 建立可验证的 capability schema，再在 Pi UI 暴露高级模型元数据。

第三个风险是凭证可见性。将受管理 client key 写入 Pi 的私有配置与当前四个适配器的行为一致，但它仍是一份应用侧静态密钥。Pi 的 `!command` 支持给出了更严格的未来路径：新增一个只面向本地受管理客户端的短期/可撤销 token 命令，Pi 在请求时解析它；该设计必须包含调用方限制、缓存、失效策略和审计，不能把 Codex 用的上游 credential 命令照搬过来。因此它不属于首个版本。

最可能的演进是：阶段 1-2 以全局 AI Router Provider 和统一协议目录为主，Pi 立即可用；阶段 3 稳定四协议兼容；随后根据真实用户需求引入 per-model 元数据与 command-backed client token。最危险的演进是跳过 JSONC/双文件恢复、只实现 OpenAI Chat，短期演示成功后在用户已有 Pi 目录上丢失配置。最乐观的演进是协议映射和 capability schema 成熟后，将同一模型目录生成能力抽象为多个 CLI agent 的共享组件；但在当前应用数量和配置格式差异下，过早抽象只会让清理、备份和安全语义变得模糊。

## 最终建议

批准以“Pi Coding Agent 全局 custom-provider 适配器”为范围启动；先完成阶段 0，再实施阶段 1-2。首发不要做 Pi Extension、OAuth、项目级 `.pi` 写入、上游直连模式或模型能力编辑器。这个范围与 AI Router 当前架构最契合，能让 Pi 使用网关的所有核心价值，同时将配置风险限制在清晰、可回滚的受管节点内。

## 信息来源

1. Pi 官方自定义模型文档：<https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md>（访问于 2026-08-10）。说明 `~/.pi/agent/models.json`、四类 API、Provider/模型字段、环境变量与 `!command` 值解析。
2. Pi 官方 Settings 文档：<https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/settings.md>（访问于 2026-08-10）。说明全局与项目 settings 的覆盖、`defaultProvider`、`defaultModel` 和 trust 语义。
3. Pi 官方 Custom Providers 文档：<https://pi.dev/docs/latest/custom-provider>（访问于 2026-08-10）。说明标准配置足够时无需 Extension，以及 Extension 适用的动态认证/流式场景。
4. Pi 官方使用文档：<https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/usage.md>（访问于 2026-08-10）。说明 `PI_CODING_AGENT_DIR`、`pi --provider`、`--model` 和 print mode。
5. Pi 官方 Releases：<https://github.com/earendil-works/pi/releases>（访问于 2026-08-10）。用于确认项目处于快速迭代期，并记录当时最新 v0.84.1。
6. AI Router 本地代码：`internal/application/`、`internal/admin/admin.go`、`internal/gateway/gateway.go`、`web/src/pages/ApplicationsPage/ApplicationsPage.tsx`、`web/src/lib.ts`（审阅于 2026-08-10）。
