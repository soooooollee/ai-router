import React, { lazy, Suspense, useCallback, useEffect, useState } from "react";
import {
  ArrowUpRight,
  Braces,
  ChartNoAxesCombined,
  ChevronRight,
  CircleAlert,
  KeyRound,
  ListTree,
  Route,
  Server,
  Settings,
} from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "./api";
import { applyLocale, initialLocale, type Locale } from "./i18n";
import type { AppConfig, Page, Status, UpdateInfo } from "../types";
const ApplicationsPage = lazy(() =>
  import("../pages/ApplicationsPage/ApplicationsPage").then((module) => ({
    default: module.ApplicationsPage,
  })),
);
const OverviewPage = lazy(() =>
  import("../pages/OverviewPage/OverviewPage").then((module) => ({
    default: module.OverviewPage,
  })),
);
const LogsPage = lazy(() =>
  import("../pages/LogsPage/LogsPage").then((module) => ({
    default: module.LogsPage,
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
  { id: "overview", label: "运行概览", icon: ChartNoAxesCombined },
  { id: "providers", label: "模型接入", icon: Server },
  { id: "routes", label: "路由配置", icon: Route },
  { id: "apps", label: "应用配置", icon: Braces },
  { id: "logs", label: "调用日志", icon: ListTree },
  { id: "settings", label: "系统设置", icon: Settings },
];

export function App() {
  const requestedPage =
    location.hash.slice(1) === "claude" ? "apps" : location.hash.slice(1);
  const initialPage = pages.some((item) => item.id === requestedPage)
    ? (requestedPage as Page)
    : "overview";
  const [page, setPage] = useState<Page>(initialPage);
  const [status, setStatus] = useState<Status | null>(null);
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [yaml, setYaml] = useState("");
  const [hash, setHash] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [locale, setLocale] = useState<Locale>(initialLocale);
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
      const localDocument = parse(c.yaml) || {};
      localDocument.providers = (localDocument.providers || []).map(
        (configuredProvider: { id?: string; api_key?: string }) => ({
          ...configuredProvider,
          api_key:
            p.providers.find(
              (provider: AppConfig["providers"][number]) =>
                provider.id === configuredProvider.id,
            )?.api_key || configuredProvider.api_key || "",
        }),
      );
      setConfig({
        ...c.config,
        providers: p.providers,
      });
      setYaml(stringify(localDocument, { lineWidth: 0 }));
      setHash(c.hash);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    return applyLocale(locale);
  }, [locale]);
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
    if (!status?.version) return;
    api("/api/update")
      .then(setUpdateInfo)
      .catch(() => setUpdateInfo(null));
  }, [status?.version]);
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
    <div className="app-frame">
      <header className="app-header">
        <strong>AI Router</strong>
        <div>
          <span className="app-header-caption">本地 AI 协议网关</span>
          <label className="language-switcher">
            <span>语言</span>
            <select aria-label="语言" value={locale} onChange={(event) => { const next = event.target.value as Locale; localStorage.setItem("airoute_locale", next); setLocale(next); }}>
              <option value="zh-CN">中文</option>
              <option value="en-US">English</option>
            </select>
          </label>
          <span className={`header-runtime ${status?.status === "running" ? "running" : "stopped"}`}>
            <i />{status?.status === "running" ? "运行中" : "已关闭"}
          </span>
        </div>
      </header>
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
        <VersionBadge version={status?.version} update={updateInfo} />
        </aside>
        <main>
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
          {page === "overview" && <OverviewPage status={status} />}
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
              providers={config?.providers || []}
              gateway={status?.gateway_url || "http://127.0.0.1:12666"}
              yaml={yaml}
              hash={hash}
              changed={(y, h) => {
                setYaml(y);
                setHash(h);
                load();
              }}
            />
          )}
          {page === "logs" && <LogsPage />}
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
    </div>
  );
}

function VersionBadge({
  version,
  update,
}: {
  version?: string;
  update: UpdateInfo | null;
}) {
  const displayVersion = (value?: string) => {
    if (!value) return "—";
    return value === "dev" || value.startsWith("v") ? value : `v${value}`;
  };
  const hasUpdate = Boolean(update?.update_available && update.latest_version);
  const releaseURL = update?.release_url || "https://github.com/soooooollee/ai-router/releases";
  return (
    <a
      className={`sidebar-version${hasUpdate ? " has-update" : ""}`}
      data-testid="sidebar-version"
      href={releaseURL}
      target="_blank"
      rel="noreferrer"
    >
      <span className="sidebar-version-label">
        <span>当前版本</span>
        <ArrowUpRight size={13} />
      </span>
      <strong>{displayVersion(version)}</strong>
      {hasUpdate && (
        <small>
          <i />
          <span>可更新到</span>
          {displayVersion(update?.latest_version)}
        </small>
      )}
    </a>
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
