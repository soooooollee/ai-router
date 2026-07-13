import React, { useState } from "react";
import { Activity, Check, Save } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { protocolName } from "../../lib";

export function ProviderDialog({
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
            <small className="secret-storage-hint">
              {/^\$\{[A-Za-z_][A-Za-z0-9_]*\}$/.test(form.api_key)
                ? `环境变量模式：${form.api_key.slice(2, -1)}`
                : form.api_key
                  ? "本地便利模式：明文保存到 0600 配置和备份"
                  : "支持直接密钥，或填写 ${ENV_NAME} 使用环境变量"}
            </small>
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
