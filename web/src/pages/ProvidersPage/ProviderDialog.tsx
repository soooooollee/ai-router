import React, { useState } from "react";
import { Activity, Check, Eye, EyeOff, Save, TriangleAlert } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { localizeValue } from "../../app/i18n";
import { generatedProviderID, generatedProviderRoutes, protocolName } from "../../lib";

type CapabilityCheck = {
  ok: boolean;
  state?: "supported" | "unsupported" | "inconclusive" | "not_tested";
  confidence?: number;
  status?: number;
  latency_ms?: number;
  error_code?: string;
  error?: string;
  evidence?: string[];
};

type ProtocolCapabilities = {
  basic: CapabilityCheck;
  streaming?: CapabilityCheck;
  tools?: CapabilityCheck;
  reasoning?: CapabilityCheck;
  tools_with_reasoning?: CapabilityCheck;
  tool_round_trip?: CapabilityCheck;
  codex_direct?: CapabilityCheck;
  codex_end_to_end?: CapabilityCheck;
};

type DetectionResult = {
  ok: boolean;
  protocol?: string;
  profile?: string;
  label?: string;
  latency_ms?: number;
  detector_version?: number;
  cached?: boolean;
  model_reports?: Record<string, { protocol?: string; basic: CapabilityCheck }>;
  attempts?: Array<{ protocol?: string; status?: number; error?: string }>;
  protocols?: Record<string, ProtocolCapabilities>;
  codex_compatibility?: {
    status: "full" | "degraded" | "unverified" | "incompatible" | "unavailable";
    protocol?: string;
    message: string;
    recommended_omit_fields?: string[];
    confidence?: number;
    verified?: boolean;
    recommended_integration_mode?: "direct" | "passthrough" | "compatibility";
    recommended_compatibility_mode?: "codex-chat" | "codex-responses";
    recommended_tool_choice_mode?: "standard" | "required" | "auto-only";
    recommended_reasoning_history?: "preserve" | "drop";
    recommended_reasoning_with_tools?: "supported" | "disabled";
  };
};

function capabilityText(check?: CapabilityCheck) {
  if (!check || check.state === "not_tested") return "未测试";
  if (check.state === "supported" || check.ok) return "已支持";
  if (check.state === "inconclusive") return "尚未确认";
  return "不支持";
}

function capabilityDiagnostic(check?: CapabilityCheck) {
  if (!check || check.state === "supported" || check.state === "not_tested") return "";
  const code = (check.error_code || "").toLowerCase();
  const error = (check.error || "").toLowerCase();
  let reason = "";
  if (code.includes("timeout") || error.includes("deadline exceeded")) {
    reason = "请求超时";
  } else if (code === "tool_not_observed") {
    reason = "请求已被接受，但模型未返回工具调用";
  } else if (code === "schema_mismatch") {
    reason = "响应结构不符合预期";
  } else if (code.includes("sse") || code.includes("stream") || code.includes("content_type")) {
    reason = "流式响应结构不符合预期";
  } else if (code.includes("custom_tool_not_observed")) {
    reason = "未观察到模型触发 apply_patch custom tool";
  } else if (code.includes("call_id_missing")) {
    reason = "custom tool 调用缺少 call ID";
  } else if (code.includes("round_trip")) {
    reason = "第二轮工具结果续接失败";
  } else if (check.error_code) {
    reason = check.error_code;
  } else if (check.error) {
    reason = check.error.slice(0, 120);
  }
  const latency = check.latency_ms
    ? check.latency_ms >= 1000
      ? `${(check.latency_ms / 1000).toFixed(1)} 秒`
      : `${check.latency_ms} ms`
    : "";
  return [reason, latency].filter(Boolean).join(" · ");
}

function capabilityResultText(check?: CapabilityCheck) {
  const result = capabilityText(check);
  const diagnostic = capabilityDiagnostic(check);
  return diagnostic ? `${result} · ${diagnostic}` : result;
}

function compatibilityTitle(
  status: NonNullable<DetectionResult["codex_compatibility"]>["status"],
  mode?: NonNullable<DetectionResult["codex_compatibility"]>["recommended_integration_mode"],
) {
  switch (status) {
    case "full":
      if (mode === "direct") return "Codex 官方直连完整兼容";
      if (mode === "compatibility") return "Codex 经 AI Router 完整兼容";
      return "Codex 完整兼容";
    case "degraded":
      return mode === "compatibility"
        ? "Codex CLI / ChatGPT App 经 AI Router 兼容"
        : "Codex 兼容";
    case "unverified":
      return mode === "compatibility"
        ? "Codex 经 AI Router 待验证"
        : "Codex 尚未验证";
    case "unavailable": return "Codex 检测未完成";
    default: return "Codex 不兼容";
  }
}

async function streamProviderDetection(
  body: Record<string, unknown>,
  onProgress: (message: string) => void,
): Promise<DetectionResult> {
  const token = sessionStorage.getItem("airoute_token") || "";
  const response = await fetch("/api/providers/detect?stream=1", {
    method: "POST",
    headers: {
      "content-type": "application/json",
      ...(token ? { authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(body),
  });
  if (!response.ok || !response.body) {
    const failure = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(failure.error || `HTTP ${response.status}`);
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let result: DetectionResult | null = null;
  for (;;) {
    const chunk = await reader.read();
    buffer += decoder.decode(chunk.value || new Uint8Array(), { stream: !chunk.done });
    const frames = buffer.split(/\r?\n\r?\n/);
    buffer = frames.pop() || "";
    for (const frame of frames) {
      const event = frame.split(/\r?\n/).find((line) => line.startsWith("event:"))?.slice(6).trim();
      const data = frame.split(/\r?\n/).find((line) => line.startsWith("data:"))?.slice(5).trim();
      if (!data) continue;
      const value = JSON.parse(data);
      if (event === "progress") onProgress(value.message || "正在检测…");
      if (event === "result") result = value;
    }
    if (chunk.done) break;
  }
  if (!result) throw new Error("检测连接已结束，但没有收到最终结果");
  return result;
}

function fieldList(value: string) {
  return value
    .split(",")
    .map((field) => field.trim())
    .filter(Boolean);
}

function isPrivateProviderURL(value: string) {
  try {
    const hostname = new URL(value).hostname.toLowerCase().replace(/^\[|\]$/g, "");
    if (
      hostname === "localhost" ||
      hostname.endsWith(".localhost") ||
      hostname === "::" ||
      hostname === "::1" ||
      hostname.startsWith("fc") ||
      hostname.startsWith("fd") ||
      hostname.startsWith("fe8") ||
      hostname.startsWith("fe9") ||
      hostname.startsWith("fea") ||
      hostname.startsWith("feb")
    ) {
      return true;
    }
    const parts = hostname.split(".").map(Number);
    if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part))) {
      return false;
    }
    return (
      parts[0] === 0 ||
      parts[0] === 10 ||
      parts[0] === 127 ||
      (parts[0] === 169 && parts[1] === 254) ||
      (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) ||
      (parts[0] === 192 && parts[1] === 168)
    );
  } catch {
    return false;
  }
}

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
    codex_integration:
      raw?.codex_integration ||
      (raw?.compatibility_mode ? "compatibility" : "passthrough"),
    codex_compatibility: raw?.codex_compatibility || "",
    compatibility_mode:
      raw?.compatibility_mode ||
      (raw?.protocol === "openai-chat" &&
      (raw?.request_policy?.omit_fields || []).includes("reasoning_effort")
        ? "codex-chat"
        : ""),
    tool_choice_mode: raw?.tool_choice_mode || "standard",
    reasoning_history: raw?.reasoning_history || "preserve",
    reasoning_with_tools: raw?.reasoning_with_tools || "supported",
    base_url: raw?.base_url || "",
    api_key: raw?.api_key || "",
    models: (raw?.models || []).join(", "),
    headers: Object.entries(raw?.headers || {})
      .map(([key, value]) => `${key}: ${value}`)
      .join("\n"),
    timeout: raw?.timeout || "5m",
    dynamic_models: Boolean(raw?.dynamic_models),
    allow_private_url: Boolean(raw?.allow_private_url),
    omit_fields: (raw?.request_policy?.omit_fields || []).join(", "),
  });
  const [error, setError] = useState("");
  const [detecting, setDetecting] = useState(false);
  const [detectionProgress, setDetectionProgress] = useState("");
  const [autoCreateRoutes, setAutoCreateRoutes] = useState(existing === null);
  const [showAPIKey, setShowAPIKey] = useState(false);
  const [detection, setDetection] = useState<DetectionResult | null>(
    existing === null
      ? null
      : {
          ok: true,
          label: protocolName(form.protocol),
        },
  );
  const privateURL = isPrivateProviderURL(form.base_url);

  async function detect(forceRefresh = false) {
    if (privateURL && !form.allow_private_url) {
      setError("检测到本机或局域网地址，请先确认允许 AI Router 访问该模型服务");
      setDetection({ ok: false });
      return;
    }
    setDetecting(true);
    setDetectionProgress("正在连接模型服务…");
    setError("");
    setDetection(null);
    try {
      const models = form.models
        .split(",")
        .map((value: string) => value.trim())
        .filter(Boolean);
      const result = await streamProviderDetection(
        {
          base_url: form.base_url,
          api_key: form.api_key,
          models,
          allow_private_url: form.allow_private_url,
          force_refresh: forceRefresh,
        },
        setDetectionProgress,
      );
      if (!result.ok) {
        setDetection(result);
        const attempts = (result.attempts || [])
          .map(
            (item: any) =>
              `${protocolName(item.protocol)}: ${item.status || item.error || "失败"}`,
          )
          .join("；");
        throw new Error(result.codex_compatibility?.message || `没有识别出可用协议。${attempts}`);
      }
      const firstModel = models[0];
      const generatedID = generatedProviderID(
        firstModel,
        (parse(yaml)?.providers || []).map((provider: any) => provider.id || ""),
      );
      const recommendedOmissions =
        result.codex_compatibility?.recommended_omit_fields || [];
      const recommendedCompatibilityMode =
        result.codex_compatibility?.recommended_compatibility_mode || "";
      const recommendedIntegrationMode =
        result.codex_compatibility?.recommended_integration_mode ||
        (recommendedCompatibilityMode ? "compatibility" : "passthrough");
      const selectedCompatibilityMode =
        recommendedCompatibilityMode ||
        (result.protocol === "openai-chat" &&
        form.compatibility_mode === "codex-chat"
          ? "codex-chat"
          : "");
      const previousRecommendations =
        detection?.codex_compatibility?.recommended_omit_fields || [];
      setForm((current) => ({
        ...current,
        id: current.id || generatedID || `model-${Date.now()}`,
        name: current.name.trim() || firstModel,
        protocol: result.protocol || current.protocol,
        codex_integration: recommendedIntegrationMode,
        codex_compatibility:
          result.codex_compatibility?.status || current.codex_compatibility,
        compatibility_mode: selectedCompatibilityMode,
        profile: result.profile || "generic",
        tool_choice_mode:
          result.codex_compatibility?.recommended_tool_choice_mode ||
          current.tool_choice_mode,
        reasoning_history:
          result.codex_compatibility?.recommended_reasoning_history ||
          current.reasoning_history,
        reasoning_with_tools:
          result.codex_compatibility?.recommended_reasoning_with_tools ||
          current.reasoning_with_tools,
        omit_fields: recommendedOmissions.filter(
          (field: string) =>
            !(selectedCompatibilityMode === "codex-chat" && field === "reasoning_effort"),
        ).length
          ? [
              ...new Set([
                ...fieldList(current.omit_fields).filter(
                  (field) => !previousRecommendations.includes(field),
                ),
                ...recommendedOmissions.filter(
                  (field: string) =>
                    !(selectedCompatibilityMode === "codex-chat" && field === "reasoning_effort"),
                ),
              ]),
            ].join(", ")
          : fieldList(current.omit_fields)
              .filter((field) => !previousRecommendations.includes(field))
              .join(", "),
      }));
      setDetection({
        ...result,
        label: protocolName(result.protocol || ""),
      });
    } catch (e) {
      setError((e as Error).message);
      setDetection((current) => current || { ok: false });
    } finally {
      setDetecting(false);
    }
  }

  async function save() {
    try {
      if (!detection?.ok) throw new Error("请先完成连接测试和协议识别");
      const doc = parse(yaml) || {};
      doc.providers = doc.providers || [];
      const models = form.models
        .split(",")
        .map((model: string) => model.trim())
        .filter(Boolean);
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
      const value: any = {
        ...raw,
        id: form.id,
        name: form.name.trim() || models[0],
        profile: form.profile,
        reasoning_mode: form.reasoning_mode,
        ...(form.max_output_tokens
          ? { max_output_tokens: Number(form.max_output_tokens) }
          : {}),
        protocol: form.protocol,
        codex_integration: form.codex_integration || undefined,
        codex_compatibility: form.codex_compatibility || undefined,
        compatibility_mode: form.compatibility_mode || undefined,
        tool_choice_mode: form.tool_choice_mode || undefined,
        reasoning_history: form.reasoning_history || undefined,
        reasoning_with_tools: form.reasoning_with_tools || undefined,
        base_url: form.base_url,
        api_key: form.api_key,
        timeout: form.timeout,
        dynamic_models: form.dynamic_models,
        allow_private_url: form.allow_private_url,
        ...(Object.keys(headers).length ? { headers } : {}),
        models,
      };
      const omitFields = fieldList(form.omit_fields).filter(
        (field) =>
          !(form.compatibility_mode === "codex-chat" && field === "reasoning_effort"),
      );
      if (omitFields.length) {
        value.request_policy = {
          ...(raw?.request_policy || {}),
          omit_fields: omitFields,
        };
      } else {
        delete value.request_policy;
      }
      if (!form.compatibility_mode) delete value.compatibility_mode;
      if (!form.codex_integration) delete value.codex_integration;
      if (!form.codex_compatibility) delete value.codex_compatibility;
      if (existing === null) {
        doc.providers.push(value);
        if (autoCreateRoutes) {
          doc.routes = doc.routes || [];
          doc.routes.push(
            ...generatedProviderRoutes(
              doc.routes,
              value.id,
              value.models,
            ),
          );
        }
      } else
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
        {error && <div className="notice error">{localizeValue(error)}</div>}
        <div className="onboard-fields">
          <label>
            模型服务名称
            <input
              placeholder="例如：硅基流动 1"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </label>
          <label>
            API 地址
            <input
              placeholder="https://api.example.com/v1"
              value={form.base_url}
              onChange={(e) => {
                const baseURL = e.target.value;
                setForm({
                  ...form,
                  base_url: baseURL,
                  allow_private_url: isPrivateProviderURL(baseURL)
                    ? form.allow_private_url
                    : false,
                });
                setError("");
                setDetection(null);
              }}
            />
          </label>
          <label>
            API Key
            <span className="secret-input-wrap">
              <input
                type={showAPIKey ? "text" : "password"}
                placeholder="sk-..."
                value={form.api_key}
                onChange={(e) => {
                  setForm({ ...form, api_key: e.target.value });
                  setDetection(null);
                }}
              />
              <button type="button" aria-label={showAPIKey ? "隐藏密钥" : "显示密钥"} onClick={() => setShowAPIKey(!showAPIKey)}>
                {showAPIKey ? <EyeOff size={15} /> : <Eye size={15} />}
              </button>
            </span>
            <small className="secret-storage-hint">
              {form.api_key
                ? "密钥将明文保存到本机 0600 配置和备份中"
                : "请输入模型服务的 API Key"}
            </small>
          </label>
          <label>
            模型名称
            <input
              placeholder="qwen3, qwen3-coder（多个用逗号分隔）"
              value={form.models}
              onChange={(e) => {
                setForm({ ...form, models: e.target.value });
                setDetection(null);
              }}
            />
          </label>
          {existing === null && (
            <div className="auto-route-option">
              <label className="check-label auto-route-switch">
                <input
                  type="checkbox"
                  checked={autoCreateRoutes}
                  onChange={(event) => setAutoCreateRoutes(event.target.checked)}
                />
                自动生成每个模型的全部协议路由
              </label>
              <small>
                默认创建 Claude、OpenAI Chat、OpenAI Responses 和 Gemini 路由；已存在的模型与协议组合会自动跳过。
              </small>
            </div>
          )}
          {privateURL && (
            <div className="private-url-confirmation" role="alert">
              <TriangleAlert size={17} />
              <div>
                <b>检测到本机或局域网模型服务</b>
                <span>该地址只能访问当前电脑或所在局域网，请确认这是你信任的模型服务。</span>
                <label className="check-label private-switch">
                  <input
                    type="checkbox"
                    aria-label="确认访问本机或私网模型服务"
                    checked={form.allow_private_url}
                    onChange={(e) => {
                      setForm({ ...form, allow_private_url: e.target.checked });
                      setError("");
                    }}
                  />
                  我确认允许 AI Router 访问这个地址
                </label>
              </div>
            </div>
          )}
        </div>
        <button className="detect-button" onClick={() => detect(false)} disabled={detecting}>
          <Activity size={16} />
          {detecting ? localizeValue(detectionProgress || "正在连接并识别…") : "测试连接并自动识别协议"}
        </button>
        {detecting && <div className="detection-progress" role="status"><span /><b>{localizeValue(detectionProgress)}</b></div>}
        <div className="detection-section-label">接入协议</div>
        <div
          className={`detected-protocol-field ${detection?.ok ? "identified" : ""}`}
          aria-live="polite"
        >
          <span>原生协议</span>
          <b>
            {detecting
              ? "正在自动识别…"
              : detection?.ok
                ? detection.label
                : "待自动识别"}
          </b>
          <small>由上方连接测试自动识别，无需手动选择</small>
        </div>
        {detection?.ok && (
          <div className="detection-result success">
            <Check size={18} />
            <div>
              <b>{localizeValue("API 连接与响应结构验证成功")}</b>
              <span>{localizeValue(`真实请求已通过 · ${detection.latency_ms} ms${detection.cached ? " · 使用缓存" : ""}`)}</span>
            </div>
          </div>
        )}
        {detection?.cached && (
          <button className="detection-refresh" type="button" onClick={() => detect(true)} disabled={detecting}>
            {localizeValue("强制重新检测")}
          </button>
        )}
        {detection?.codex_compatibility && detection.codex_compatibility.status !== "full" && (
          <div
            className={`detection-result compatibility-result ${detection.codex_compatibility.status}`}
          >
            <TriangleAlert size={22} />
            <div>
              <b>{localizeValue(compatibilityTitle(
                detection.codex_compatibility.status,
                detection.codex_compatibility.recommended_integration_mode,
              ))}</b>
              <span>{localizeValue(detection.codex_compatibility.message)}</span>
            </div>
          </div>
        )}
        <details className="advanced-settings">
          <summary>高级设置与识别结果</summary>
          {detection?.protocols && (
            <div className="capability-matrix">
              {Object.entries(detection.protocols).map(([protocol, report]) => (
                <div className="capability-row" key={protocol}>
                  <b>{protocolName(protocol)}</b>
                  <span>
                    {localizeValue("基础请求：")}{localizeValue(capabilityResultText(report.basic))}
                  </span>
                  {report.streaming && (
                    <span>{localizeValue("流式：")}{localizeValue(capabilityResultText(report.streaming))}</span>
                  )}
                  {report.tools && (
                    <span>{localizeValue("工具：")}{localizeValue(capabilityResultText(report.tools))}</span>
                  )}
                  {report.reasoning && (
                    <span>{localizeValue("推理：")}{localizeValue(capabilityResultText(report.reasoning))}</span>
                  )}
                  {report.tools_with_reasoning && (
                    <span>
                      {localizeValue("工具+推理：")}{localizeValue(capabilityResultText(report.tools_with_reasoning))}
                    </span>
                  )}
                  {report.tool_round_trip && <span>{localizeValue("多轮续接：")}{localizeValue(capabilityResultText(report.tool_round_trip))}</span>}
                  {report.codex_direct && <span>{localizeValue("Codex 官方直连：")}{localizeValue(capabilityResultText(report.codex_direct))}</span>}
                  {report.codex_end_to_end && (
                    <span>
                      {localizeValue("经 Router 端到端：")}{localizeValue(capabilityResultText(report.codex_end_to_end))}
                    </span>
                  )}
                  {!report.basic.ok && report.basic.error && <small title={localizeValue(report.basic.error)}>{localizeValue(report.basic.error.slice(0, 180))}</small>}
                </div>
              ))}
            </div>
          )}
          {detection?.model_reports && Object.keys(detection.model_reports).length > 1 && (
            <div className="model-probe-list">
              <b>逐模型基础验证</b>
              {Object.entries(detection.model_reports).map(([model, report]) => (
                <span key={model}>{model} · {localizeValue(capabilityText(report.basic))}</span>
              ))}
            </div>
          )}
          <div className="form-grid">
            <label>
              标识 ID
              <input
                value={form.id}
                onChange={(e) => setForm({ ...form, id: e.target.value })}
              />
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
              工具选择策略
              <select value={form.tool_choice_mode} onChange={(e) => setForm({ ...form, tool_choice_mode: e.target.value })}>
                <option value="standard">标准</option>
                <option value="required">优先 required</option>
                <option value="auto-only">仅 auto</option>
              </select>
            </label>
            <label>
              工具与推理组合
              <select value={form.reasoning_with_tools} onChange={(e) => setForm({ ...form, reasoning_with_tools: e.target.value })}>
                <option value="supported">允许组合</option>
                <option value="disabled">工具请求关闭推理</option>
              </select>
            </label>
            <label>
              超时
              <input
                value={form.timeout}
                onChange={(e) => setForm({ ...form, timeout: e.target.value })}
              />
            </label>
            <label>
              省略请求字段
              <input
                placeholder="reasoning_effort"
                value={form.omit_fields}
                onChange={(e) =>
                  setForm({ ...form, omit_fields: e.target.value })
                }
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
