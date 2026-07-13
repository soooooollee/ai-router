import React, { useEffect, useMemo, useState } from "react";
import { Check, RotateCcw, Save } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import type { Status } from "../../types";
import { currentLocale } from "../../app/i18n";

const sectionNames: Record<string, { label: string; description: string }> = {
  logging: { label: "日志记录", description: "级别、格式与历史记录" },
  __privacy__: { label: "网页脱敏", description: "管理页面中的敏感信息显示方式" },
};

export function SettingsPage({
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
  const document = useMemo<Record<string, unknown>>(() => {
    try {
      return parse(yaml) || {};
    } catch {
      return {};
    }
  }, [yaml]);
  const sectionKeys = useMemo(
    () => (Object.prototype.hasOwnProperty.call(document, "logging") ? ["logging", "__privacy__"] : []),
    [document],
  );
  const [sheet, setSheet] = useState("__all__");
  const sourceJSON = useMemo(
    () => JSON.stringify(
      sheet === "__all__"
        ? document
        : sheet === "__privacy__"
          ? { web_redaction: Boolean((document.logging as Record<string, unknown> | undefined)?.web_redaction) }
          : document[sheet],
      null,
      2,
    ),
    [document, sheet],
  );
  const [draft, setDraft] = useState(sourceJSON);
  const [message, setMessage] = useState("");
  const [action, setAction] = useState<"" | "validating" | "saving" | "rolling-back">("");
  const [rollbackName, setRollbackName] = useState("");
  const dirty = draft !== sourceJSON;
  const activeName =
    sheet === "__all__"
      ? { label: "完整配置", description: "所有本地运行配置" }
      : sectionNames[sheet] || { label: sheet, description: "自定义配置分组" };
  const loggingDraft = useMemo<Record<string, any>>(() => {
    if (sheet !== "logging") return {};
    try { return JSON.parse(draft) || {}; } catch { return {}; }
  }, [draft, sheet]);
  const privacyDraft = useMemo<Record<string, any>>(() => {
    if (sheet !== "__privacy__") return {};
    try { return JSON.parse(draft) || {}; } catch { return {}; }
  }, [draft, sheet]);
  const webRedactionEnabled = Boolean((document.logging as Record<string, unknown> | undefined)?.web_redaction);

  useEffect(() => setDraft(sourceJSON), [sourceJSON]);
  useEffect(() => {
    if (sheet !== "__all__" && !sectionKeys.includes(sheet)) setSheet("__all__");
  }, [sectionKeys, sheet]);

  function asYAML() {
    const parsedDraft = JSON.parse(draft);
    const nextDocument =
      sheet === "__all__"
        ? parsedDraft
        : sheet === "__privacy__"
          ? {
              ...document,
              logging: {
                ...((document.logging as Record<string, unknown> | undefined) || {}),
                web_redaction: Boolean(parsedDraft.web_redaction),
              },
            }
          : { ...document, [sheet]: parsedDraft };
    return stringify(nextDocument, { lineWidth: 0 });
  }

  function updateLogging(key: string, value: unknown) {
    let current: Record<string, unknown> = {};
    try { current = JSON.parse(draft) || {}; } catch { /* validation will report malformed JSON */ }
    setDraft(JSON.stringify({ ...current, [key]: value }, null, 2));
    setMessage("");
  }

  function updatePrivacy(value: boolean) {
    setDraft(JSON.stringify({ web_redaction: value }, null, 2));
    setMessage("");
  }

  async function validate() {
    setAction("validating");
    try {
      await api("/api/config/validate", {
        method: "POST",
        body: JSON.stringify({ yaml: asYAML() }),
      });
      setMessage(
        sheet === "__all__"
          ? "JSON 格式和 AI Router 配置均有效"
          : `${activeName.label} JSON 格式和 AI Router 配置均有效`,
      );
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setAction("");
    }
  }

  async function save() {
    setAction("saving");
    try {
      const nextYAML = asYAML();
      const result = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: nextYAML, expected_hash: hash }),
      });
      const effects = [
        result.hot_reloaded?.length ? `已热加载：${result.hot_reloaded.join("、")}` : "",
        result.runtime_rebuilt?.length ? `已重建：${result.runtime_rebuilt.join("、")}` : "",
        result.restart_required?.length ? `重启后生效：${result.restart_required.join("、")}` : "",
      ].filter(Boolean);
      setMessage(`${activeName.label}已保存并备份。${effects.join("；") || "没有运行字段变化"}`);
      onSaved(nextYAML, result.hash);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setAction("");
    }
  }

  async function prepareRollback() {
    setAction("rolling-back");
    try {
      const result = await api("/api/config/backups");
      if (!result.backups?.[0]) {
        setMessage("暂无可回滚的配置备份");
        return;
      }
      setRollbackName(result.backups[0].name);
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setAction("");
    }
  }

  async function rollback() {
    if (!rollbackName) return;
    setAction("rolling-back");
    try {
      const rollbackResult = await api("/api/config/rollback", {
        method: "POST",
        body: JSON.stringify({ name: rollbackName }),
      });
      const latest = await api("/api/config");
      onSaved(latest.yaml, rollbackResult.hash);
      setMessage("已回滚到最近的配置备份");
      setRollbackName("");
    } catch (error) {
      setMessage((error as Error).message);
    } finally {
      setAction("");
    }
  }

  return (
    <div className="system-settings-page console-page">
      <div className="page-toolbar">
        <div><h1>系统设置</h1><p>{webRedactionEnabled ? "网页脱敏已开启，敏感字段使用占位符显示。" : "查看和编辑本机完整运行配置，包括密钥。"}</p></div>
        <span className={`status-pill ${status?.status === "running" ? "ok" : "bad"}`}>
          {status?.status === "running" ? "配置正在生效" : "网关已关闭"}
        </span>
      </div>
      <div className="horizontal-sheets settings-horizontal-sheets" role="tablist" aria-label="配置分组">
        <button type="button" role="tab" aria-selected={sheet === "__all__"} className={sheet === "__all__" ? "active" : ""} onClick={() => { setSheet("__all__"); setMessage(""); }}>完整配置</button>
        {sectionKeys.map((key) => {
          const info = sectionNames[key] || { label: key, description: "自定义配置分组" };
          return <button type="button" role="tab" aria-selected={sheet === key} className={sheet === key ? "active" : ""} key={key} onClick={() => { setSheet(key); setMessage(""); }}>{info.label}</button>;
        })}
      </div>
      <section className="settings-workbench">
        <div className="settings-editor-panel">
          <div className="settings-editor-toolbar">
            <div className="settings-file-title">
              <span className="settings-file-icon">{"{ }"}</span>
              <div><b>{activeName.label}</b><small>{activeName.description}</small></div>
            </div>
            <div className="settings-editor-actions">
              <button className="secondary" onClick={prepareRollback} disabled={Boolean(action)}>
                <RotateCcw size={14} />{action === "rolling-back" ? "回滚中…" : "回滚"}
              </button>
              <button className="secondary" onClick={validate} disabled={Boolean(action)}>
                <Check size={14} />{action === "validating" ? "校验中…" : "校验"}
              </button>
              <button className="primary" disabled={!dirty || Boolean(action)} onClick={save}>
                <Save size={14} />{action === "saving" ? "保存中…" : "保存配置"}
              </button>
            </div>
          </div>
          {message && (
            <div className={`settings-message ${/有效|保存|回滚到/.test(message) ? "success" : "error"}`}>
              <span />{message}<button aria-label="关闭提示" onClick={() => setMessage("")}>×</button>
            </div>
          )}
          {sheet === "logging" ? (
            <div className="logging-settings-form">
              <div className="logging-setting-row">
                <div><b>日志持久化</b><span>将调用日志和聊天正文写入本机 JSONL 文件，服务重启后自动载入。</span></div>
                <label className="settings-switch"><input style={{ pointerEvents: "auto", inset: 0, width: "100%", height: "100%", margin: 0, zIndex: 2, cursor: "pointer" }} aria-label="日志持久化" type="checkbox" checked={Boolean(loggingDraft.persist)} onChange={(event) => updateLogging("persist", event.target.checked)} /><i /></label>
              </div>
              {Boolean(loggingDraft.persist) && <div className="logging-path-field"><label>日志文件路径<input aria-label="日志文件路径" value={loggingDraft.file || ""} placeholder="留空时保存到配置文件同目录的 airoute-requests.jsonl" onChange={(event) => updateLogging("file", event.target.value)} /></label><small>文件达到约 10 MB 后自动轮转，保留最近 3 个历史文件。</small></div>}
              <div className="logging-setting-row">
                <div><b>记录聊天正文</b><span>保存请求和响应内容，关闭后日志详情只显示路由、状态、耗时和 Token。</span></div>
                <label className="settings-switch"><input style={{ pointerEvents: "auto", inset: 0, width: "100%", height: "100%", margin: 0, zIndex: 2, cursor: "pointer" }} aria-label="记录聊天正文" type="checkbox" checked={Boolean(loggingDraft.capture_bodies)} onChange={(event) => updateLogging("capture_bodies", event.target.checked)} /><i /></label>
              </div>
              <div className="logging-basic-fields">
                <label>日志级别<select aria-label="日志级别" value={loggingDraft.level || "info"} onChange={(event) => updateLogging("level", event.target.value)}><option value="debug">Debug</option><option value="info">Info</option><option value="warn">Warn</option><option value="error">Error</option></select></label>
                <label>内存保留条数<input aria-label="内存保留条数" type="number" min={1} max={100000} value={loggingDraft.request_history || 500} onChange={(event) => updateLogging("request_history", Number(event.target.value))} /></label>
              </div>
              <div className="logging-privacy-note"><b>本地数据说明</b><span>{loggingDraft.persist ? "日志会写入本机磁盘。" : "日志当前只保存在进程内存，重启后清空。"}聊天正文是否记录由上方开关控制。</span></div>
            </div>
          ) : sheet === "__privacy__" ? (
            <div className="logging-settings-form privacy-settings-form">
              <div className="logging-setting-row">
                <div><b>网页脱敏</b><span>控制管理网页是否隐藏 API Key、Token、Cookie、密码等敏感字段。</span></div>
                <label className="settings-switch"><input style={{ pointerEvents: "auto", inset: 0, width: "100%", height: "100%", margin: 0, zIndex: 2, cursor: "pointer" }} aria-label="网页脱敏" type="checkbox" checked={Boolean(privacyDraft.web_redaction)} onChange={(event) => updatePrivacy(event.target.checked)} /><i /></label>
              </div>
              <div className="privacy-scope-grid">
                <div><b>开启后</b><span>模型列表、应用配置、完整配置和日志详情中的敏感字段显示为占位符。</span></div>
                <div><b>关闭后</b><span>管理网页按原文显示本机保存的密钥，便于直接查看和复制。</span></div>
                <div><b>数据存储</b><span>该设置只影响网页展示，不会修改配置文件或持久化日志中的真实内容。</span></div>
              </div>
              <div className="logging-privacy-note"><b>当前状态</b><span>{privacyDraft.web_redaction ? "网页脱敏已开启，保存后敏感字段将隐藏。" : "网页脱敏已关闭，保存后敏感字段将按原文显示。"}</span></div>
            </div>
          ) : <>
            <div className="settings-editor-meta"><span>JSON</span><span>{draft.split("\n").length} 行</span><span>{draft.length.toLocaleString(currentLocale())} 字符</span><span className={dirty ? "is-dirty" : "is-synced"}>{dirty ? "未保存" : "已同步"}</span></div>
            <textarea aria-label="系统 JSON 配置" className="settings-json-editor" spellCheck={false} value={draft} onChange={(event) => { setDraft(event.target.value); setMessage(""); }} />
            <div className="settings-editor-footer"><span>当前显示：{activeName.label}，{webRedactionEnabled ? "敏感字段已脱敏，保存时会自动保留真实值。" : "密钥不会被隐藏。"}</span><code>UTF-8 · 2 空格缩进</code></div>
          </>}
        </div>
      </section>
      <ConfirmDialog
        open={Boolean(rollbackName)}
        title="回滚系统配置？"
        description={<>系统将恢复备份 <b>{rollbackName}</b>，当前配置会被替换，但仍会保留为备份。</>}
        confirmLabel="确认回滚"
        busy={action === "rolling-back"}
        onCancel={() => setRollbackName("")}
        onConfirm={rollback}
      />
    </div>
  );
}
