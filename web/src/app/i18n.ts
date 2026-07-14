export type Locale = "zh-CN" | "en-US";

const zhToEn: Record<string, string> = {
  "中文": "Chinese",
  "英文": "English",
  "语言": "Language",
  "运行概览": "Overview",
  "模型接入": "Models",
  "路由配置": "Routes",
  "应用配置": "Applications",
  "调用日志": "Request Logs",
  "系统设置": "Settings",
  "当前版本": "Current version",
  "可更新到": "Update available:",
  "本地 AI 协议网关": "Local AI Protocol Gateway",
  "运行中": "Running",
  "已关闭": "Stopped",
  "正在准备管理控制台": "Preparing the console",
  "安全管理控制台": "Secure management console",
  "连接 AI Router": "Connect to AI Router",
  "输入管理令牌。令牌只保存在当前浏览器标签页。": "Enter the admin token. It is stored only in this browser tab.",
  "管理令牌": "Admin token",
  "连接": "Connect",
  "正在加载页面…": "Loading page…",
  "配置热加载失败，服务仍使用上一版本：": "Configuration reload failed. The service is still using the previous version: ",
  "查看本地网关的实时请求、Token 消耗和链路性能。": "Monitor requests, token usage, and gateway performance.",
  "网关运行中": "Gateway running",
  "网关已关闭": "Gateway stopped",
  "累计请求": "Total requests",
  "当前进程": "Current process",
  "成功率": "Success rate",
  "次错误": "errors",
  "Token 总消耗": "Total tokens",
  "输入 + 输出": "Input + output",
  "当前并发": "Active requests",
  "正在处理": "In progress",
  "Token 消耗": "Token usage",
  "当前进程累计统计": "Totals for the current process",
  "输入 Token": "Input tokens",
  "输出 Token": "Output tokens",
  "Token 合计": "Total tokens",
  "链路性能": "Performance",
  "请求延迟分位值": "Request latency percentiles",
  "P50 延迟": "P50 latency",
  "P95 延迟": "P95 latency",
  "重试 / 故障切换": "Retries / failovers",
  "模型列表": "Model services",
  "填写 API 地址、密钥和模型名；测试成功后自动识别协议并录入。": "Enter the API URL, key, and model names. The protocol is detected after a successful test.",
  "+ 接入模型": "+ Add model",
  "模型服务": "Model service",
  "协议": "Protocol",
  "API 地址": "API URL",
  "模型": "Models",
  "操作": "Actions",
  "测试": "Test",
  "测 试": "Test",
  "编辑": "Edit",
  "编 辑": "Edit",
  "删除": "Delete",
  "删 除": "Delete",
  "未设置": "Not set",
  "尚未设置密钥": "No key configured",
  "还没有接入模型": "No model services yet",
  "连接正常": "Connected",
  "测试通过": "Test passed",
  "连接测试通过": "Connection test passed",
  "连接测试失败": "Connection test failed",
  "删除模型服务失败": "Failed to delete model service",
  "删除模型服务？": "Delete model service?",
  "删除模型服务": "Delete model service",
  "将删除": "Delete",
  "，并清理引用它的路由目标；失去全部目标的路由会一并删除。": ". Referencing route targets and routes left without targets will also be removed.",
  "。如果路由仍在使用该服务，需要先删除或修改对应路由。": ". If a route still uses this service, delete or update that route first.",
  "接入新模型": "Add model service",
  "编辑模型接入": "Edit model service",
  "配置流程": "Setup steps",
  "填写连接信息": "Connection details",
  "测试并识别": "Test and detect",
  "确认模型": "Confirm models",
  "保存接入": "Save service",
  "选择协议": "Select protocol",
  "模型服务名称": "Service name",
  "请输入模型服务的 API Key": "Enter the model service API key",
  "密钥将明文保存到本机 0600 配置和备份中": "The key is stored in the local 0600 configuration and backups.",
  "模型名": "Model names",
  "qwen3, qwen3-coder（多个用逗号分隔）": "qwen3, qwen3-coder (comma-separated)",
  "测试连接并自动识别协议": "Test connection and detect protocol",
  "正在连接并识别…": "Connecting and detecting…",
  "请先完成连接测试和协议识别": "Test the connection and detect the protocol first.",
  "检测到本机或局域网模型服务": "Local or private network model service detected",
  "该地址只能访问当前电脑或所在局域网，请确认这是你信任的模型服务。": "This address is only reachable from this computer or its local network. Confirm that you trust this model service.",
  "我确认允许 AI Router 访问这个地址": "I allow AI Router to access this address",
  "识别成功：": "Detected: ",
  "真实请求已通过 ·": "Live request succeeded ·",
  "高级设置与识别结果": "Advanced settings and detection results",
  "标识 ID": "Identifier",
  "显示名称": "Display name",
  "识别协议": "Detected protocol",
  "模型配置模板": "Model profile",
  "通用": "Generic",
  "思考模式": "Reasoning mode",
  "跟随客户端": "Follow client request",
  "始终关闭": "Always disabled",
  "始终开启": "Always enabled",
  "超时": "Timeout",
  "确认接入并加入模型列表": "Add model service",
  "检测到本机或局域网地址，请先确认允许 AI Router 访问该模型服务": "A local or private network address was detected. Confirm access before continuing.",
  "确认访问本机或私网模型服务": "Allow access to local or private model service",
  "保存中…": "Saving…",
  "取消": "Cancel",
  "确认": "Confirm",
  "路由列表": "Routes",
  "客户端使用一个简单模型名发起请求，AI Router 再将请求转发到指定的上游模型。": "Clients use a simple model alias, and AI Router forwards the request to the selected upstream model.",
  "+ 添加路由": "+ Add route",
  "客户端模型名": "Client model alias",
  "转发到上游模型": "Upstream model",
  "客户端协议": "Client protocol",
  "调用地址": "Endpoint",
  "所有模型": "All models",
  "所有兼容协议": "All compatible protocols",
  "还没有配置路由": "No routes configured",
  "删除路由失败": "Failed to delete route",
  "删除路由？": "Delete route?",
  "删除路由": "Delete route",
  "将删除客户端模型路由": "Delete client model route",
  "，使用该模型名的请求将不再匹配这条规则。": ". Requests using this model alias will no longer match this route.",
  "创建模型路由": "Create route",
  "编辑模型路由": "Edit route",
  "路由就是一条模型映射规则": "A route maps a client alias to an upstream model",
  "客户端模型名（例如 qwen3） → 实际上游服务与模型": "Client model alias (for example, qwen3) → actual upstream service and model",
  "目标上游模型": "Target upstream model",
  "选择接入模型": "Select model service",
  "客户端模型名（别名）": "Client model alias",
  "例如 coding-model": "For example, coding-model",
  "客户端调用协议": "Client protocol",
  "客户端调用地址": "Client endpoint",
  "请求中的 model 填写": "Set the request model to",
  "，系统会自动转发到上方选择的模型。": ". AI Router will forward it to the selected model.",
  "高级匹配设置": "Advanced matching",
  "路由 ID": "Route ID",
  "流式响应": "Streaming",
  "工具调用": "Tool calls",
  "图片输入": "Image input",
  "任意": "Any",
  "是": "Yes",
  "否": "No",
  "保存路由": "Save route",
  "为本地开发工具维护连接设置和模型角色映射。": "Manage connection settings and model role mappings for local development tools.",
  "将 AI Router 路由安全合并到应用的本机配置。": "Merge AI Router routing safely into the application's local configuration.",
  "个应用 ·": "applications ·",
  "条可用路由": "available routes",
  "配置文件": "Configuration file",
  "已安装": "Installed",
  "Claude Code 已安装": "Claude Code installed",
  "Claude App 已安装": "Claude App installed",
  "Codex 已安装": "Codex installed",
  "MiMo Code 已安装": "MiMo Code installed",
  "未检测到 Claude Code 命令": "Claude Code command not found",
  "未检测到 Codex 命令": "Codex command not found",
  "未检测到 MiMo Code 命令": "MiMo Code command not found",
  "已检测到命令，但版本读取失败": "Command detected, but version lookup failed",
  "未检测到 Claude App": "Claude App not detected",
  "未检测到": "Not detected",
  "配置已同步": "Configuration synced",
  "尚未同步": "Not synced",
  "· 保留现有": "· Preserving",
  "个顶层配置项": "existing top-level fields",
  "连接设置": "Connection",
  "模型角色映射": "Model role mapping",
  "模型设置": "Model settings",
  "默认模型": "Default model",
  "不设置": "Not set",
  "Sonnet 角色": "Sonnet role",
  "Opus 角色": "Opus role",
  "Haiku 角色": "Haiku role",
  "AI Router 地址": "AI Router URL",
  "AI Router 客户端密钥": "AI Router client key",
  "Claude Code 连接到本机 AI Router": "Claude Code connects to the local AI Router",
  "Claude App 通过本机第三方网关连接 AI Router": "Claude App connects to AI Router through its local third-party gateway",
  "Codex 通过 Responses 协议连接 AI Router": "Codex connects to AI Router using the Responses protocol",
  "MiMo Code 通过 OpenAI 兼容协议连接 AI Router": "MiMo Code connects to AI Router using an OpenAI-compatible protocol",
  "留空则保留现有密钥": "Leave blank to keep the current key",
  "可选模型来源于已经保存的路由": "Available models come from saved routes",
  "刷新预览": "Refresh preview",
  "生成中…": "Generating…",
  "验证连接": "Verify connection",
  "验证中…": "Verifying…",
  "备份并写入": "Back up and write",
  "正在写入…": "Writing…",
  "写入前自动备份，不覆盖 Hooks、插件和权限配置。": "Automatically back up before writing without overwriting hooks, plugins, or permission settings.",
  "写入 Claude-3p 独立配置，保存后需重启 Claude App。": "Write the standalone Claude-3p configuration. Restart Claude App after saving.",
  "仅更新 Codex 的 AI Router provider，保留其他 TOML 设置。": "Only update Codex's AI Router provider and preserve other TOML settings.",
  "仅更新 MiMo Code 的 AI Router provider，保留其他 Provider 和设置。": "Only update MiMo Code's AI Router provider and preserve other providers and settings.",
  "配置预览": "Configuration preview",
  "实时更新中…": "Updating live preview…",
  "实时预览暂不可用": "Live preview unavailable",
  "正在生成实时预览…": "Generating live preview…",
  "配置预览视图": "Configuration preview view",
  "当前配置": "Current configuration",
  "合并后配置": "Merged configuration",
  "写入时将创建备份": "A backup will be created",
  "尚无现有配置": "No existing configuration",
  "点击“刷新预览”查看结构化合并结果。": "Click “Refresh preview” to inspect the merged configuration.",
  "查看字段差异": "View field changes",
  "密钥已脱敏": "Key redacted",
  "密钥按原文显示": "Key shown as stored",
  "· 保留": "· Preserved",
  "个顶层字段": "top-level fields",
  "验证结果": "Verification result",
  "运行 Claude Code 完整验证": "Run full Claude Code verification",
  "正在执行…": "Running…",
  "配置备份": "Configuration backups",
  "最近的自动备份可随时恢复": "Recent automatic backups can be restored at any time",
  "份": "backups",
  "· 可能含本机密钥": "· May contain a local key",
  "恢复": "Restore",
  "恢复中…": "Restoring…",
  "删除配置备份？": "Delete configuration backup?",
  "删除备份": "Delete backup",
  "将永久删除备份": "Permanently delete backup",
  "，此操作无法撤销。": ". This action cannot be undone.",
  "写入配置后会在这里显示备份。": "Backups appear here after writing configuration.",
  "运行完整验证？": "Run full verification?",
  "运行完整验证": "Run full verification",
  "恢复应用配置备份？": "Restore application configuration backup?",
  "确认恢复": "Restore",
  "应用链路验证通过。": "Application connection verified.",
  "部分验证未通过，请查看阶段详情。": "Some checks failed. Review the stage details.",
  "调用日志详情": "Request log details",
  "日志详情": "Log details",
  "关闭日志详情": "Close log details",
  "正在加载…": "Loading…",
  "正在读取完整日志…": "Loading full log…",
  "状态": "Status",
  "成功": "Success",
  "失败": "Failed",
  "路由 / 上游": "Route / upstream",
  "耗时 / Token": "Latency / tokens",
  "聊天内容": "Conversation",
  "请求正文": "Request body",
  "响应正文": "Response body",
  "执行详情": "Execution",
  "助手": "Assistant",
  "系统": "System",
  "工具": "Tool",
  "用户": "User",
  "当时未记录正文。": "The body was not recorded.",
  "这条历史日志生成时尚未开启正文记录，无法恢复聊天内容。": "Body capture was disabled when this log was created, so the conversation cannot be recovered.",
  "正文存在，但没有识别到标准聊天消息。可在“请求正文”和“响应正文”中查看原始内容。": "A body exists, but no standard chat messages were detected. View the raw request and response bodies instead.",
  "上游尝试": "Upstream attempts",
  "没有上游尝试记录。": "No upstream attempts recorded.",
  "协议诊断": "Protocol diagnostics",
  "没有协议诊断信息。": "No protocol diagnostics.",
  "时间 / 请求 ID": "Time / Request ID",
  "请求模型": "Requested model",
  "耗时": "Latency",
  "首 Token": "First token",
  "入 /": "in /",
  "出": "out",
  "点击任意一条记录，查看完整聊天内容、原始正文和上游执行过程。": "Select a record to inspect the conversation, raw bodies, and upstream execution.",
  "刷新": "Refresh",
  "关键词": "Keyword",
  "请求 ID、模型、路由、上游": "Request ID, model, route, or upstream",
  "全部状态": "All statuses",
  "结果": "Results",
  "显示": "Showing",
  "暂无调用日志": "No request logs",
  "日志加载失败：": "Failed to load logs: ",
  "完整配置": "Full configuration",
  "所有本地运行配置": "All local runtime configuration",
  "日志记录": "Logging",
  "级别、格式与历史记录": "Level, format, and history",
  "网页脱敏": "Web redaction",
  "管理页面中的敏感信息显示方式": "Control sensitive data visibility in the console",
  "配置正在生效": "Configuration active",
  "配置分组": "Configuration sections",
  "回滚": "Rollback",
  "回滚中…": "Rolling back…",
  "校验": "Validate",
  "校验中…": "Validating…",
  "保存配置": "Save configuration",
  "关闭提示": "Close message",
  "日志持久化": "Persistent logs",
  "将调用日志和聊天正文写入本机 JSONL 文件，服务重启后自动载入。": "Write request logs and conversation bodies to a local JSONL file and reload them after restart.",
  "日志文件路径": "Log file path",
  "留空时保存到配置文件同目录的 airoute-requests.jsonl": "Leave blank to use airoute-requests.jsonl beside the configuration file",
  "文件达到约 10 MB 后自动轮转，保留最近 3 个历史文件。": "Rotate at approximately 10 MB and keep the three most recent files.",
  "记录聊天正文": "Capture conversation bodies",
  "保存请求和响应内容，关闭后日志详情只显示路由、状态、耗时和 Token。": "Store request and response content. When disabled, logs only show routing, status, latency, and tokens.",
  "日志级别": "Log level",
  "内存保留条数": "In-memory entries",
  "聊天正文记录已开启，配置已保存并备份。后续新请求将记录请求和响应正文，已有日志不受影响。": "Conversation body capture is enabled and the configuration was saved with a backup. New requests will record request and response bodies; existing logs are unchanged.",
  "聊天正文记录已关闭，配置已保存并备份。后续新请求将不再记录请求和响应正文，已有日志不受影响。": "Conversation body capture is disabled and the configuration was saved with a backup. New requests will no longer record request or response bodies; existing logs are unchanged.",
  "本地数据说明": "Local data",
  "日志当前只保存在进程内存，重启后清空。": "Logs are currently kept only in process memory and are cleared on restart.",
  "日志会写入本机磁盘。": "Logs are written to local disk.",
  "聊天正文是否记录由上方开关控制。": "Conversation body capture is controlled above.",
  "控制管理网页是否隐藏 API Key、Token、Cookie、密码等敏感字段。": "Control whether the console hides API keys, tokens, cookies, passwords, and other sensitive fields.",
  "开启后": "When enabled",
  "模型列表、应用配置、完整配置和日志详情中的敏感字段显示为占位符。": "Sensitive fields in models, applications, full configuration, and logs are replaced with placeholders.",
  "关闭后": "When disabled",
  "管理网页按原文显示本机保存的密钥，便于直接查看和复制。": "The console shows locally stored keys as-is for viewing and copying.",
  "数据存储": "Data storage",
  "该设置只影响网页展示，不会修改配置文件或持久化日志中的真实内容。": "This setting only affects the console and does not modify configuration files or persisted logs.",
  "当前状态": "Current status",
  "网页脱敏已开启，保存后敏感字段将隐藏。": "Web redaction is enabled. Sensitive fields will be hidden after saving.",
  "网页脱敏已关闭，保存后敏感字段将按原文显示。": "Web redaction is disabled. Sensitive fields will be shown as stored after saving.",
  "系统 JSON 配置": "System JSON configuration",
  "当前显示：": "Currently showing: ",
  "未保存": "Unsaved",
  "已同步": "Synced",
  "行": "lines",
  "字符": "characters",
  "密钥不会被隐藏。": "Keys are not hidden.",
  "回滚系统配置？": "Rollback system configuration?",
  "确认回滚": "Rollback",
  "暂无可回滚的配置备份": "No configuration backups available",
  "已回滚到最近的配置备份": "Rolled back to the latest configuration backup",
  "查看和编辑本机完整运行配置，包括密钥。": "View and edit the complete local runtime configuration, including keys.",
  "网页脱敏已开启，敏感字段使用占位符显示。": "Web redaction is enabled. Sensitive fields are shown as placeholders."
};

const enToZh = Object.fromEntries(Object.entries(zhToEn).map(([zh, en]) => [en, zh]));
const attributes = ["aria-label", "placeholder", "title"];
const dynamicFragments: Array<[string, string]> = [
  ["连接正常", "Connected"],
  ["测试通过", "Test passed"],
  ["测试失败", "Test failed"],
  ["未知状态", "Unknown status"],
  ["Claude Code 已安装", "Claude Code installed"],
  ["Claude App 已安装", "Claude App installed"],
  ["保留现有", "Preserving"],
  ["个顶层配置项", "existing top-level fields"],
  ["可能含本机密钥", "May contain a local key"],
  ["可能含本地密钥", "May contain a local key"],
  ["日志当前只保存在进程内存，重启后清空。", "Logs are currently kept only in process memory and are cleared on restart."],
  ["日志会写入本机磁盘。", "Logs are written to local disk."],
];

export function translateValue(value: string, locale: Locale): string {
  const table = locale === "en-US" ? zhToEn : enToZh;
  const trimmed = value.trim();
  const direct = table[trimmed];
  if (direct) return value.replace(trimmed, direct);
  let translated = value;
  for (const [zh, en] of dynamicFragments) {
    translated = translated.replaceAll(locale === "en-US" ? zh : en, locale === "en-US" ? en : zh);
  }
  value = translated;
  if (locale === "en-US") {
    return value
      .replace(/^共 (\d+) 个模型服务$/, "$1 model services")
      .replace(/^共 (\d+) 条模型路由$/, "$1 routes")
      .replace(/^显示 (\d+) \/ (\d+)$/, "Showing $1 / $2")
      .replace(/^(\d+) 次错误$/, "$1 errors")
      .replace(/^第 (\d+) 次 · /, "Attempt $1 · ")
      .replace(/^(\d+) 入 \/ (\d+) 出$/, "$1 in / $2 out")
      .replace(/^保留 (\d+) 个顶层字段$/, "Preserved $1 top-level fields")
      .replace(/^密钥已脱敏 · 保留 (\d+) 个顶层字段$/, "Key redacted · Preserved $1 top-level fields")
      .replace(/^密钥按原文显示 · 保留 (\d+) 个顶层字段$/, "Key shown as stored · Preserved $1 top-level fields");
  }
  return value
    .replace(/^(\d+) model services$/, "共 $1 个模型服务")
    .replace(/^(\d+) routes$/, "共 $1 条模型路由")
    .replace(/^Showing (\d+) \/ (\d+)$/, "显示 $1 / $2")
    .replace(/^(\d+) errors$/, "$1 次错误")
    .replace(/^Attempt (\d+) · /, "第 $1 次 · ")
    .replace(/^(\d+) in \/ (\d+) out$/, "$1 入 / $2 出")
    .replace(/^Preserved (\d+) top-level fields$/, "保留 $1 个顶层字段")
    .replace(/^Key redacted · Preserved (\d+) top-level fields$/, "密钥已脱敏 · 保留 $1 个顶层字段")
    .replace(/^Key shown as stored · Preserved (\d+) top-level fields$/, "密钥按原文显示 · 保留 $1 个顶层字段");
}

function localizeNode(node: Node, locale: Locale) {
  if (node.nodeType === Node.TEXT_NODE && node.nodeValue) {
    const next = translateValue(node.nodeValue, locale);
    if (next !== node.nodeValue) node.nodeValue = next;
    return;
  }
  if (!(node instanceof Element)) return;
  for (const name of attributes) {
    const value = node.getAttribute(name);
    if (!value) continue;
    const next = translateValue(value, locale);
    if (next !== value) node.setAttribute(name, next);
  }
  const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
  let current = walker.nextNode();
  while (current) {
    localizeNode(current, locale);
    current = walker.nextNode();
  }
  node.querySelectorAll("[aria-label],[placeholder],[title]").forEach((child) => {
    for (const name of attributes) {
      const value = child.getAttribute(name);
      if (!value) continue;
      const next = translateValue(value, locale);
      if (next !== value) child.setAttribute(name, next);
    }
  });
}

export function initialLocale(): Locale {
  const storage = globalThis.localStorage;
  return storage && typeof storage.getItem === "function" && storage.getItem("airoute_locale") === "en-US" ? "en-US" : "zh-CN";
}

export function currentLocale(): Locale {
  return initialLocale();
}

export function applyLocale(locale: Locale) {
  localStorage.setItem("airoute_locale", locale);
  document.documentElement.lang = locale;
  localizeNode(document.body, locale);
  const observer = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === "characterData") localizeNode(record.target, locale);
      record.addedNodes.forEach((node) => localizeNode(node, locale));
      if (record.type === "attributes") localizeNode(record.target, locale);
    }
  });
  observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: attributes });
  return () => observer.disconnect();
}
