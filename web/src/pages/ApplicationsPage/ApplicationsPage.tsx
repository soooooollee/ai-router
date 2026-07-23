import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, Save, ShieldCheck, Trash2, TriangleAlert } from "lucide-react";
import { api } from "../../app/api";
import { localizeValue } from "../../app/i18n";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import {
  applicationGatewayURL,
  applicationRouteOptionLabel,
  applicationRouteOptions,
} from "../../lib";
import { ApplicationResults } from "./ApplicationResults";
import type {
  AppConfig,
  ApplicationBackup,
  ApplicationCapability,
  ApplicationListItem,
  ApplicationPreview,
  ApplicationState,
  ApplicationVerifyResult,
  ClientListResponse,
  Status,
} from "../../types";

const applicationOrder = [
  "claude-code",
  "claude-app",
  "codex",
  "mimo-code",
];

function previewText(value: ApplicationPreview["content"] | undefined) {
  if (value === undefined) return "";
  return typeof value === "string" ? value : JSON.stringify(value, null, 2);
}

export function ApplicationsPage({
  status,
  config,
}: {
  status: Status | null;
  config: AppConfig | null;
}) {
	const [apps, setApps] = useState<ApplicationListItem[]>([]);
	const [clientKeys, setClientKeys] = useState<ClientListResponse | null>(null);
	const [selectedID, setSelectedID] = useState("claude-code");
	const routeOptions = useMemo(
		() => applicationRouteOptions(config?.routes || [], selectedID, config?.providers || []),
		[config?.routes, config?.providers, selectedID],
	);
	const aliases = useMemo(
		() => routeOptions.map((option) => option.alias),
		[routeOptions],
	);
  const fallback = aliases[0] || "";
  const webRedaction = Boolean(config?.logging?.web_redaction);
  const gateway = status?.gateway_url || "http://127.0.0.1:12666";
  const [appState, setAppState] = useState<ApplicationState | null>(null);
  const [previewResult, setPreviewResult] = useState<ApplicationPreview | null>(
    null,
  );
  const [previewView, setPreviewView] = useState<"current" | "next">("next");
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [editedPreview, setEditedPreview] = useState("");
  const [previewDirty, setPreviewDirty] = useState(false);
  const previewDirtyRef = useRef(false);
	const credentialSelectionTouchedRef = useRef(false);
  const markPreviewDirty = (dirty: boolean) => {
    previewDirtyRef.current = dirty;
    setPreviewDirty(dirty);
  };
  const [verifyResult, setVerifyResult] =
    useState<ApplicationVerifyResult | null>(null);
  const [backups, setBackups] = useState<ApplicationBackup[]>([]);
  const [form, setForm] = useState({
    base_url: gateway,
	api_key: "",
    credential_id: "",
    model: fallback,
    integration_mode: "compatibility" as "direct" | "passthrough" | "compatibility",
    opus_model: fallback,
    sonnet_model: fallback,
    haiku_model: fallback,
  });
  const selectedRouteOption = routeOptions.find(
    (option) => option.alias === form.model,
  );
  const usesCodexCompatibilityFallback =
    selectedID === "codex" &&
    Boolean(selectedRouteOption) &&
    form.integration_mode === "compatibility" &&
    selectedRouteOption?.codex_compatibility !== "full";
  const usesReasoningFallback =
    usesCodexCompatibilityFallback &&
    selectedRouteOption?.reasoning_with_tools === "disabled";
	const selectableKeys = useMemo(() => (clientKeys?.clients || []).flatMap((item) => item.credentials
		.filter((credential) => credential.recoverable && credential.status === "active" && item.client.status === "active" && credential.prefix.startsWith("sk-"))
		.map((credential) => ({ credential, client: item.client }))), [clientKeys]);
	const firstCredentialID = selectableKeys[0]?.credential.id || "";
	const hasRequiredAccessKey = selectedID === "codex" && form.integration_mode === "direct" || Boolean(firstCredentialID);
  const requestConfig = useMemo(
    () => {
      return ({
		...form,
		credential_id: form.credential_id || firstCredentialID,
      models: aliases,
      ...(selectedID === "codex" && selectedRouteOption
        ? {
            provider_id: selectedRouteOption.provider_id,
            provider_name: selectedRouteOption.provider_name,
            provider_base_url: selectedRouteOption.provider_base_url,
            provider_model: selectedRouteOption.provider_model,
          }
        : {}),
    });
    },
    [aliases, firstCredentialID, form, selectedID, selectedRouteOption],
  );
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState("");
  const [confirmation, setConfirmation] = useState<
    | { kind: "cli" }
    | { kind: "rollback"; backup: ApplicationBackup }
    | { kind: "delete-backup"; backup: ApplicationBackup }
    | { kind: "cleanup" }
    | null
  >(null);

  const selectedApp = apps.find((item) => item.manifest.id === selectedID);
	const supportsConfigForm = [
		"claude-code",
		"claude-app",
		"codex",
		"mimo-code",
	].includes(selectedID);
	const isClaudeApplication =
		selectedID === "claude-code" || selectedID === "claude-app";
  const hasCapability = (capability: ApplicationCapability) =>
    selectedApp?.manifest.capabilities.includes(capability) ?? false;
  const canPreview = hasCapability("preview");

  const loadApplications = useCallback(async () => {
    // The list only needs manifests. CLI detection belongs to the selected app
    // request and can take seconds for missing or damaged installations.
    const value = await api("/api/apps?detect=false");
    const items = ((value.apps || []) as ApplicationListItem[]).sort(
      (left, right) => {
        const leftIndex = applicationOrder.indexOf(left.manifest.id);
        const rightIndex = applicationOrder.indexOf(right.manifest.id);
        return (leftIndex < 0 ? applicationOrder.length : leftIndex) -
          (rightIndex < 0 ? applicationOrder.length : rightIndex);
      },
    );
    setApps(items);
		api("/api/clients").then((value) => setClientKeys(value as ClientListResponse)).catch(() => setClientKeys(null));
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
    if (supportsConfigForm) {
      const managed = state.managed || {};
			const currentCredentialID = typeof managed.airoute_client_credential_id === "string" && managed.airoute_client_credential_recoverable === true
				&& typeof managed.airoute_client_credential_prefix === "string" && managed.airoute_client_credential_prefix.startsWith("sk-")
				? managed.airoute_client_credential_id
				: "";
      const selected = (candidate: unknown) =>
        typeof candidate === "string" && aliases.includes(candidate)
          ? candidate
          : fallback;
      const isDesktop = selectedID === "claude-app";
      const read = (desktopKey: string, codeKey: string) =>
        isClaudeApplication
          ? managed[isDesktop ? desktopKey : codeKey]
          : managed[desktopKey];
      const managedModel = read("model", "ANTHROPIC_MODEL");
      const selectedModel =
        typeof managedModel === "string"
          ? routeOptions.find(
              (option) =>
                option.alias === managedModel ||
                (managed.integration_mode === "direct" &&
                  option.provider_model === managedModel),
            )?.alias || fallback
          : fallback;
      setForm((current) => ({
        base_url: applicationGatewayURL(
          read("base_url", "ANTHROPIC_BASE_URL"),
          gateway,
        ),
				credential_id: credentialSelectionTouchedRef.current ? current.credential_id : currentCredentialID || firstCredentialID,
				api_key: "",
        model: selectedModel,
        integration_mode:
          selectedID === "codex"
            ? (routeOptions.find((option) => option.alias === selectedModel)?.integration_mode ||
              (managed.integration_mode as typeof current.integration_mode) ||
              "compatibility")
            : current.integration_mode,
        opus_model: selected(read("opus_model", "ANTHROPIC_DEFAULT_OPUS_MODEL")),
        sonnet_model: selected(read("sonnet_model", "ANTHROPIC_DEFAULT_SONNET_MODEL")),
        haiku_model: selected(read("haiku_model", "ANTHROPIC_DEFAULT_HAIKU_MODEL")),
      }));
    }
  }, [aliases, fallback, firstCredentialID, gateway, isClaudeApplication, routeOptions, selectedID, supportsConfigForm]);

  useEffect(() => {
    loadApplications().catch((error) => setMessage((error as Error).message));
  }, [loadApplications]);

  useEffect(() => {
    setAppState(null);
    setBackups([]);
    setPreviewResult(null);
    setEditedPreview("");
    markPreviewDirty(false);
    setPreviewView("next");
    setVerifyResult(null);
    loadApplication().catch((error) => setMessage((error as Error).message));
	}, [loadApplication]);

	useEffect(() => {
		credentialSelectionTouchedRef.current = false;
	}, [selectedID]);

  useEffect(() => {
    if (!selectedID || !aliases.length || !canPreview) {
      setPreviewing(false);
      setPreviewError("");
      return;
    }
    const controller = new AbortController();
    setPreviewing(true);
    const timer = window.setTimeout(async () => {
      try {
        const value = (await api(`/api/apps/${selectedID}/preview`, {
          method: "POST",
          body: JSON.stringify(requestConfig),
          signal: controller.signal,
        })) as ApplicationPreview;
        if (!controller.signal.aborted) {
          setPreviewResult(value);
          if (!previewDirtyRef.current) {
            setEditedPreview(previewText(value.content));
            markPreviewDirty(false);
          }
          setPreviewError("");
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setPreviewError((error as Error).message);
        }
      } finally {
        if (!controller.signal.aborted) setPreviewing(false);
      }
    }, 300);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [aliases, canPreview, requestConfig, selectedID, webRedaction]);

  async function save() {
    setBusy("save");
    setMessage("");
    try {
      const nextPreview = (await api(`/api/apps/${selectedID}/preview`, {
        method: "POST",
        body: JSON.stringify(requestConfig),
      })) as ApplicationPreview;
      setPreviewResult(nextPreview);
      setEditedPreview(previewText(nextPreview.content));
      markPreviewDirty(false);
      const result = await api(`/api/apps/${selectedID}/config`, {
        method: "PUT",
        body: JSON.stringify(requestConfig),
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

  async function saveEditedPreview() {
    setBusy("raw-save");
    setMessage("");
    try {
      const result = await api(`/api/apps/${selectedID}/raw-config`, {
        method: "PUT",
        body: JSON.stringify({
          content: editedPreview,
          config: requestConfig,
        }),
      });
      setMessage(
        result.backup
          ? `已写入手动修改的配置，备份 ${result.backup} 已创建。`
          : "已写入手动修改的配置。",
      );
      markPreviewDirty(false);
      await loadApplication();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function cleanup() {
    setBusy("cleanup");
    setMessage("");
    try {
      const result = await api(`/api/apps/${selectedID}/cleanup`, {
        method: "POST",
      });
      setMessage(
        result.backup
          ? `已清理旧配置，备份 ${result.backup} 已创建。`
          : "已清理旧配置。",
      );
      await loadApplication();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function verify(runCLI: boolean) {
    setBusy(runCLI ? "cli" : "verify");
    setMessage("");
    try {
      const result = (await api(`/api/apps/${selectedID}/verify`, {
        method: "POST",
        body: JSON.stringify({ config: requestConfig, run_cli: runCLI }),
      })) as ApplicationVerifyResult;
      setVerifyResult(result);
      setMessage(
        result.ok
          ? "应用链路验证通过。"
          : "部分验证未通过，请查看阶段详情。",
      );
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function rollback(backup: ApplicationBackup) {
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

  async function deleteBackup(backup: ApplicationBackup) {
    setBusy(`delete:${backup.name}`);
    setMessage("");
    try {
      await api(`/api/apps/${selectedID}/backups`, {
        method: "DELETE",
        body: JSON.stringify({ name: backup.name }),
      });
      setMessage(`已删除备份 ${backup.name}。`);
      await loadApplication();
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setBusy("");
    }
  }

  async function confirmAction() {
    if (!confirmation) return;
    if (confirmation.kind === "cli") await verify(true);
    else if (confirmation.kind === "rollback") await rollback(confirmation.backup);
    else if (confirmation.kind === "delete-backup") await deleteBackup(confirmation.backup);
    else await cleanup();
    setConfirmation(null);
  }

  function modelSelect(
    value: string,
    key: "model" | "opus_model" | "sonnet_model" | "haiku_model",
    label: string,
  ) {
    const selectedOption = routeOptions.find((option) => option.alias === value);
    const selectedLabel = selectedOption
      ? applicationRouteOptionLabel(selectedOption)
      : "";
    return (
      <label>
        {label}
        <select
          aria-label={label}
          value={value}
          title={selectedLabel || undefined}
          onChange={(event) => {
            const nextValue = event.target.value;
            const option = routeOptions.find((item) => item.alias === nextValue);
            setForm({
              ...form,
              [key]: nextValue,
              ...(selectedID === "codex" && key === "model"
                ? { integration_mode: option?.integration_mode || "compatibility" }
                : {}),
            });
          }}
        >
          <option value="">不设置</option>
          {routeOptions.map((option) => (
            <option
              key={option.alias}
              value={option.alias}
              title={applicationRouteOptionLabel(option)}
            >
              {applicationRouteOptionLabel(option)}
            </option>
          ))}
        </select>
      </label>
    );
  }

  return (
    <div className="application-config-page console-page">
      <div className="page-toolbar">
        <div><h1>应用配置</h1><p>为本地开发工具维护连接设置和模型角色映射。</p></div>
        <span className="filter-count">{apps.length} 个应用 · {aliases.length} 条可用路由</span>
      </div>
      <div className="horizontal-sheets" role="tablist" aria-label="应用配置">
        {apps.map((item) => (
          <button
            className={selectedID === item.manifest.id ? "active" : ""}
            type="button"
            role="tab"
            aria-selected={selectedID === item.manifest.id}
            key={item.manifest.id}
            onClick={() => setSelectedID(item.manifest.id)}
          >{item.manifest.name}</button>
        ))}
      </div>
      <section className="application-config-workbench">
        <div className="application-detail-panel">
          <div className="application-detail-header">
            <div className="application-detail-identity">
              <div>
                <div className="application-detail-title">
                  <h3>{selectedApp?.manifest.name || "应用配置"}</h3>
                  <span>
                    {!appState
                      ? "检测中"
                      : appState.detection.installed
                      ? "已安装"
                      : appState.detection.executable
                        ? "不可用"
                        : "未检测到"}
                  </span>
                </div>
                <p>
                  {localizeValue(appState?.detection.message ||
                    "将 AI Router 路由安全合并到应用的本机配置。")}
                  {appState?.detection.version
                    ? ` · ${appState.detection.version}`
                    : ""}
                </p>
              </div>
            </div>
            <div className="application-config-path">
              <span>配置文件</span>
              <code
                title={appState?.path}
                aria-label={appState?.path || undefined}
                tabIndex={appState?.path ? 0 : undefined}
              >
                {appState?.path || "正在检测…"}
              </code>
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

          {!supportsConfigForm ? (
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
                      <span>
						{selectedID === "claude-app"
							? "Claude App 通过本机第三方网关连接 AI Router"
							: selectedID === "codex"
								? "Codex CLI 与 ChatGPT App 通过 Responses 协议连接 AI Router"
								: selectedID === "mimo-code"
									? "MiMo Code 通过 OpenAI 兼容协议连接 AI Router"
									: "Claude Code 连接到本机 AI Router"}
					</span>
                    </div>
                    <span>01</span>
                  </div>
                  <div className="application-connection-fields">
                    {selectedID === "codex" && form.integration_mode === "direct" ? (
                      <label>
                        上游 Responses 地址
                        <input value={selectedRouteOption?.provider_base_url || ""} readOnly />
                      </label>
                    ) : (
                      <>
                    <label>
                      AI Router 地址
                      <input
                        value={form.base_url}
                        onChange={(event) =>
                          setForm({ ...form, base_url: event.target.value })
                        }
                      />
                    </label>
					{selectableKeys.length ? <label className="full">
						AI Router 访问密钥
						<select
							aria-label="AI Router 访问密钥"
							value={form.credential_id || firstCredentialID}
							onChange={(event) => {
								credentialSelectionTouchedRef.current = true;
								setForm({
									...form,
									credential_id: event.target.value,
									api_key: "",
								});
							}}
						>
							{selectableKeys.map(({ credential, client }) => <option key={credential.id} value={credential.id}>{client.name}</option>)}
						</select>
					</label> : <div className="application-key-empty" role="alert">
						<TriangleAlert size={17} />
						<div>
							<b>还没有访问密钥</b>
							<span>请先生成一个访问密钥，再继续配置应用。</span>
						</div>
						<button type="button" onClick={() => { location.hash = "clients"; }}>生成密钥</button>
					</div>}
                      </>
                    )}
                  </div>
                </section>

                <section className="application-form-section">
                  <div className="application-section-title">
                    <div>
					  <b>{isClaudeApplication ? "模型角色映射" : "模型设置"}</b>
                      <span>可选模型来源于已经保存的路由</span>
                    </div>
                    <span>02</span>
                  </div>
                  <div className="application-role-grid">
					{modelSelect(
						form.model,
						"model",
						"默认模型",
					)}
					{isClaudeApplication && (
						<>
							{modelSelect(
								form.sonnet_model,
								"sonnet_model",
								"Sonnet 角色",
							)}
							{modelSelect(form.opus_model, "opus_model", "Opus 角色")}
							{modelSelect(form.haiku_model, "haiku_model", "Haiku 角色")}
						</>
					)}
                  </div>
                  {usesCodexCompatibilityFallback && (
                    <div className="application-compatibility-warning" role="alert">
                      <TriangleAlert size={17} />
                      <div>
                        <b>{localizeValue("正在使用 AI Router 兼容转换")}</b>
                        <span>
                          {localizeValue(usesReasoningFallback
                            ? "Codex 使用 high 推理等级时通常会同时发送 tools（如 apply_patch、shell 等工具定义）和 reasoning_effort（用于指定模型推理强度）。如果上游 Chat 接口拒绝 tools + reasoning_effort，AI Router 会在工具请求中移除 reasoning_effort 并保留工具调用；普通对话和工具调用仍可正常使用。"
                            : "Codex 仍使用 Responses；AI Router 会根据检测结果转换协议或修复 custom tools 与 reasoning 差异。能力边界以模型接入检测结果为准。")}
                        </span>
                      </div>
                    </div>
                  )}
                </section>

                <div className="application-save-bar">
                  <div>
                    <ShieldCheck size={16} />
					<span>
						{selectedID === "claude-app"
							? "写入 Claude-3p 独立配置，保存后需重启 Claude App。"
							: selectedID === "codex"
								? "Codex CLI、ChatGPT App 与 IDE 扩展共享 ~/.codex/config.toml；写入后请重启正在使用的客户端。"
								: selectedID === "mimo-code"
									? "仅更新 MiMo Code 的 AI Router provider，保留其他 Provider 和设置。"
									: "写入前自动备份，不覆盖 Hooks、插件和权限配置。"}
					</span>
                  </div>
                  <div className="application-save-actions">
                    {hasCapability("cleanup") && (
                      <button
                        className="cleanup"
                        disabled={Boolean(busy)}
                        onClick={() => setConfirmation({ kind: "cleanup" })}
                      >
                        <Trash2 size={15} />
                        {busy === "cleanup" ? "清理中…" : "清理旧配置"}
                      </button>
                    )}
                    {hasCapability("verify") && (
                      <button
                        disabled={!aliases.length || !hasRequiredAccessKey || Boolean(busy)}
                        onClick={() => verify(false)}
                      >
                        <Check size={15} />
                        {busy === "verify" ? "验证中…" : "验证连接"}
                      </button>
                    )}
                    {hasCapability("configure") && (
                      <button
                        className="primary"
                        disabled={!aliases.length || !hasRequiredAccessKey || Boolean(busy)}
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
                    className={`application-inline-message ${/已写入|已恢复|已清理|验证通过/.test(message) ? "success" : "error"}`}
                  >
                    {localizeValue(message)}
                  </div>
                )}

                <ApplicationResults
                  verifyResult={verifyResult}
                  busy={busy}
                  verifyCLI={selectedID === "claude-code" ? () => setConfirmation({ kind: "cli" }) : undefined}
                  canRollback={hasCapability("rollback")}
                  backups={backups}
                  rollback={(backup) => setConfirmation({ kind: "rollback", backup })}
                  deleteBackup={(backup) => setConfirmation({ kind: "delete-backup", backup })}
                />
              </div>

              <div className="application-preview-panel">
                <div className="application-preview-header">
                  <div>
                    <b>配置预览</b>
                    <span>
                      {previewing
                        ? "实时更新中…"
                        : previewError
                          ? "实时预览暂不可用"
                          : previewResult?.will_create_backup
                        ? "写入时将创建备份"
                          : "尚无现有配置"}
                    </span>
                  </div>
                  <div className="application-preview-tabs" role="tablist" aria-label="配置预览视图">
                    <button
                      type="button"
                      role="tab"
                      aria-selected={previewView === "current"}
                      className={previewView === "current" ? "active" : ""}
                      onClick={() => setPreviewView("current")}
                    >
                      当前配置
                    </button>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={previewView === "next"}
                      className={previewView === "next" ? "active" : ""}
                      onClick={() => setPreviewView("next")}
                    >
                      合并后配置
                    </button>
                  </div>
                </div>
                {previewView === "next" &&
                previewResult &&
                hasCapability("edit-preview") &&
                !previewError ? (
                  <textarea
                    className="application-preview-editor"
                    aria-label="编辑合并后配置"
                    value={editedPreview}
                    readOnly={webRedaction}
                    onChange={(event) => {
                      setEditedPreview(event.target.value);
                      markPreviewDirty(
                        event.target.value !== previewText(previewResult.content),
                      );
                    }}
                  />
                ) : (
                  <pre>
                    {previewError
                      ? `预览生成失败：${previewError}`
                      : previewResult
                        ? previewText(
                            previewView === "current"
                              ? previewResult.current
                              : previewResult.content,
                          )
                        : "正在生成实时预览…"}
                  </pre>
                )}
                {previewResult?.diff && (
                  <details className="application-preview-diff">
                    <summary>查看字段差异</summary>
                    <pre>{previewResult.diff}</pre>
                  </details>
                )}
                <div className="application-preview-footer">
                  <span>
                    {webRedaction
                      ? "网页脱敏开启时不可手动写入"
                      : previewDirty
                        ? "预览已修改，写入前会校验格式"
                        : "可直接编辑合并后配置"}
                    {" · "}保留 {previewResult?.preserved_fields || 0} 个顶层字段
                  </span>
                  {hasCapability("edit-preview") && (
                    <button
                      disabled={
                        webRedaction || !previewDirty || !hasRequiredAccessKey || Boolean(busy)
                      }
                      onClick={saveEditedPreview}
                    >
                      <Save size={14} />
                      {busy === "raw-save" ? "写入中…" : "备份并写入修改"}
                    </button>
                  )}
                </div>
              </div>
            </div>
          )}
        </div>
      </section>
      <ConfirmDialog
        open={Boolean(confirmation)}
        title={
          confirmation?.kind === "cli"
            ? "运行完整验证？"
            : confirmation?.kind === "cleanup"
              ? "清理旧配置？"
            : confirmation?.kind === "delete-backup"
              ? "删除配置备份？"
              : "恢复应用配置备份？"
        }
        description={
          confirmation?.kind === "cli" ? (
            <>Claude Code 将发起一次真实模型请求，可能产生少量 Token 费用。</>
          ) : confirmation?.kind === "cleanup" ? (
            <>
              将删除 <b>{selectedApp?.manifest.name}</b> 中由 AI Router
              管理或可能产生冲突的旧配置。其他设置会保留，当前内容会先自动备份。
              {selectedID === "codex" && (
                <> ChatGPT App 与 Codex CLI 共享此配置，清理会同时影响两者。</>
              )}
            </>
          ) : confirmation?.kind === "delete-backup" ? (
            <>将永久删除备份 <b>{confirmation.backup.name}</b>，此操作无法撤销。</>
          ) : (
            <>将恢复备份 <b>{confirmation?.backup.name}</b>，当前配置会先自动保留。</>
          )
        }
        confirmLabel={
          confirmation?.kind === "cli"
            ? "运行完整验证"
            : confirmation?.kind === "cleanup"
              ? "备份并清理"
            : confirmation?.kind === "delete-backup"
              ? "删除备份"
              : "确认恢复"
        }
        danger={
          confirmation?.kind === "delete-backup" ||
          confirmation?.kind === "cleanup"
        }
        busy={Boolean(busy)}
        onCancel={() => setConfirmation(null)}
        onConfirm={confirmAction}
      />
    </div>
  );
}
