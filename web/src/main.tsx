import React, { useCallback, useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Button as AntButton,
  ConfigProvider,
  Space,
  Table,
  Tag,
  Tooltip,
  notification,
  type TableColumnsType,
} from "antd";
import {
  Activity,
  Braces,
  Check,
  ChevronRight,
  CircleAlert,
  Gauge,
  KeyRound,
  Network,
  Play,
  Power,
  RefreshCw,
  RotateCcw,
  Route,
  Save,
  ScrollText,
  Server,
  Settings,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import { parse, stringify } from "yaml";
import { compact, protocolName } from "./lib";
import "./styles.css";
import "./actions.css";

type Page =
  | "apps"
  | "overview"
  | "providers"
  | "routes"
  | "playground"
  | "logs"
  | "settings";
type Status = {
  status: string;
  version: string;
  uptime_seconds: number;
  config_version: string;
  config_error?: string;
  gateway_url: string;
  providers: number;
  routes: number;
  provider_health?: Record<
    string,
    { ok: boolean; latency_ms?: number; status?: number; checked_at?: string }
  >;
  metrics: Metrics;
};
type Metrics = {
  requests: number;
  errors: number;
  in_flight: number;
  retries: number;
  fallbacks: number;
  input_tokens: number;
  output_tokens: number;
  timeouts: number;
  cancellations: number;
  diagnostics: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
};
type Provider = {
  id: string;
  name: string;
  profile?: string;
  protocol: string;
  base_url: string;
  models: string[];
  api_key_set: boolean;
  health?: { ok: boolean; latency_ms?: number; checked_at?: string };
};
type RouteConfig = {
  id: string;
  priority: number;
  match: {
    model?: string;
    protocol?: string;
    stream?: boolean;
    tools?: boolean;
    image?: boolean;
    headers?: Record<string, string>;
  };
  targets: { provider: string; model: string }[];
};
type RouteTableRow = RouteConfig & {
  key: string;
  order: string;
  fallback?: boolean;
};
type AppConfig = {
  providers: Provider[];
  routes: RouteConfig[];
  auth?: { enabled?: boolean };
  default_route?: { targets: { provider: string; model: string }[] };
};
type Log = {
  id: string;
  started_at: string;
  client_protocol: string;
  requested_model: string;
  route_id: string;
  provider_id: string;
  resolved_model: string;
  status: number;
  duration_ms: number;
  first_token_ms?: number;
  usage: { input_tokens?: number; output_tokens?: number };
  error_code?: string;
  diagnostics?: { code: string; message: string }[];
  attempts?: {
    number: number;
    provider_id: string;
    model: string;
    status?: number;
    error?: string;
    duration_ms: number;
  }[];
  request_body?: string;
  response_body?: string;
};

const pages: { id: Page; label: string; icon: React.ElementType }[] = [
  { id: "providers", label: "模型接入", icon: Server },
  { id: "routes", label: "路由配置", icon: Route },
  { id: "apps", label: "应用配置", icon: Braces },
  { id: "settings", label: "系统设置", icon: Settings },
];

function api(path: string, init: RequestInit = {}) {
  const token = sessionStorage.getItem("airoute_token") || "";
  const headers = new Headers(init.headers);
  headers.set("content-type", "application/json");
  if (token) headers.set("authorization", `Bearer ${token}`);
  return fetch(path, { ...init, headers }).then(async (r) => {
    if (r.headers.get("content-type")?.includes("text/event-stream")) {
      const body = await r.text();
      return {
        status: Number(r.headers.get("x-airoute-playground-status") || 200),
        content_type: "text/event-stream",
        body,
        request_id: r.headers.get("x-airoute-request-id"),
      };
    }
    const data = await r.json().catch(() => ({ error: r.statusText }));
    if (!r.ok) throw new Error(data.error || `HTTP ${r.status}`);
    return data;
  });
}
function App() {
  const requestedPage =
    location.hash.slice(1) === "claude" ? "apps" : location.hash.slice(1);
  const initialPage = pages.some((item) => item.id === requestedPage)
    ? (requestedPage as Page)
    : "providers";
  const [page, setPage] = useState<Page>(initialPage);
  const [status, setStatus] = useState<Status | null>(null);
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [yaml, setYaml] = useState("");
  const [hash, setHash] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [switching, setSwitching] = useState(false);
  const [token, setToken] = useState(
    sessionStorage.getItem("airoute_token") || "",
  );
  const load = useCallback(async () => {
    setError("");
    try {
      const [s, c, p] = await Promise.all([
        api("/api/status"),
        api("/api/config"),
        api("/api/providers"),
      ]);
      setStatus(s);
      setConfig({ ...c.config, providers: p.providers });
      setYaml(c.yaml);
      setHash(c.hash);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    load();
    const id = setInterval(() => {
      api("/api/status")
        .then(setStatus)
        .catch(() => {});
    }, 5000);
    return () => clearInterval(id);
  }, [load]);
  function navigate(p: Page) {
    setPage(p);
    location.hash = p;
  }
  async function toggleRuntime() {
    const enabled = status?.status !== "running";
    if (
      !enabled &&
      !confirm("关闭后将暂停所有 AI 转发请求，管理页面仍可使用。确认关闭？")
    )
      return;
    setSwitching(true);
    try {
      await api("/api/runtime", {
        method: "PUT",
        body: JSON.stringify({ enabled }),
      });
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSwitching(false);
    }
  }
  if (loading)
    return (
      <div className="boot">
        <div className="mark">AR</div>
        <span>正在准备管理控制台</span>
      </div>
    );
  if (error && error.toLowerCase().includes("unauthorized"))
    return <Login token={token} setToken={setToken} retry={load} />;
  return (
    <div className="shell">
      <aside>
        <div className="brand">
          <div>
            <strong className="brand-wordmark">AI Router</strong>
          </div>
        </div>
        <nav>
          {pages.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              className={page === id ? "active" : ""}
              onClick={() => navigate(id)}
            >
              <Icon size={17} />
              <span>{label}</span>
              {page === id && <ChevronRight size={14} />}
            </button>
          ))}
        </nav>
        <button
          className={`side-status ${status?.status === "running" ? "is-running" : "is-stopped"}`}
          onClick={toggleRuntime}
          disabled={switching}
          title={status?.status === "running" ? "关闭 AI 转发" : "启动 AI 转发"}
        >
          <span className="dot" />
          <div>
            <b>
              {switching
                ? "切换中"
                : status?.status === "running"
                  ? "运行中"
                  : "已关闭"}
            </b>
            <small>
              {status?.status === "running" ? "点击关闭" : "点击启动"}
            </small>
          </div>
          <Power size={15} />
        </button>
      </aside>
      <main>
        <header>
          <div>
            <div className="eyebrow">
              管理控制台 / {pages.find((p) => p.id === page)?.label}
            </div>
            <h1>{pages.find((p) => p.id === page)?.label}</h1>
          </div>
          <button className="icon-button" onClick={load} title="刷新">
            <RefreshCw size={17} />
          </button>
        </header>
        {error && (
          <div className="notice error">
            <CircleAlert size={16} />
            {error}
          </div>
        )}
        {status?.config_error && (
          <div className="notice error">
            <CircleAlert size={16} />
            配置热加载失败，服务仍使用上一版本：{status.config_error}
          </div>
        )}
        {page === "apps" && (
          <ApplicationConfigPage status={status} config={config} />
        )}
        {page === "providers" && (
          <Providers
            data={config?.providers || []}
            yaml={yaml}
            hash={hash}
            changed={(y, h) => {
              setYaml(y);
              setHash(h);
              load();
            }}
          />
        )}{" "}
        {page === "routes" && (
          <>
            <Routes
              data={config?.routes || []}
              fallback={config?.default_route?.targets || []}
              providers={config?.providers || []}
              gateway={status?.gateway_url || "http://127.0.0.1:8080"}
              yaml={yaml}
              hash={hash}
              changed={(y, h) => {
                setYaml(y);
                setHash(h);
                load();
              }}
            />
          </>
        )}
        {page === "settings" && (
          <ConfigEditor
            yaml={yaml}
            hash={hash}
            status={status}
            onSaved={(y, h) => {
              setYaml(y);
              setHash(h);
              load();
            }}
          />
        )}
      </main>
    </div>
  );
}

function Login({
  token,
  setToken,
  retry,
}: {
  token: string;
  setToken: (v: string) => void;
  retry: () => void;
}) {
  return (
    <div className="login">
      <div className="login-card">
        <div className="mark">AR</div>
        <div className="eyebrow">安全管理控制台</div>
        <h1>连接 AI Router</h1>
        <p>输入管理令牌。令牌只保存在当前浏览器标签页。</p>
        <label>
          管理令牌
          <input
            type="password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                sessionStorage.setItem("airoute_token", token);
                retry();
              }
            }}
          />
        </label>
        <button
          className="primary"
          onClick={() => {
            sessionStorage.setItem("airoute_token", token);
            retry();
          }}
        >
          <KeyRound size={16} />
          连接
        </button>
      </div>
    </div>
  );
}

function ClaudeCodeSetup({
  status,
  config,
}: {
  status: Status | null;
  config: AppConfig | null;
}) {
  const aliases = useMemo(
    () =>
      (config?.routes || [])
        .map((route) => route.match.model)
        .filter(
          (model): model is string =>
            typeof model === "string" &&
            model.length > 0 &&
            !/[?*\[]/.test(model),
        ),
    [config],
  );
  const preferredModel = aliases.includes("mimo")
    ? "mimo"
    : aliases.includes("qwen3")
      ? "qwen3"
      : aliases[0] || "qwen3";
  const [model, setModel] = useState(preferredModel);
  const [copied, setCopied] = useState("");
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{
    ok: boolean;
    message: string;
    detail?: string;
  } | null>(null);

  useEffect(() => {
    if (!aliases.includes(model)) setModel(preferredModel);
  }, [aliases, model, preferredModel]);

  const gateway = status?.gateway_url || "http://127.0.0.1:8080";
  const clientKey = config?.auth?.enabled
    ? "<你的 AI Router 客户端密钥>"
    : "airoute-local";
  const route = config?.routes?.find((item) => item.match.model === model);
  const target = route?.targets?.[0];
  const provider = config?.providers?.find(
    (item) => item.id === target?.provider,
  );
  const claudeSettings = JSON.stringify({
    env: {
      ANTHROPIC_BASE_URL: gateway,
      ANTHROPIC_API_KEY: clientKey,
    },
  });
  const oneOffCommand = `claude --settings '${claudeSettings}' --model "${model}"`;
  const exportCommand = `export ANTHROPIC_BASE_URL="${gateway}"
export ANTHROPIC_API_KEY="${clientKey}"
claude --model "${model}"`;

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      window.setTimeout(() => setCopied(""), 1600);
    } catch {
      setCopied("error");
    }
  }

  async function verify() {
    setTesting(true);
    setTestResult(null);
    try {
      const envelope = await api("/api/playground/request", {
        method: "POST",
        body: JSON.stringify({
          protocol: "anthropic-messages",
          stream: false,
          body: {
            model,
            max_tokens: 64,
            thinking: { type: "disabled" },
            messages: [
              { role: "user", content: "请只回复：CLAUDE_CODE_READY" },
            ],
          },
        }),
      });
      let responseText = "";
      try {
        const parsed = JSON.parse(envelope.body || "{}");
        responseText = Array.isArray(parsed.content)
          ? parsed.content
              .filter((block: any) => block?.type === "text")
              .map((block: any) => block.text)
              .join("")
          : parsed.error?.message || parsed.message || "";
      } catch {
        responseText = envelope.body || "";
      }
      if (Number(envelope.status) < 200 || Number(envelope.status) >= 300) {
        throw new Error(responseText || `上游返回 HTTP ${envelope.status}`);
      }
      setTestResult({
        ok: true,
        message: "Claude Code 协议链路验证成功",
        detail: `${route?.id || model} → ${target?.provider || "上游"}/${target?.model || model}${envelope.request_id ? ` · 请求 ${envelope.request_id}` : ""}${responseText ? ` · 回复：${responseText}` : ""}`,
      });
    } catch (error) {
      setTestResult({
        ok: false,
        message: "链路验证失败",
        detail: (error as Error).message,
      });
    } finally {
      setTesting(false);
    }
  }

  return (
    <div className="claude-setup">
      <section className="claude-hero">
        <div className="claude-hero-copy">
          <div className="hero-badge">
            <span /> 本地网关已就绪
          </div>
          <h2>
            <span>Claude Code</span>
            <br />
            接入你的任意模型
          </h2>
          <p>
            保留完整的 Claude Code 使用方式。AI Router 在本地接收 Anthropic
            请求，自动完成模型路由与协议转换。
          </p>
        </div>
        <div className="claude-flow" aria-label="Claude Code 调用链路">
          <span>Claude Code</span>
          <ChevronRight size={15} />
          <b>AI Router</b>
          <ChevronRight size={15} />
          <span>{target?.model || "Qwen / MiMo"}</span>
        </div>
      </section>

      <section className="claude-grid">
        <article className="claude-card model-picker">
          <div className="step-number">1</div>
          <div className="card-heading">
            <small>选择 Claude Code 要使用的模型</small>
            <h3>模型别名</h3>
          </div>
          <label>
            Claude Code 中显示的模型
            <select
              aria-label="Claude Code 模型"
              value={model}
              onChange={(event) => {
                setModel(event.target.value);
                setTestResult(null);
              }}
            >
              {aliases.length ? (
                aliases.map((alias) => (
                  <option key={alias} value={alias}>
                    {alias}
                    {alias === "mimo" ? "（推荐：工具调用）" : ""}
                  </option>
                ))
              ) : (
                <option value="qwen3">qwen3</option>
              )}
            </select>
          </label>
          <div className="model-route">
            <div>
              <span>客户端别名</span>
              <code>{model}</code>
            </div>
            <ChevronRight size={15} />
            <div>
              <span>真实上游</span>
              <code>{provider?.name || target?.provider || "尚未配置"}</code>
              <small>
                {target?.model || "请先创建精确模型路由"}
                {provider?.protocol
                  ? ` · ${protocolName(provider.protocol)}`
                  : ""}
              </small>
            </div>
          </div>
        </article>

        <article className="claude-card command-card">
          <div className="step-number">2</div>
          <div className="card-heading">
            <small>在项目终端中执行</small>
            <h3>启动 Claude Code</h3>
          </div>
          <div className="command-box">
            <div className="terminal-head">
              <span>
                <i /> <i /> <i />
              </span>
              Terminal
            </div>
            <pre>{oneOffCommand}</pre>
            <button onClick={() => copy("once", oneOffCommand)}>
              {copied === "once" ? <Check size={14} /> : <Braces size={14} />}
              {copied === "once" ? "已复制" : "复制命令"}
            </button>
          </div>
          <details>
            <summary>当前 Claude 配置没有地址覆盖时，也可使用环境变量</summary>
            <div className="command-box compact-command">
              <pre>{exportCommand}</pre>
              <button onClick={() => copy("export", exportCommand)}>
                {copied === "export" ? "已复制" : "复制 export 命令"}
              </button>
            </div>
          </details>
          {copied === "error" && (
            <small className="copy-error">复制失败，请手动选择命令。</small>
          )}
        </article>

        <article className="claude-card verify-card">
          <div className="step-number">3</div>
          <div className="card-heading">
            <small>先确认完整转换与上游调用</small>
            <h3>验证 Claude Code 链路</h3>
          </div>
          <p>
            使用 Anthropic Messages
            格式发送一次真实请求，验证路由、协议转换、密钥与上游模型。
          </p>
          <button
            className="primary verify-button"
            onClick={verify}
            disabled={testing}
          >
            <Play size={15} />
            {testing ? "正在验证…" : "验证连接"}
          </button>
          {testResult && (
            <div
              className={`verify-result ${testResult.ok ? "success" : "error"}`}
            >
              {testResult.ok ? <Check size={16} /> : <CircleAlert size={16} />}
              <div>
                <b>{testResult.message}</b>
                <span>{testResult.detail}</span>
              </div>
            </div>
          )}
        </article>
      </section>

      <details className="claude-notes">
        <summary>使用说明与模型兼容性</summary>
        <p>
          真正使用时由 <code>claude</code>{" "}
          进程发出包含会话、工具调用和流式输出的 Anthropic 请求。上面的
          <code>--settings</code> 参数只覆盖连接地址和客户端密钥，会保留你已有的
          Hooks、插件和工具配置。
        </p>
        <ul>
          <li>
            <code>ANTHROPIC_BASE_URL</code> 必须是网关根地址{" "}
            <code>{gateway}</code>，不要追加 <code>/v1</code>。
          </li>
          <li>
            <code>ANTHROPIC_API_KEY</code> 是 AI Router
            的客户端密钥，不是供应商密钥；当前鉴权
            {config?.auth?.enabled ? "已开启" : "未开启，填写任意非空值即可"}。
          </li>
          <li>
            命令显式传入连接设置，可避免用户级 Claude
            配置中的旧地址覆盖当前终端环境变量。
          </li>
          <li>
            Qwen 3.x 用于 Claude Code
            时，建议上游配置为关闭推理模式，并限制合理的最大输出令牌数。
          </li>
          {aliases.includes("mimo") && (
            <li>
              需要稳定使用 Read、Edit、Bash 等工具时，当前配置优先选择
              <code>mimo</code>；<code>qwen3</code>
              可用于代码问答，工具调用效果取决于具体 Qwen 版本与上游实现。
            </li>
          )}
        </ul>
      </details>
    </div>
  );
}

function WorkflowSteps({ active }: { active: 1 | 2 | 3 }) {
  return (
    <div className="workflow-steps" aria-label="配置流程">
      {["模型接入", "路由配置", "应用配置"].map((label, index) => (
        <React.Fragment key={label}>
          <div
            className={
              active === index + 1 ? "active" : active > index + 1 ? "done" : ""
            }
          >
            <span>{index + 1}</span>
            <b>{label}</b>
          </div>
          {index < 2 && <i />}
        </React.Fragment>
      ))}
    </div>
  );
}

function ApplicationConfigPage({
  status,
  config,
}: {
  status: Status | null;
  config: AppConfig | null;
}) {
  const aliases = useMemo(
    () =>
      (config?.routes || [])
        .map((route) => route.match.model)
        .filter(
          (model): model is string =>
            Boolean(model) && !/[?*\[]/.test(model || ""),
        ),
    [config],
  );
  const fallback = aliases[0] || "";
  const gateway = status?.gateway_url || "http://127.0.0.1:8080";
  const [form, setForm] = useState({
    base_url: gateway,
    api_key: config?.auth?.enabled ? "" : "airoute-local",
    model: fallback,
    opus_model: fallback,
    sonnet_model: fallback,
    haiku_model: fallback,
  });
  const [meta, setMeta] = useState({
    path: "~/.claude/settings.json",
    preserved: 0,
  });
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    api("/api/claude-code/config")
      .then((value) => {
        const router = value.router || {};
        const selected = (candidate: string) =>
          aliases.includes(candidate) ? candidate : fallback;
        setMeta({
          path: value.path,
          preserved: value.preserved_fields || 0,
        });
        setForm((current) => ({
          ...current,
          base_url: gateway,
          api_key: router.api_key_set ? "" : current.api_key,
          model: selected(router.ANTHROPIC_MODEL),
          opus_model: selected(router.ANTHROPIC_DEFAULT_OPUS_MODEL),
          sonnet_model: selected(router.ANTHROPIC_DEFAULT_SONNET_MODEL),
          haiku_model: selected(router.ANTHROPIC_DEFAULT_HAIKU_MODEL),
        }));
      })
      .catch((error) => setMessage((error as Error).message));
  }, [aliases, fallback, gateway]);

  const preview = JSON.stringify(
    {
      env: {
        ANTHROPIC_BASE_URL: form.base_url,
        ANTHROPIC_API_KEY: form.api_key || "（保留现有密钥）",
        ANTHROPIC_MODEL: form.model,
        ANTHROPIC_DEFAULT_OPUS_MODEL: form.opus_model || undefined,
        ANTHROPIC_DEFAULT_SONNET_MODEL: form.sonnet_model || undefined,
        ANTHROPIC_DEFAULT_HAIKU_MODEL: form.haiku_model || undefined,
      },
    },
    null,
    2,
  );

  async function save() {
    setSaving(true);
    setMessage("");
    try {
      const result = await api("/api/claude-code/config", {
        method: "PUT",
        body: JSON.stringify(form),
      });
      setMessage(`已写入 ${result.path}，原配置已自动备份。`);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setSaving(false);
    }
  }

  function modelSelect(
    value: string,
    key: "model" | "opus_model" | "sonnet_model" | "haiku_model",
    label: string,
  ) {
    return (
      <label>
        {label}
        <select
          value={value}
          onChange={(event) => setForm({ ...form, [key]: event.target.value })}
        >
          <option value="">不设置</option>
          {aliases.map((alias) => (
            <option key={alias} value={alias}>
              {alias}
            </option>
          ))}
        </select>
      </label>
    );
  }

  return (
    <div className="application-config-page">
      <WorkflowSteps active={3} />
      <section className="application-config-overview">
        <div className="application-overview-copy">
          <span className="application-overview-label">应用配置</span>
          <h2>让本地应用使用 AI Router</h2>
          <p>
            为不同开发工具维护独立的连接设置和模型映射，不需要手动编辑应用配置文件。
          </p>
        </div>
        <div className="application-overview-stats">
          <div>
            <span>已支持应用</span>
            <b>1</b>
          </div>
          <div>
            <span>可用路由</span>
            <b>{aliases.length}</b>
          </div>
          <div>
            <span>连接方式</span>
            <b>本地配置</b>
          </div>
        </div>
      </section>

      <section className="application-config-workbench">
        <div className="application-list-panel">
          <div className="application-list-heading">
            <div>
              <b>应用</b>
              <span>选择要连接 AI Router 的工具</span>
            </div>
            <span className="application-count">1</span>
          </div>
          <button className="application-list-item active" type="button">
            <span className="application-monogram claude">CC</span>
            <span className="application-list-copy">
              <b>Claude Code</b>
              <small>命令行编码助手</small>
            </span>
            <span className="application-supported">已支持</span>
          </button>
          <div className="application-list-empty">
            <span>+</span>
            <p>后续支持的应用会显示在这里，并使用各自独立的配置流程。</p>
          </div>
        </div>

        <div className="application-detail-panel">
          <div className="application-detail-header">
            <div className="application-detail-identity">
              <span className="application-monogram claude">CC</span>
              <div>
                <div className="application-detail-title">
                  <h3>Claude Code</h3>
                  <span>已支持</span>
                </div>
                <p>将 AI Router 路由安全合并到 Claude Code 本机配置。</p>
              </div>
            </div>
            <div className="application-config-path">
              <span>配置文件</span>
              <code>{meta.path}</code>
              <small>保留现有 {meta.preserved} 个顶层配置项</small>
            </div>
          </div>

          {!aliases.length && (
            <div className="application-inline-message error">
              请先在“路由配置”中至少创建一条精确模型路由。
            </div>
          )}

          <div className="application-config-body">
            <div className="application-form-panel">
              <section className="application-form-section">
                <div className="application-section-title">
                  <div>
                    <b>连接设置</b>
                    <span>Claude Code 连接到本机 AI Router</span>
                  </div>
                  <span>01</span>
                </div>
                <div className="application-connection-fields">
                  <label>
                    AI Router 地址
                    <input
                      value={form.base_url}
                      onChange={(event) =>
                        setForm({ ...form, base_url: event.target.value })
                      }
                    />
                  </label>
                  <label>
                    AI Router 客户端密钥
                    <input
                      type="password"
                      value={form.api_key}
                      placeholder="留空则保留现有密钥"
                      onChange={(event) =>
                        setForm({ ...form, api_key: event.target.value })
                      }
                    />
                  </label>
                </div>
              </section>

              <section className="application-form-section">
                <div className="application-section-title">
                  <div>
                    <b>模型角色映射</b>
                    <span>可选模型来源于已经保存的路由</span>
                  </div>
                  <span>02</span>
                </div>
                <div className="application-role-grid">
                  {modelSelect(form.model, "model", "默认模型")}
                  {modelSelect(
                    form.sonnet_model,
                    "sonnet_model",
                    "Sonnet 角色",
                  )}
                  {modelSelect(form.opus_model, "opus_model", "Opus 角色")}
                  {modelSelect(form.haiku_model, "haiku_model", "Haiku 角色")}
                </div>
              </section>

              <div className="application-save-bar">
                <div>
                  <ShieldCheck size={16} />
                  <span>写入前自动备份，不覆盖 Hooks、插件和权限配置。</span>
                </div>
                <button
                  className="primary"
                  disabled={!aliases.length || saving}
                  onClick={save}
                >
                  <Save size={15} />
                  {saving ? "正在写入…" : "备份并写入"}
                </button>
              </div>
              {message && (
                <div
                  className={`application-inline-message ${message.startsWith("已写入") ? "success" : "error"}`}
                >
                  {message}
                </div>
              )}
            </div>

            <div className="application-preview-panel">
              <div className="application-preview-header">
                <div>
                  <b>配置预览</b>
                  <span>settings.json</span>
                </div>
                <span>JSON</span>
              </div>
              <pre>{preview}</pre>
              <div className="application-preview-footer">
                仅展示由 AI Router 管理的字段
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}

function Overview({
  status,
  logs,
  config,
  navigate,
}: {
  status: Status | null;
  logs: Log[];
  config: AppConfig | null;
  navigate: (page: Page) => void;
}) {
  const m = status?.metrics;
  const success = m?.requests
    ? Math.max(0, 100 - (m.errors / m.requests) * 100)
    : 100;
  return (
    <>
      <QuickStart status={status} config={config} navigate={navigate} />
      <section className="hero">
        <div>
          <div className="eyebrow">系统已就绪</div>
          <h2>
            一个入口，
            <br />
            <em>连接所有协议。</em>
          </h2>
          <p>统一接收、转换并路由 OpenAI、Anthropic 与 Gemini 请求。</p>
        </div>
        <div className="hero-signal">
          <Activity size={28} />
          <b>{m?.in_flight || 0}</b>
          <span>个活动流</span>
        </div>
      </section>
      <section className="metrics">
        <Metric
          label="总请求"
          value={compact(m?.requests)}
          hint={`${m?.in_flight || 0} 正在处理`}
        />
        <Metric
          label="成功率"
          value={`${success.toFixed(1)}%`}
          hint={`${m?.errors || 0} 个错误`}
        />
        <Metric
          label="令牌用量"
          value={compact((m?.input_tokens || 0) + (m?.output_tokens || 0))}
          hint={`${compact(m?.input_tokens)} 输入 · ${compact(m?.output_tokens)} 输出`}
        />
        <Metric
          label="恢复动作"
          value={compact((m?.retries || 0) + (m?.fallbacks || 0))}
          hint={`${m?.retries || 0} 次重试 · ${m?.fallbacks || 0} 次降级`}
        />
        <Metric
          label="延迟"
          value={`${m?.p95_latency_ms || 0} ms`}
          hint={`P50 ${m?.p50_latency_ms || 0} ms · P95`}
        />
      </section>
      <section className="split">
        <div className="panel">
          <PanelTitle
            icon={Network}
            title="运行拓扑"
            action={`${status?.providers || 0} 个上游`}
          />
          <div className="flow">
            <div>
              <Braces />
              <span>客户端协议</span>
            </div>
            <i />
            <div className="core">
              <Wrench />
              <span>统一请求 IR</span>
            </div>
            <i />
            <div>
              <Server />
              <span>上游服务</span>
            </div>
          </div>
          <div className="endpoint">
            <span>网关地址</span>
            <code>{status?.gateway_url || "—"}</code>
          </div>
          <div className="health-list">
            {Object.entries(status?.provider_health || {}).map(
              ([id, health]) => (
                <div key={id}>
                  <span className={`dot ${health.ok ? "" : "bad"}`} />
                  <b>{id}</b>
                  <small>
                    {health.ok ? `${health.latency_ms || 0} ms` : `异常`}
                  </small>
                </div>
              ),
            )}
            {!Object.keys(status?.provider_health || {}).length && (
              <small>上游健康状态将在首次探测后显示</small>
            )}
          </div>
        </div>
        <div className="panel">
          <PanelTitle icon={ShieldCheck} title="最近请求" action="实时" />
          <MiniLogs logs={logs.slice(0, 5)} />
        </div>
      </section>
    </>
  );
}

function QuickStart({
  status,
  config,
  navigate,
}: {
  status: Status | null;
  config: AppConfig | null;
  navigate: (page: Page) => void;
}) {
  const [copied, setCopied] = useState("");
  const aliases = (config?.routes || [])
    .map((route) => route.match.model)
    .filter(
      (model): model is string =>
        typeof model === "string" && model.length > 0 && !/[?*\[]/.test(model),
    );
  const model = aliases[0] || "你的模型别名";
  const gateway = status?.gateway_url || "http://127.0.0.1:8080";
  const baseURL = `${gateway}/v1`;
  const claudeCode = `ANTHROPIC_BASE_URL=${gateway} ANTHROPIC_API_KEY=本地密钥 claude --model ${model}`;
  async function copy(label: string, value: string) {
    await navigator.clipboard.writeText(value);
    setCopied(label);
    window.setTimeout(() => setCopied(""), 1600);
  }
  return (
    <section className="quick-start">
      <div className="quick-heading">
        <div>
          <div className="eyebrow">从这里开始</div>
          <h2>只需三步，让客户端调用任意模型</h2>
          <p>
            客户端只连接 AI Router；AI Router
            根据模型别名选择上游，并自动转换协议。
          </p>
        </div>
        <div className="request-flow" aria-label="请求流程">
          <span>客户端</span>
          <ChevronRight size={14} />
          <b>AI Router</b>
          <ChevronRight size={14} />
          <span>真实模型</span>
        </div>
      </div>
      <div className="setup-steps">
        <article>
          <div className="step-number">1</div>
          <div>
            <small>连接模型服务</small>
            <h3>添加上游服务</h3>
            <p>填写供应商地址、API 密钥、协议和真实模型名。</p>
            <strong>{config?.providers?.length || 0} 个已配置</strong>
          </div>
          <button className="secondary" onClick={() => navigate("providers")}>
            管理上游
          </button>
        </article>
        <article>
          <div className="step-number">2</div>
          <div>
            <small>定义客户端模型名</small>
            <h3>建立模型路由</h3>
            <p>例如客户端请求 qwen3，实际转到 SiliconFlow 的完整模型名。</p>
            <strong>{config?.routes?.length || 0} 条已配置</strong>
          </div>
          <button className="secondary" onClick={() => navigate("routes")}>
            管理路由
          </button>
        </article>
        <article>
          <div className="step-number">3</div>
          <div>
            <small>发送第一条请求</small>
            <h3>接入并验证</h3>
            <p>复制网关地址到客户端，或先在调试台发送测试请求。</p>
            <strong>{status?.metrics?.requests || 0} 次已处理</strong>
          </div>
          <button className="primary" onClick={() => navigate("apps")}>
            配置应用
          </button>
        </article>
      </div>
      <div className="connection-card">
        <div>
          <small>OpenAI 兼容客户端基础地址</small>
          <code>{baseURL}</code>
        </div>
        <button onClick={() => copy("base", baseURL)}>
          {copied === "base" ? "已复制" : "复制地址"}
        </button>
        <div>
          <small>Claude Code 临时启动命令</small>
          <code>{claudeCode}</code>
        </div>
        <button onClick={() => copy("claude", claudeCode)}>
          {copied === "claude" ? "已复制" : "复制命令"}
        </button>
      </div>
      <p className="quick-note">
        客户端填写的模型名是 <code>{model}</code>
        ；真实供应商模型名由路由决定，客户端无需知道。
      </p>
    </section>
  );
}
function Metric({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{hint}</small>
    </div>
  );
}
function PanelTitle({
  icon: Icon,
  title,
  action,
}: {
  icon: React.ElementType;
  title: string;
  action: string;
}) {
  return (
    <div className="panel-title">
      <div>
        <Icon size={16} />
        <b>{title}</b>
      </div>
      <span>{action}</span>
    </div>
  );
}
function MiniLogs({ logs }: { logs: Log[] }) {
  return (
    <div className="mini-logs">
      {logs.length === 0 ? (
        <Empty text="还没有请求" />
      ) : (
        logs.map((l) => (
          <div key={l.id}>
            <span className={`status-code ${l.status >= 400 ? "bad" : ""}`}>
              {l.status || "…"}
            </span>
            <div>
              <b>{l.requested_model || "未知模型"}</b>
              <small>
                {protocolName(l.client_protocol)} → {l.provider_id || "路由中"}
              </small>
            </div>
            <time>{l.duration_ms} ms</time>
          </div>
        ))
      )}
    </div>
  );
}

function TableViewTabs<T extends string>({
  value,
  items,
  onChange,
}: {
  value: T;
  items: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div className="table-view-tabs" role="tablist">
      {items.map((item) => (
        <button
          key={item.value}
          className={value === item.value ? "active" : ""}
          role="tab"
          aria-selected={value === item.value}
          onClick={() => onChange(item.value)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

function Providers({
  data,
  yaml,
  hash,
  changed,
}: {
  data: Provider[];
  yaml: string;
  hash: string;
  changed: (y: string, h: string) => void;
}) {
  const [probing, setProbing] = useState("");
  const [result, setResult] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState<string | null | undefined>(undefined);
  const [notice, noticeContext] = notification.useNotification();
  const [view, setView] = useState<"all" | "connected" | "attention">("all");
  async function probe(id: string, testRequest = false) {
    setProbing(id);
    const provider = data.find((item) => item.id === id);
    try {
      const r = await api(`/api/providers/${id}/probe`, {
        method: "POST",
        body: JSON.stringify({ test_request: testRequest }),
      });
      const detail = r.ok
        ? `${r.latency_ms} ms · ${testRequest ? (r.test_ok ? "测试通过" : `测试失败（${r.test_status || "未知状态"}）`) : "连接正常"}`
        : r.error || `HTTP ${r.status || "未知状态"}`;
      setResult((x) => ({ ...x, [id]: detail }));
      if (r.ok) {
        notice.success({
          message: "连接测试通过",
          description: `${provider?.name || id} · ${detail}`,
          placement: "bottomRight",
          duration: 4,
        });
      } else {
        notice.error({
          message: "连接测试失败",
          description: `${provider?.name || id} · ${detail}`,
          placement: "bottomRight",
          duration: 6,
        });
      }
    } catch (e) {
      const detail = (e as Error).message;
      setResult((x) => ({ ...x, [id]: detail }));
      notice.error({
        message: "连接测试失败",
        description: `${provider?.name || id} · ${detail}`,
        placement: "bottomRight",
        duration: 6,
      });
    } finally {
      setProbing("");
    }
  }
  async function remove(id: string) {
    if (!confirm(`删除上游服务 ${id}？引用它的路由必须先移除。`)) return;
    try {
      const doc = parse(yaml) || {};
      doc.providers = (doc.providers || []).filter((p: any) => p.id !== id);
      const next = stringify(doc);
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      changed(next, r.hash);
    } catch (e) {
      alert((e as Error).message);
    }
  }
  const visibleProviders = data.filter((provider) => {
    if (view === "connected") return provider.api_key_set;
    if (view === "attention")
      return !provider.api_key_set || provider.health?.ok === false;
    return true;
  });
  const columns: TableColumnsType<Provider> = [
    {
      title: "模型服务",
      key: "service",
      width: 250,
      render: (_, provider) => (
        <div className="provider-identity table-provider-identity">
          <h3>{provider.name || provider.id}</h3>
          <Tag>{protocolName(provider.protocol)}</Tag>
        </div>
      ),
    },
    {
      title: "API 地址",
      dataIndex: "base_url",
      key: "base_url",
      width: 250,
      ellipsis: true,
      className: "provider-api-column",
      render: (value: string) => (
        <Tooltip title={value} placement="topLeft">
          <code className="table-code table-ellipsis">{value}</code>
        </Tooltip>
      ),
    },
    {
      title: "模型",
      key: "models",
      width: 190,
      ellipsis: true,
      className: "provider-model-column",
      render: (_, provider) => (
        <Tooltip title={provider.models.join(", ")} placement="topLeft">
          <div className="table-model-cell">
            <Tag>{provider.models[0] || "—"}</Tag>
            {provider.models.length > 1 && (
              <span>+{provider.models.length - 1}</span>
            )}
          </div>
        </Tooltip>
      ),
    },
    {
      title: "状态",
      key: "status",
      width: 86,
      align: "center",
      className: "provider-status-column",
      render: (_, provider) => (
        <Tooltip
          title={
            provider.health
              ? provider.health.ok
                ? `${provider.health.latency_ms || 0} ms`
                : "连接异常"
              : undefined
          }
        >
          <Tag color={provider.api_key_set ? "success" : "warning"}>
            {provider.api_key_set ? "已连接" : "缺少密钥"}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 174,
      align: "right",
      fixed: "right",
      render: (_, provider) => (
        <Space size={6} wrap={false}>
          <AntButton
            size="small"
            loading={probing === provider.id}
            aria-label={result[provider.id] || "测试"}
            onClick={() => probe(provider.id)}
          >
            测试
          </AntButton>
          <AntButton size="small" onClick={() => setEditing(provider.id)}>
            编辑
          </AntButton>
          <AntButton size="small" danger onClick={() => remove(provider.id)}>
            删除
          </AntButton>
        </Space>
      ),
    },
  ];
  return (
    <>
      {noticeContext}
      <WorkflowSteps active={1} />
      <section className="data-panel">
        <div className="toolbar">
          <div>
            <b>模型列表</b>
            <p>填写 API 地址、密钥和模型名；测试成功后自动识别协议并录入。</p>
          </div>
          <button className="primary" onClick={() => setEditing(null)}>
            + 接入模型
          </button>
        </div>
        <TableViewTabs
          value={view}
          onChange={setView}
          items={[
            { value: "all", label: "全部" },
            { value: "connected", label: "已连接" },
            { value: "attention", label: "需处理" },
          ]}
        />
        <div className="antd-table-shell">
          <Table<Provider>
            className="airoute-data-table provider-data-table"
            columns={columns}
            dataSource={visibleProviders}
            rowKey="id"
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 950 }}
            locale={{ emptyText: "还没有接入模型" }}
            footer={() =>
              view === "all"
                ? `共 ${data.length} 个模型服务`
                : `筛选结果 ${visibleProviders.length} 个 · 全部 ${data.length} 个`
            }
          />
        </div>
      </section>
      {editing !== undefined && (
        <ProviderDialog
          yaml={yaml}
          hash={hash}
          existing={editing}
          close={() => setEditing(undefined)}
          saved={changed}
        />
      )}
    </>
  );
}
function ProviderDialog({
  yaml,
  hash,
  existing,
  close,
  saved,
}: {
  yaml: string;
  hash: string;
  existing: string | null;
  close: () => void;
  saved: (y: string, h: string) => void;
}) {
  const raw = (parse(yaml)?.providers || []).find(
    (p: any) => p.id === existing,
  );
  const [form, setForm] = useState({
    id: raw?.id || "",
    name: raw?.name || "",
    profile: raw?.profile || "generic",
    reasoning_mode: raw?.reasoning_mode || "auto",
    max_output_tokens: String(raw?.max_output_tokens || ""),
    protocol: raw?.protocol || "openai-chat",
    base_url: raw?.base_url || "",
    api_key: raw?.api_key || "",
    models: (raw?.models || []).join(", "),
    headers: Object.entries(raw?.headers || {})
      .map(([key, value]) => `${key}: ${value}`)
      .join("\n"),
    timeout: raw?.timeout || "5m",
    dynamic_models: Boolean(raw?.dynamic_models),
    allow_private_url: Boolean(raw?.allow_private_url),
  });
  const [error, setError] = useState("");
  const [detecting, setDetecting] = useState(false);
  const [detection, setDetection] = useState<{
    ok: boolean;
    label?: string;
    latency_ms?: number;
  } | null>(
    existing === null ? null : { ok: true, label: protocolName(form.protocol) },
  );

  async function detect() {
    setDetecting(true);
    setError("");
    setDetection(null);
    try {
      const models = form.models
        .split(",")
        .map((value: string) => value.trim())
        .filter(Boolean);
      const result = await api("/api/providers/detect", {
        method: "POST",
        body: JSON.stringify({
          base_url: form.base_url,
          api_key: form.api_key,
          models,
          allow_private_url: form.allow_private_url,
        }),
      });
      if (!result.ok) {
        const attempts = (result.attempts || [])
          .map(
            (item: any) =>
              `${protocolName(item.protocol)}: ${item.status || item.error || "失败"}`,
          )
          .join("；");
        throw new Error(`没有识别出可用协议。${attempts}`);
      }
      const firstModel = models[0];
      const generatedID = firstModel
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-|-$/g, "")
        .slice(0, 42);
      setForm((current) => ({
        ...current,
        id: current.id || generatedID || `model-${Date.now()}`,
        name: current.name || firstModel,
        protocol: result.protocol,
        profile: result.profile || "generic",
      }));
      setDetection({
        ok: true,
        label: result.label,
        latency_ms: result.latency_ms,
      });
    } catch (e) {
      setError((e as Error).message);
      setDetection({ ok: false });
    } finally {
      setDetecting(false);
    }
  }

  async function save() {
    try {
      if (!detection?.ok) throw new Error("请先完成连接测试和协议识别");
      const doc = parse(yaml) || {};
      doc.providers = doc.providers || [];
      const headers = Object.fromEntries(
        form.headers
          .split("\n")
          .map((line: string) => line.trim())
          .filter(Boolean)
          .map((line: string) => {
            const index = line.indexOf(":");
            if (index < 1) throw new Error(`Header 格式错误：${line}`);
            return [line.slice(0, index).trim(), line.slice(index + 1).trim()];
          }),
      );
      const value = {
        ...raw,
        id: form.id,
        name: form.name,
        profile: form.profile,
        reasoning_mode: form.reasoning_mode,
        ...(form.max_output_tokens
          ? { max_output_tokens: Number(form.max_output_tokens) }
          : {}),
        protocol: form.protocol,
        base_url: form.base_url,
        api_key: form.api_key,
        timeout: form.timeout,
        dynamic_models: form.dynamic_models,
        allow_private_url: form.allow_private_url,
        ...(Object.keys(headers).length ? { headers } : {}),
        models: form.models
          .split(",")
          .map((x: string) => x.trim())
          .filter(Boolean),
      };
      if (existing === null) doc.providers.push(value);
      else
        doc.providers = doc.providers.map((p: any) =>
          p.id === existing ? value : p,
        );
      const next = stringify(doc);
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      saved(next, r.hash);
      close();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  return (
    <div className="modal">
      <div className="dialog model-onboard-dialog">
        <div className="panel-title">
          <div>
            <div className="step-number">1</div>
            <b>{existing === null ? "接入新模型" : "编辑模型接入"}</b>
          </div>
          <button onClick={close}>×</button>
        </div>
        {error && <div className="notice error">{error}</div>}
        <div className="onboard-fields">
          <label>
            API 地址
            <input
              placeholder="https://api.example.com/v1"
              value={form.base_url}
              onChange={(e) => {
                setForm({ ...form, base_url: e.target.value });
                setDetection(null);
              }}
            />
          </label>
          <label>
            API Key
            <input
              type="password"
              placeholder="sk-..."
              value={form.api_key}
              onChange={(e) => {
                setForm({ ...form, api_key: e.target.value });
                setDetection(null);
              }}
            />
          </label>
          <label>
            Model Names
            <input
              placeholder="qwen3, qwen3-coder（多个用逗号分隔）"
              value={form.models}
              onChange={(e) => {
                setForm({ ...form, models: e.target.value });
                setDetection(null);
              }}
            />
          </label>
          <label className="check-label private-switch">
            <input
              type="checkbox"
              checked={form.allow_private_url}
              onChange={(e) =>
                setForm({ ...form, allow_private_url: e.target.checked })
              }
            />
            允许访问本机或私网地址
          </label>
        </div>
        <button className="detect-button" onClick={detect} disabled={detecting}>
          <Activity size={16} />
          {detecting ? "正在连接并识别…" : "测试连接并自动识别协议"}
        </button>
        {detection?.ok && (
          <div className="detection-result success">
            <Check size={18} />
            <div>
              <b>识别成功：{detection.label}</b>
              <span>真实请求已通过 · {detection.latency_ms} ms</span>
            </div>
          </div>
        )}
        <details className="advanced-settings">
          <summary>高级设置与识别结果</summary>
          <div className="form-grid">
            <label>
              标识 ID
              <input
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
              />
            </label>
            <label>
              显示名称
              <input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </label>
            <label>
              识别协议
              <select
                value={form.protocol}
                onChange={(e) => setForm({ ...form, protocol: e.target.value })}
              >
                <option value="openai-chat">OpenAI Chat</option>
                <option value="openai-responses">OpenAI Responses</option>
                <option value="anthropic-messages">Anthropic / Claude</option>
                <option value="gemini-generate-content">Gemini</option>
              </select>
            </label>
            <label>
              模型配置模板
              <select
                value={form.profile}
                onChange={(e) => setForm({ ...form, profile: e.target.value })}
              >
                <option value="generic">通用</option>
                <option value="qwen3">Qwen 3.x</option>
                <option value="xiaomi-mimo">Xiaomi MiMo</option>
              </select>
            </label>
            <label>
              思考模式
              <select
                value={form.reasoning_mode}
                onChange={(e) =>
                  setForm({ ...form, reasoning_mode: e.target.value })
                }
              >
                <option value="auto">跟随客户端</option>
                <option value="disabled">始终关闭</option>
                <option value="enabled">始终开启</option>
              </select>
            </label>
            <label>
              超时
              <input
                value={form.timeout}
                onChange={(e) => setForm({ ...form, timeout: e.target.value })}
              />
            </label>
          </div>
        </details>
        <div className="dialog-actions">
          <button className="secondary" onClick={close}>
            取消
          </button>
          <button className="primary" onClick={save} disabled={!detection?.ok}>
            <Save size={15} />
            确认接入并加入模型列表
          </button>
        </div>
      </div>
    </div>
  );
}

function routeAddress(gateway: string, protocol: string, model: string) {
  const root = gateway.replace(/\/$/, "");
  switch (protocol) {
    case "openai-chat":
      return `${root}/v1/chat/completions`;
    case "openai-responses":
      return `${root}/v1/responses`;
    case "gemini-generate-content":
      return `${root}/v1beta/models/${encodeURIComponent(model || "模型名")}:generateContent`;
    default:
      return `${root}/v1/messages`;
  }
}

function Routes({
  data,
  fallback,
  providers,
  gateway,
  yaml,
  hash,
  changed,
}: {
  data: RouteConfig[];
  fallback: { provider: string; model: string }[];
  providers: Provider[];
  gateway: string;
  yaml: string;
  hash: string;
  changed: (y: string, h: string) => void;
}) {
  const [editing, setEditing] = useState<string | null | undefined>(undefined);
  const [view, setView] = useState<"all" | "rules" | "fallback">("all");
  async function remove(id: string) {
    if (!confirm(`删除路由 ${id}？`)) return;
    const doc = parse(yaml) || {};
    doc.routes = (doc.routes || []).filter((r: any) => r.id !== id);
    const next = stringify(doc);
    try {
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      changed(next, r.hash);
    } catch (e) {
      alert((e as Error).message);
    }
  }
  const rows: RouteTableRow[] = [
    ...data.map((route, index) => ({
      ...route,
      key: route.id,
      order: String(index + 1).padStart(2, "0"),
    })),
    {
      key: "__fallback__",
      order: "↳",
      id: "默认路由",
      priority: 0,
      match: {},
      targets: fallback,
      fallback: true,
    },
  ];
  const visibleRows = rows.filter((route) => {
    if (view === "rules") return !route.fallback;
    if (view === "fallback") return route.fallback;
    return true;
  });
  const columns: TableColumnsType<RouteTableRow> = [
    {
      title: "序号",
      dataIndex: "order",
      key: "order",
      width: 66,
      render: (value: string) => <span className="route-order">{value}</span>,
    },
    {
      title: "路由名称",
      key: "name",
      width: 170,
      render: (_, route) => (
        <div className="table-route-name">
          <b>{route.id}</b>
          <span>
            {route.fallback ? "兜底路由" : `优先级 ${route.priority}`}
          </span>
        </div>
      ),
    },
    {
      title: "匹配条件",
      key: "match",
      width: 210,
      className: "route-match-column",
      render: (_, route) => (
        <div className="table-route-match">
          <code>
            {route.fallback
              ? "没有其他规则命中"
              : `model = ${route.match.model || "*"}`}
          </code>
          {route.match.protocol && (
            <Tag>{protocolName(route.match.protocol)}</Tag>
          )}
        </div>
      ),
    },
    {
      title: "目标模型",
      key: "target",
      width: 260,
      ellipsis: true,
      render: (_, route) => (
        <Tooltip
          title={route.targets
            .map((target) => `${target.provider} / ${target.model}`)
            .join("\n")}
          placement="topLeft"
        >
          <div className="table-route-target">
            {route.targets.map((target, index) => (
              <code key={`${target.provider}-${target.model}-${index}`}>
                {target.provider} / {target.model}
              </code>
            ))}
          </div>
        </Tooltip>
      ),
    },
    {
      title: "调用地址",
      key: "address",
      ellipsis: true,
      className: "route-address-column",
      render: (_, route) => {
        const address = route.fallback
          ? gateway
          : route.match.protocol
            ? routeAddress(
                gateway,
                route.match.protocol,
                route.match.model || "模型名",
              )
            : gateway;
        return (
          <Tooltip title={address} placement="topLeft">
            <code className="table-code table-ellipsis">{address}</code>
          </Tooltip>
        );
      },
    },
    {
      title: "操作",
      key: "actions",
      width: 112,
      align: "right",
      fixed: "right",
      render: (_, route) =>
        route.fallback ? (
          <span className="muted-table-text">—</span>
        ) : (
          <Space size={6} wrap={false}>
            <AntButton size="small" onClick={() => setEditing(route.id)}>
              编辑
            </AntButton>
            <AntButton size="small" danger onClick={() => remove(route.id)}>
              删除
            </AntButton>
          </Space>
        ),
    },
  ];
  return (
    <>
      <WorkflowSteps active={2} />
      <section className="data-panel">
        <div className="toolbar">
          <div>
            <b>路由列表</b>
            <p>选择已接入模型和输出协议，系统会生成可以直接使用的路由地址。</p>
          </div>
          <button className="primary" onClick={() => setEditing(null)}>
            + 添加路由
          </button>
        </div>
        <TableViewTabs
          value={view}
          onChange={setView}
          items={[
            { value: "all", label: "全部" },
            { value: "rules", label: "模型路由" },
            { value: "fallback", label: "默认路由" },
          ]}
        />
        <div className="antd-table-shell">
          <Table<RouteTableRow>
            className="airoute-data-table route-data-table"
            columns={columns}
            dataSource={visibleRows}
            rowKey="key"
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 1040 }}
            locale={{ emptyText: "还没有配置路由" }}
            rowClassName={(route) =>
              route.fallback ? "fallback-table-row" : ""
            }
            footer={() =>
              view === "all"
                ? `共 ${data.length} 条模型路由 · 1 条默认路由`
                : `筛选结果 ${visibleRows.length} 条`
            }
          />
        </div>
      </section>
      {editing !== undefined && (
        <RouteDialog
          yaml={yaml}
          hash={hash}
          existing={editing}
          providers={providers}
          gateway={gateway}
          close={() => setEditing(undefined)}
          saved={changed}
        />
      )}
    </>
  );
}
function RouteDialog({
  yaml,
  hash,
  existing,
  providers,
  gateway,
  close,
  saved,
}: {
  yaml: string;
  hash: string;
  existing: string | null;
  providers: Provider[];
  gateway: string;
  close: () => void;
  saved: (y: string, h: string) => void;
}) {
  const raw = (parse(yaml)?.routes || []).find((r: any) => r.id === existing);
  const [form, setForm] = useState({
    id: raw?.id || "",
    priority: String(raw?.priority ?? 100),
    model: raw?.match?.model || "",
    protocol: raw?.match?.protocol || "anthropic-messages",
    stream: raw?.match?.stream === undefined ? "any" : String(raw.match.stream),
    tools: raw?.match?.tools === undefined ? "any" : String(raw.match.tools),
    image: raw?.match?.image === undefined ? "any" : String(raw.match.image),
    headers: Object.entries(raw?.match?.headers || {})
      .map(([key, value]) => `${key}: ${value}`)
      .join("\n"),
    targets:
      (raw?.targets || [])
        .map((t: any) => `${t.provider}:${t.model}`)
        .join(", ") ||
      `${providers[0]?.id || "provider"}:${providers[0]?.models?.[0] || "model"}`,
  });
  const [error, setError] = useState("");
  async function save() {
    try {
      const doc = parse(yaml) || {};
      doc.routes = doc.routes || [];
      const match: any = { model: form.model };
      if (form.protocol) match.protocol = form.protocol;
      for (const key of ["stream", "tools", "image"] as const) {
        if (form[key] !== "any") match[key] = form[key] === "true";
      }
      const headers = Object.fromEntries(
        form.headers
          .split("\n")
          .map((line: string) => line.trim())
          .filter(Boolean)
          .map((line: string) => {
            const index = line.indexOf(":");
            if (index < 1) throw new Error(`Header 格式错误：${line}`);
            return [line.slice(0, index).trim(), line.slice(index + 1).trim()];
          }),
      );
      if (Object.keys(headers).length) match.headers = headers;
      const targets = form.targets.split(",").map((x: string) => {
        const [provider, ...model] = x.trim().split(":");
        return { provider, model: model.join(":") };
      });
      const value = {
        ...raw,
        id: form.id,
        priority: Number(form.priority),
        match,
        targets,
      };
      if (existing === null) doc.routes.push(value);
      else
        doc.routes = doc.routes.map((r: any) =>
          r.id === existing ? value : r,
        );
      const next = stringify(doc);
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      saved(next, r.hash);
      close();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  return (
    <div className="modal">
      <div className="dialog route-wizard-dialog">
        <div className="panel-title">
          <div>
            <div className="step-number">2</div>
            <b>{existing === null ? "创建模型路由" : "编辑模型路由"}</b>
          </div>
          <button onClick={close}>×</button>
        </div>
        {error && <div className="notice error">{error}</div>}
        <div className="route-wizard-fields">
          <label>
            选择已接入模型
            <select
              aria-label="选择接入模型"
              value={form.targets.split(",")[0].trim()}
              onChange={(e) => {
                const selected = e.target.value;
                const model = selected.split(":").slice(1).join(":");
                const alias =
                  model
                    .split("/")
                    .pop()
                    ?.toLowerCase()
                    .replace(/[^a-z0-9.-]+/g, "-") || "model";
                setForm({
                  ...form,
                  targets: selected,
                  model: form.model || alias,
                  id: form.id || alias,
                });
              }}
            >
              {providers.flatMap((provider) =>
                provider.models.map((model) => (
                  <option
                    key={`${provider.id}:${model}`}
                    value={`${provider.id}:${model}`}
                  >
                    {provider.name || provider.id} / {model}
                  </option>
                )),
              )}
            </select>
          </label>
          <label>
            路由模型名
            <input
              aria-label="路由模型名"
              placeholder="例如 coding-model"
              value={form.model}
              onChange={(e) =>
                setForm({
                  ...form,
                  model: e.target.value,
                  id: existing === null ? e.target.value : form.id,
                })
              }
            />
          </label>
          <label>
            输出协议
            <select
              aria-label="输出协议"
              value={form.protocol}
              onChange={(e) => setForm({ ...form, protocol: e.target.value })}
            >
              <option value="anthropic-messages">Claude / Anthropic</option>
              <option value="openai-chat">OpenAI Chat</option>
              <option value="openai-responses">OpenAI Responses</option>
              <option value="gemini-generate-content">Gemini</option>
            </select>
          </label>
        </div>
        <div className="generated-route-address">
          <div>
            <small>路由后的调用地址</small>
            <code>{routeAddress(gateway, form.protocol, form.model)}</code>
          </div>
          <span>
            模型参数填写 <b>{form.model || "模型名"}</b>
          </span>
        </div>
        <details className="advanced-settings">
          <summary>高级匹配设置</summary>
          <div className="form-grid">
            <label>
              路由 ID
              <input
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
              />
            </label>
            <label>
              优先级
              <input
                type="number"
                value={form.priority}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
              />
            </label>
            {(["stream", "tools", "image"] as const).map((key) => (
              <label key={key}>
                {key === "stream"
                  ? "流式响应"
                  : key === "tools"
                    ? "工具调用"
                    : "图片输入"}
                <select
                  value={form[key]}
                  onChange={(e) => setForm({ ...form, [key]: e.target.value })}
                >
                  <option value="any">任意</option>
                  <option value="true">是</option>
                  <option value="false">否</option>
                </select>
              </label>
            ))}
          </div>
        </details>
        <div className="dialog-actions">
          <button className="secondary" onClick={close}>
            取消
          </button>
          <button className="primary" onClick={save}>
            <Save size={15} />
            保存路由
          </button>
        </div>
      </div>
    </div>
  );
}
function RouteExplain() {
  const [model, setModel] = useState("fast");
  const [protocol, setProtocol] = useState("openai-chat");
  const [result, setResult] = useState("");
  async function explain() {
    const request =
      protocol === "openai-responses"
        ? { model, input: "test" }
        : protocol === "anthropic-messages"
          ? {
              model,
              max_tokens: 32,
              messages: [{ role: "user", content: "test" }],
            }
          : protocol === "gemini-generate-content"
            ? { model, contents: [{ role: "user", parts: [{ text: "test" }] }] }
            : { model, messages: [{ role: "user", content: "test" }] };
    try {
      const r = await api("/api/routes/explain", {
        method: "POST",
        body: JSON.stringify({ protocol, request }),
      });
      setResult(
        `${r.decision.route_id} → ${r.decision.targets.map((t: any) => `${t.provider.id}/${t.model}`).join(" → ")}`,
      );
    } catch (e) {
      setResult((e as Error).message);
    }
  }
  return (
    <div className="route-explain">
      <div>
        <b>路由解释</b>
        <span>在发送请求前验证最终路由</span>
      </div>
      <input
        aria-label="解释模型"
        value={model}
        onChange={(e) => setModel(e.target.value)}
      />
      <select
        aria-label="解释协议"
        value={protocol}
        onChange={(e) => setProtocol(e.target.value)}
      >
        <option value="openai-chat">OpenAI Chat</option>
        <option value="openai-responses">OpenAI Responses</option>
        <option value="anthropic-messages">Anthropic</option>
        <option value="gemini-generate-content">Gemini</option>
      </select>
      <button className="secondary" onClick={explain}>
        解释
      </button>
      <code>{result || "—"}</code>
    </div>
  );
}

const samples: Record<string, object> = {
  "openai-chat": {
    model: "fast",
    messages: [{ role: "user", content: "请用一句话解释 AI 协议转换。" }],
    stream: false,
  },
  "openai-responses": {
    model: "fast",
    input: "请用一句话解释 AI 协议转换。",
    stream: false,
  },
  "anthropic-messages": {
    model: "fast",
    max_tokens: 256,
    messages: [{ role: "user", content: "请用一句话解释 AI 协议转换。" }],
    stream: false,
  },
  "gemini-generate-content": {
    model: "fast",
    contents: [
      {
        role: "user",
        parts: [{ text: "请用一句话解释 AI 协议转换。" }],
      },
    ],
  },
};
function Playground() {
  const [protocol, setProtocol] = useState("openai-chat");
  const [body, setBody] = useState(JSON.stringify(samples[protocol], null, 2));
  const [model, setModel] = useState("fast");
  const [stream, setStream] = useState(false);
  const [output, setOutput] = useState("");
  const [preview, setPreview] = useState<any>(null);
  const [running, setRunning] = useState(false);
  function change(p: string) {
    setProtocol(p);
    setBody(JSON.stringify(samples[p], null, 2));
    setOutput("");
    setPreview(null);
  }
  function preparedBody() {
    const value = JSON.parse(body);
    value.model = model;
    value.stream = stream;
    return value;
  }
  async function inspect() {
    try {
      setPreview(
        await api("/api/playground/preview", {
          method: "POST",
          body: JSON.stringify({ protocol, request: preparedBody() }),
        }),
      );
    } catch (e) {
      setPreview({ error: (e as Error).message });
    }
  }
  async function send() {
    setRunning(true);
    try {
      await inspect();
      const token = sessionStorage.getItem("airoute_token") || "";
      const response = await fetch("/api/playground/request", {
        method: "POST",
        headers: {
          "content-type": "application/json",
          ...(token ? { authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ protocol, body: preparedBody(), stream }),
      });
      if (!response.ok) {
        const error = await response
          .json()
          .catch(() => ({ error: response.statusText }));
        throw new Error(error.error || `HTTP ${response.status}`);
      }
      if (
        response.headers.get("content-type")?.includes("text/event-stream") &&
        response.body
      ) {
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let text = `HTTP ${response.headers.get("x-airoute-playground-status") || 200} · ${response.headers.get("x-airoute-request-id") || "—"}\n\n`;
        setOutput(text);
        for (;;) {
          const chunk = await reader.read();
          if (chunk.done) break;
          text += decoder.decode(chunk.value, { stream: true });
          setOutput(text);
        }
        return;
      }
      const envelope = await response.json();
      let display = envelope.body;
      try {
        display = JSON.stringify(JSON.parse(envelope.body), null, 2);
      } catch {}
      setOutput(
        `HTTP ${envelope.status} · ${envelope.request_id || "—"}\n\n${display}`,
      );
    } catch (e) {
      setOutput((e as Error).message);
    } finally {
      setRunning(false);
    }
  }
  return (
    <div className="playground">
      <div className="page-intro">
        <b>第 3 步：发送测试请求</b>
        <span>
          选择客户端协议和模型别名；系统会展示路由、转换后的上游请求与最终响应。
        </span>
      </div>
      <div className="play-head">
        <select value={protocol} onChange={(e) => change(e.target.value)}>
          <option value="openai-chat">OpenAI Chat</option>
          <option value="openai-responses">OpenAI Responses</option>
          <option value="anthropic-messages">Anthropic Messages</option>
          <option value="gemini-generate-content">Gemini</option>
        </select>
        <input
          aria-label="调试模型"
          value={model}
          onChange={(e) => setModel(e.target.value)}
        />
        <label className="check-label">
          <input
            type="checkbox"
            checked={stream}
            onChange={(e) => setStream(e.target.checked)}
          />
          流式响应
        </label>
        <button className="secondary" onClick={inspect}>
          预览转换
        </button>
        <button className="primary" onClick={send} disabled={running}>
          <Play size={15} />
          {running ? "发送中…" : "发送请求"}
        </button>
      </div>
      <div className="editors">
        <div>
          <label>请求内容</label>
          <textarea
            spellCheck={false}
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
        </div>
        <div>
          <label>响应内容</label>
          <pre>{output || "响应将在这里显示。"}</pre>
        </div>
      </div>
      {preview && (
        <div className="preview-grid">
          <div>
            <label>统一请求 IR</label>
            <pre>
              {JSON.stringify(
                preview.canonical_request || preview.error,
                null,
                2,
              )}
            </pre>
          </div>
          <div>
            <label>上游请求</label>
            <pre>{JSON.stringify(preview.upstream_request || {}, null, 2)}</pre>
          </div>
          <div>
            <label>兼容性诊断</label>
            <pre>{JSON.stringify(preview.diagnostics || [], null, 2)}</pre>
          </div>
        </div>
      )}
    </div>
  );
}

function Logs({ logs }: { logs: Log[] }) {
  const [selected, setSelected] = useState<Log | null>(null);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("all");
  const filtered = useMemo(
    () =>
      logs.filter((log) => {
        const matchesStatus =
          status === "all" ||
          (status === "error" ? log.status >= 400 : log.status < 400);
        const haystack =
          `${log.id} ${log.requested_model} ${log.provider_id} ${log.route_id} ${log.client_protocol}`.toLowerCase();
        return matchesStatus && haystack.includes(query.toLowerCase());
      }),
    [logs, query, status],
  );
  return (
    <div className="logs-layout">
      <div className="log-table">
        <div className="log-filters">
          <input
            aria-label="日志筛选"
            placeholder="请求 ID / 模型 / 上游服务"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="all">全部状态</option>
            <option value="success">成功</option>
            <option value="error">错误</option>
          </select>
          <span>
            {filtered.length} / {logs.length}
          </span>
        </div>
        <div className="table-head">
          <span>状态</span>
          <span>模型 / 路由</span>
          <span>协议</span>
          <span>上游服务</span>
          <span>耗时</span>
          <span>时间</span>
        </div>
        {filtered.map((l) => (
          <button
            key={l.id}
            onClick={() => setSelected(l)}
            className={selected?.id === l.id ? "selected" : ""}
          >
            <span className={`status-code ${l.status >= 400 ? "bad" : ""}`}>
              {l.status || "…"}
            </span>
            <span>
              <b>{l.requested_model || "—"}</b>
              <small>{l.route_id || "未匹配"}</small>
            </span>
            <span>{protocolName(l.client_protocol)}</span>
            <span>{l.provider_id || "—"}</span>
            <span>{l.duration_ms} ms</span>
            <time>{new Date(l.started_at).toLocaleTimeString()}</time>
          </button>
        ))}
      </div>
      {selected && (
        <div className="log-detail">
          <div className="panel-title">
            <b>请求详情</b>
            <button onClick={() => setSelected(null)}>×</button>
          </div>
          <code>{selected.id}</code>
          <dl>
            <dt>实际模型</dt>
            <dd>{selected.resolved_model}</dd>
            <dt>首个令牌</dt>
            <dd>{selected.first_token_ms || "—"} ms</dd>
            <dt>输入令牌</dt>
            <dd>{selected.usage?.input_tokens || 0}</dd>
            <dt>输出令牌</dt>
            <dd>{selected.usage?.output_tokens || 0}</dd>
            <dt>错误</dt>
            <dd>{selected.error_code || "无"}</dd>
          </dl>
          {selected.diagnostics?.map((d) => (
            <div className="notice" key={d.code}>
              {d.code}: {d.message}
            </div>
          ))}
          {selected.attempts && selected.attempts.length > 0 && (
            <div className="attempt-list">
              <small>降级 / 重试记录</small>
              {selected.attempts.map((attempt, index) => (
                <div key={`${attempt.provider_id}-${index}`}>
                  <b>
                    #{attempt.number} {attempt.provider_id}/{attempt.model}
                  </b>
                  <span>{attempt.status || attempt.error || "失败"}</span>
                  <time>{attempt.duration_ms} ms</time>
                </div>
              ))}
            </div>
          )}
          {(selected.request_body || selected.response_body) && (
            <div className="captured-bodies">
              <div className="notice error">
                正文采集已开启；内容经过密钥脱敏和长度截断。
              </div>
              {selected.request_body && (
                <>
                  <small>请求正文</small>
                  <pre>{selected.request_body}</pre>
                </>
              )}
              {selected.response_body && (
                <>
                  <small>响应正文</small>
                  <pre>{selected.response_body}</pre>
                </>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function ConfigEditor({
  yaml,
  hash,
  status,
  onSaved,
}: {
  yaml: string;
  hash: string;
  status: Status | null;
  onSaved: (y: string, h: string) => void;
}) {
  const sourceJSON = useMemo(() => {
    try {
      return JSON.stringify(parse(yaml), null, 2);
    } catch {
      return "{}";
    }
  }, [yaml]);
  const [draft, setDraft] = useState(sourceJSON);
  const [message, setMessage] = useState("");
  const [action, setAction] = useState<
    "" | "validating" | "saving" | "rolling-back"
  >("");
  const dirty = draft !== sourceJSON;
  const lineCount = draft.split("\n").length;
  const characterCount = draft.length.toLocaleString("zh-CN");
  useEffect(() => setDraft(sourceJSON), [sourceJSON]);
  function asYAML() {
    return stringify(JSON.parse(draft), { lineWidth: 0 });
  }
  async function validate() {
    setAction("validating");
    try {
      const nextYAML = asYAML();
      await api("/api/config/validate", {
        method: "POST",
        body: JSON.stringify({ yaml: nextYAML }),
      });
      setMessage("JSON 格式和 AI Router 配置均有效");
    } catch (e) {
      setMessage((e as Error).message);
    } finally {
      setAction("");
    }
  }
  async function save() {
    setAction("saving");
    try {
      const nextYAML = asYAML();
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: nextYAML, expected_hash: hash }),
      });
      setMessage("已保存并热加载，原配置已自动备份");
      onSaved(nextYAML, r.hash);
    } catch (e) {
      setMessage((e as Error).message);
    } finally {
      setAction("");
    }
  }
  async function rollback() {
    setAction("rolling-back");
    try {
      const r = await api("/api/config/backups");
      if (!r.backups?.[0]) {
        setMessage("暂无可回滚的配置备份");
        return;
      }
      if (!confirm(`回滚到 ${r.backups[0].name}？`)) return;
      const x = await api("/api/config/rollback", {
        method: "POST",
        body: JSON.stringify({ name: r.backups[0].name }),
      });
      const latest = await api("/api/config");
      onSaved(latest.yaml, x.hash);
      setMessage("已回滚到最近的配置备份");
    } catch (e) {
      setMessage((e as Error).message);
    } finally {
      setAction("");
    }
  }
  return (
    <div className="system-settings-page">
      <section className="settings-summary-card">
        <div className="settings-summary-copy">
          <div
            className={`settings-live-mark ${status?.status === "running" ? "is-running" : "is-stopped"}`}
          >
            <span />
            {status?.status === "running" ? "配置正在生效" : "网关当前已关闭"}
          </div>
          <h2>运行配置中心</h2>
          <p>直接维护 AI Router 的完整 JSON 配置，保存后立即校验并热加载。</p>
        </div>
        <div className="settings-summary-facts">
          <div>
            <span>模型服务</span>
            <b>{status?.providers ?? "—"}</b>
          </div>
          <div>
            <span>路由规则</span>
            <b>{status?.routes ?? "—"}</b>
          </div>
          <div>
            <span>配置版本</span>
            <code>{status?.config_version?.slice(0, 10) || "—"}</code>
          </div>
        </div>
      </section>

      <section className="settings-workbench">
        <div className="settings-context-panel">
          <div className="settings-context-heading">
            <small>配置文件</small>
            <b>系统配置</b>
          </div>
          <div className="settings-context-active">
            <Braces size={16} />
            <div>
              <b>JSON 配置</b>
              <span>完整配置文件</span>
            </div>
          </div>
          <dl className="settings-context-list">
            <div>
              <dt>编辑格式</dt>
              <dd>JSON</dd>
            </div>
            <div>
              <dt>加载方式</dt>
              <dd>热加载</dd>
            </div>
            <div>
              <dt>当前状态</dt>
              <dd className={dirty ? "is-dirty" : "is-synced"}>
                {dirty ? "有未保存更改" : "已与运行配置同步"}
              </dd>
            </div>
          </dl>
          <div className="settings-safety-note">
            <ShieldCheck size={16} />
            <p>每次保存前都会验证配置。验证失败时，当前运行版本不会被替换。</p>
          </div>
        </div>

        <div className="settings-editor-panel">
          <div className="settings-editor-toolbar">
            <div className="settings-file-title">
              <span className="settings-file-icon">&#123; &#125;</span>
              <div>
                <b>ai-router.json</b>
                <small>{dirty ? "已修改，尚未保存" : "已同步到运行配置"}</small>
              </div>
            </div>
            <div className="settings-editor-actions">
              <button
                className="secondary"
                onClick={rollback}
                disabled={Boolean(action)}
              >
                <RotateCcw size={14} />
                {action === "rolling-back" ? "回滚中…" : "回滚"}
              </button>
              <button
                className="secondary"
                onClick={validate}
                disabled={Boolean(action)}
              >
                <Check size={14} />
                {action === "validating" ? "校验中…" : "校验"}
              </button>
              <button
                className="primary"
                disabled={!dirty || Boolean(action)}
                onClick={save}
              >
                <Save size={14} />
                {action === "saving" ? "保存中…" : "保存并热加载"}
              </button>
            </div>
          </div>

          {message && (
            <div
              className={`settings-message ${message.includes("有效") || message.includes("保存") || message.includes("回滚到") ? "success" : "error"}`}
            >
              <span />
              {message}
              <button aria-label="关闭提示" onClick={() => setMessage("")}>
                ×
              </button>
            </div>
          )}

          <div className="settings-editor-meta">
            <span>JSON</span>
            <span>{lineCount} 行</span>
            <span>{characterCount} 字符</span>
            <span className={dirty ? "is-dirty" : "is-synced"}>
              {dirty ? "未保存" : "已同步"}
            </span>
          </div>
          <textarea
            aria-label="系统 JSON 配置"
            className="settings-json-editor"
            spellCheck={false}
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value);
              setMessage("");
            }}
          />
          <div className="settings-editor-footer">
            <span>保存时自动转换为运行配置格式，并创建可回滚备份。</span>
            <code>UTF-8 · 2 空格缩进</code>
          </div>
        </div>
      </section>
    </div>
  );
}
function lineDiff(before: string, after: string) {
  const left = before.split("\n");
  const right = after.split("\n");
  const lines: string[] = [];
  const count = Math.max(left.length, right.length);
  for (let i = 0; i < count; i++) {
    if (left[i] === right[i]) continue;
    if (left[i] !== undefined) lines.push(`- ${left[i]}`);
    if (right[i] !== undefined) lines.push(`+ ${right[i]}`);
  }
  return lines.slice(0, 200).join("\n") || "没有文本变化";
}
function Empty({ text }: { text: string }) {
  return (
    <div className="empty">
      <Activity size={22} />
      <span>{text}</span>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ConfigProvider
      theme={{
        token: {
          colorPrimary: "#4263eb",
          colorText: "#242a34",
          colorTextSecondary: "#737d8c",
          colorBorder: "#dfe3e8",
          borderRadius: 10,
          fontSize: 13,
          fontFamily:
            'Inter, "SF Pro Text", "PingFang SC", system-ui, sans-serif',
        },
        components: {
          Table: {
            headerBg: "#f6f8fa",
            headerColor: "#697485",
            borderColor: "#e1e5ea",
            rowHoverBg: "#f8faff",
            footerBg: "#fafbfc",
            footerColor: "#727c8a",
            cellPaddingBlock: 17,
            cellPaddingInline: 18,
          },
          Button: {
            borderRadius: 8,
            controlHeightSM: 30,
          },
          Tag: {
            borderRadiusSM: 6,
          },
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
);
