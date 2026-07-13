import React, { lazy, Suspense, useCallback, useEffect, useState } from "react";
import {
  Braces,
  ChevronRight,
  CircleAlert,
  KeyRound,
  Power,
  RefreshCw,
  Route,
  Server,
  Settings,
} from "lucide-react";
import { api } from "./api";
import type { AppConfig, Page, Status } from "../types";
const ApplicationsPage = lazy(() =>
  import("../pages/ApplicationsPage/ApplicationsPage").then((module) => ({
    default: module.ApplicationsPage,
  })),
);
const ProvidersPage = lazy(() =>
  import("../pages/ProvidersPage/ProvidersPage").then((module) => ({
    default: module.ProvidersPage,
  })),
);
const RoutesPage = lazy(() =>
  import("../pages/RoutesPage/RoutesPage").then((module) => ({
    default: module.RoutesPage,
  })),
);
const SettingsPage = lazy(() =>
  import("../pages/SettingsPage/SettingsPage").then((module) => ({
    default: module.SettingsPage,
  })),
);

const pages: { id: Page; label: string; icon: React.ElementType }[] = [
  { id: "providers", label: "模型接入", icon: Server },
  { id: "routes", label: "路由配置", icon: Route },
  { id: "apps", label: "应用配置", icon: Braces },
  { id: "settings", label: "系统设置", icon: Settings },
];

export function App() {
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
  useEffect(() => {
    const syncHash = () => {
      const requested =
        location.hash.slice(1) === "claude" ? "apps" : location.hash.slice(1);
      if (pages.some((item) => item.id === requested)) {
        setPage(requested as Page);
      }
    };
    window.addEventListener("hashchange", syncHash);
    return () => window.removeEventListener("hashchange", syncHash);
  }, []);
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
              {status?.status === "running"
                ? "点击关闭 · 重启后恢复"
                : "点击启动"}
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
        <Suspense fallback={<div className="page-loading">正在加载页面…</div>}>
          {page === "apps" && (
            <ApplicationsPage status={status} config={config} />
          )}
          {page === "providers" && (
            <ProvidersPage
              data={config?.providers || []}
              yaml={yaml}
              hash={hash}
              changed={(y, h) => {
                setYaml(y);
                setHash(h);
                load();
              }}
            />
          )}
          {page === "routes" && (
            <RoutesPage
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
          )}
          {page === "settings" && (
            <SettingsPage
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
        </Suspense>
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
