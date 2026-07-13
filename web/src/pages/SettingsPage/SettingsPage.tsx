import React, { useEffect, useMemo, useState } from "react";
import { Braces, Check, RotateCcw, Save, ShieldCheck } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import type { Status } from "../../types";

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
      const effects = [
        r.hot_reloaded?.length ? `已热加载：${r.hot_reloaded.join("、")}` : "",
        r.runtime_rebuilt?.length
          ? `已重建：${r.runtime_rebuilt.join("、")}`
          : "",
        r.restart_required?.length
          ? `重启后生效：${r.restart_required.join("、")}`
          : "",
      ].filter(Boolean);
      setMessage(
        `配置已保存并备份。${effects.join("；") || "没有运行字段变化"}`,
      );
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
          <p>
            直接维护完整 JSON
            配置；保存后会明确标注热加载、运行对象重建和重启生效项。
          </p>
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
              <dd>按字段分级生效</dd>
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
            <p>
              每次保存前都会验证配置并创建 0600
              权限备份；失败时不会替换运行版本。
            </p>
          </div>
          <div className="settings-sensitive-warning">
            当前编辑器包含完整配置，可能含明文 Provider API
            Key。请勿截图、复制到工单或提交到 Git。
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
                {action === "saving" ? "保存中…" : "保存配置"}
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
