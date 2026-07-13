import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Braces, Check, RefreshCw, Save, ShieldCheck } from "lucide-react";
import { api } from "../../app/api";
import { WorkflowSteps } from "../../components/WorkflowSteps";
import { ApplicationResults } from "./ApplicationResults";
import type {
  AppConfig,
  ApplicationBackup,
  ApplicationCapability,
  ApplicationListItem,
  ApplicationPreview,
  ApplicationState,
  ApplicationVerifyResult,
  Status,
} from "../../types";

export function ApplicationsPage({
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
  const [apps, setApps] = useState<ApplicationListItem[]>([]);
  const [selectedID, setSelectedID] = useState("claude-code");
  const [appState, setAppState] = useState<ApplicationState | null>(null);
  const [previewResult, setPreviewResult] = useState<ApplicationPreview | null>(
    null,
  );
  const [verifyResult, setVerifyResult] =
    useState<ApplicationVerifyResult | null>(null);
  const [backups, setBackups] = useState<ApplicationBackup[]>([]);
  const [form, setForm] = useState({
    base_url: gateway,
    api_key: config?.auth?.enabled ? "" : "airoute-local",
    model: fallback,
    opus_model: fallback,
    sonnet_model: fallback,
    haiku_model: fallback,
  });
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");

  const selectedApp = apps.find((item) => item.manifest.id === selectedID);
  const hasCapability = (capability: ApplicationCapability) =>
    selectedApp?.manifest.capabilities.includes(capability) ?? false;

  const loadApplications = useCallback(async () => {
    const value = await api("/api/apps");
    const items = (value.apps || []) as ApplicationListItem[];
    setApps(items);
    setSelectedID((current) =>
      items.some((item) => item.manifest.id === current)
        ? current
        : items[0]?.manifest.id || "",
    );
  }, []);

  const loadApplication = useCallback(async () => {
    if (!selectedID) return;
    const [state, backupValue] = await Promise.all([
      api(`/api/apps/${selectedID}`) as Promise<ApplicationState>,
      api(`/api/apps/${selectedID}/backups`) as Promise<{
        backups: ApplicationBackup[];
      }>,
    ]);
    setAppState(state);
    setBackups(backupValue.backups || []);
    if (selectedID === "claude-code") {
      const managed = state.managed || {};
      const selected = (candidate: unknown) =>
        typeof candidate === "string" && aliases.includes(candidate)
          ? candidate
          : fallback;
      setForm((current) => ({
        ...current,
        base_url:
          typeof managed.ANTHROPIC_BASE_URL === "string"
            ? managed.ANTHROPIC_BASE_URL
            : gateway,
        api_key: managed.api_key_set ? "" : current.api_key,
        model: selected(managed.ANTHROPIC_MODEL),
        opus_model: selected(managed.ANTHROPIC_DEFAULT_OPUS_MODEL),
        sonnet_model: selected(managed.ANTHROPIC_DEFAULT_SONNET_MODEL),
        haiku_model: selected(managed.ANTHROPIC_DEFAULT_HAIKU_MODEL),
      }));
    }
  }, [aliases, fallback, gateway, selectedID]);

  useEffect(() => {
    loadApplications().catch((error) => setMessage((error as Error).message));
  }, [loadApplications]);

  useEffect(() => {
    setPreviewResult(null);
    setVerifyResult(null);
    loadApplication().catch((error) => setMessage((error as Error).message));
  }, [loadApplication]);

  async function refreshPreview() {
    setBusy("preview");
    setMessage("");
    try {
      const value = (await api(`/api/apps/${selectedID}/preview`, {
        method: "POST",
        body: JSON.stringify(form),
      })) as ApplicationPreview;
      setPreviewResult(value);
      return value;
    } catch (error) {
      setMessage((error as Error).message);
      return null;
    } finally {
      setBusy("");
    }
  }

  async function save() {
    setBusy("save");
    setMessage("");
    try {
      const nextPreview = (await api(`/api/apps/${selectedID}/preview`, {
        method: "POST",
        body: JSON.stringify(form),
      })) as ApplicationPreview;
      setPreviewResult(nextPreview);
      const result = await api(`/api/apps/${selectedID}/config`, {
        method: "PUT",
        body: JSON.stringify(form),
      });
      setMessage(
        result.backup
          ? `已写入 ${result.path}，备份 ${result.backup} 已创建。`
          : `已写入 ${result.path}。`,
      );
      await loadApplication();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function verify(runCLI: boolean) {
    if (
      runCLI &&
      !window.confirm(
        "完整验证会让 Claude Code 发起一次真实模型请求，可能产生少量费用。继续吗？",
      )
    ) {
      return;
    }
    setBusy(runCLI ? "cli" : "verify");
    setMessage("");
    try {
      const result = (await api(`/api/apps/${selectedID}/verify`, {
        method: "POST",
        body: JSON.stringify({ config: form, run_cli: runCLI }),
      })) as ApplicationVerifyResult;
      setVerifyResult(result);
      setMessage(
        result.ok ? "应用链路验证通过。" : "部分验证未通过，请查看阶段详情。",
      );
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function rollback(backup: ApplicationBackup) {
    if (!window.confirm(`确认恢复备份 ${backup.name}？当前配置会先保留。`)) {
      return;
    }
    setBusy(backup.name);
    setMessage("");
    try {
      await api(`/api/apps/${selectedID}/rollback`, {
        method: "POST",
        body: JSON.stringify({ name: backup.name }),
      });
      setMessage(`已恢复备份 ${backup.name}。`);
      await loadApplication();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
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
            <b>{apps.length}</b>
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
            <span className="application-count">{apps.length}</span>
          </div>
          {apps.map((item) => (
            <button
              className={`application-list-item ${selectedID === item.manifest.id ? "active" : ""}`}
              type="button"
              key={item.manifest.id}
              onClick={() => setSelectedID(item.manifest.id)}
            >
              <span className="application-monogram claude">
                {item.manifest.name
                  .split(" ")
                  .map((part) => part[0])
                  .join("")
                  .slice(0, 2)}
              </span>
              <span className="application-list-copy">
                <b>{item.manifest.name}</b>
                <small>{item.manifest.description}</small>
              </span>
              <span className="application-supported">
                {item.detection.installed ? "已安装" : "可配置"}
              </span>
            </button>
          ))}
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
                  <h3>{selectedApp?.manifest.name || "应用配置"}</h3>
                  <span>
                    {appState?.detection.installed ? "已安装" : "未检测到"}
                  </span>
                </div>
                <p>
                  {appState?.detection.message ||
                    "将 AI Router 路由安全合并到应用的本机配置。"}
                  {appState?.detection.version
                    ? ` · ${appState.detection.version}`
                    : ""}
                </p>
              </div>
            </div>
            <div className="application-config-path">
              <span>配置文件</span>
              <code>{appState?.path || "正在检测…"}</code>
              <small>
                {appState?.synced ? "配置已同步" : "尚未同步"} · 保留现有{" "}
                {appState?.preserved_fields || 0} 个顶层配置项
              </small>
            </div>
          </div>

          {!aliases.length && (
            <div className="application-inline-message error">
              请先在“路由配置”中至少创建一条精确模型路由。
            </div>
          )}

          {selectedID !== "claude-code" ? (
            <div className="application-inline-message">
              此应用适配器尚未提供可视化表单。
            </div>
          ) : (
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
                  <div className="application-save-actions">
                    {hasCapability("preview") && (
                      <button
                        disabled={!aliases.length || Boolean(busy)}
                        onClick={refreshPreview}
                      >
                        <RefreshCw size={15} />
                        {busy === "preview" ? "生成中…" : "刷新预览"}
                      </button>
                    )}
                    {hasCapability("verify") && (
                      <button
                        disabled={!aliases.length || Boolean(busy)}
                        onClick={() => verify(false)}
                      >
                        <Check size={15} />
                        {busy === "verify" ? "验证中…" : "验证连接"}
                      </button>
                    )}
                    {hasCapability("configure") && (
                      <button
                        className="primary"
                        disabled={!aliases.length || Boolean(busy)}
                        onClick={save}
                      >
                        <Save size={15} />
                        {busy === "save" ? "正在写入…" : "备份并写入"}
                      </button>
                    )}
                  </div>
                </div>
                {message && (
                  <div
                    className={`application-inline-message ${/已写入|已恢复|验证通过/.test(message) ? "success" : "error"}`}
                  >
                    {message}
                  </div>
                )}

                <ApplicationResults
                  verifyResult={verifyResult}
                  busy={busy}
                  verifyCLI={() => verify(true)}
                  canRollback={hasCapability("rollback")}
                  backups={backups}
                  rollback={rollback}
                />
              </div>

              <div className="application-preview-panel">
                <div className="application-preview-header">
                  <div>
                    <b>配置预览</b>
                    <span>
                      {previewResult?.will_create_backup
                        ? "写入时将创建备份"
                        : "尚无现有配置"}
                    </span>
                  </div>
                  <span>JSON</span>
                </div>
                <pre>
                  {previewResult
                    ? JSON.stringify(previewResult.content, null, 2)
                    : "点击“刷新预览”查看结构化合并结果。"}
                </pre>
                {previewResult?.diff && (
                  <details className="application-preview-diff">
                    <summary>查看字段差异</summary>
                    <pre>{previewResult.diff}</pre>
                  </details>
                )}
                <div className="application-preview-footer">
                  密钥已脱敏 · 保留 {previewResult?.preserved_fields || 0}{" "}
                  个顶层字段
                </div>
              </div>
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
