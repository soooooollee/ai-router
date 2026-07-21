import React, { useState } from "react";
import {
  Button as AntButton,
  Space,
  Table,
  Tag,
  Tooltip,
  notification,
  type TableColumnsType,
} from "antd";
import { Save } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import {
  generatedRouteID,
  protocolName,
  providerModelLabel,
  routeIdentifier,
  routeIDTimestamp,
} from "../../lib";
import type { Provider, RouteConfig } from "../../types";

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

export function RoutesPage({
  data,
  providers,
  gateway,
  yaml,
  hash,
  changed,
}: {
  data: RouteConfig[];
  providers: Provider[];
  gateway: string;
  yaml: string;
  hash: string;
  changed: (y: string, h: string) => void;
}) {
  const [editing, setEditing] = useState<string | null | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<RouteConfig | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [notice, noticeContext] = notification.useNotification();
  const targetLabel = (
    providerID: string,
    model: string,
  ) => {
    const provider = providers.find((item) => item.id === providerID);
    return providerModelLabel(
      provider?.name,
      providerID,
      model,
    );
  };
  async function remove() {
    if (!pendingDelete) return;
    setDeleting(true);
    const doc = parse(yaml) || {};
    doc.routes = (doc.routes || []).filter(
      (route: any) => route.id !== pendingDelete.id,
    );
    const next = stringify(doc);
    try {
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      changed(next, r.hash);
      setPendingDelete(null);
    } catch (e) {
      notice.error({
        message: "删除路由失败",
        description: (e as Error).message,
        placement: "bottomRight",
      });
    } finally {
      setDeleting(false);
    }
  }
  const columns: TableColumnsType<RouteConfig> = [
    {
      title: "客户端模型名",
      key: "name",
      width: 190,
      render: (_, route) => (
        <div className="table-route-name">
          <b>{route.match.model || "所有模型"}</b>
        </div>
      ),
    },
    {
      title: "转发到上游模型",
      key: "target",
      width: 280,
      ellipsis: true,
      render: (_, route) => (
        <Tooltip
          title={route.targets
            .map((target) => targetLabel(
              target.provider,
              target.model,
            ))
            .join("\n")}
          placement="topLeft"
        >
          <div className="table-route-target">
            {route.targets.map((target, index) => (
              <code key={`${target.provider}-${target.model}-${index}`}>
                {targetLabel(
                  target.provider,
                  target.model,
                )}
              </code>
            ))}
          </div>
        </Tooltip>
      ),
    },
    {
      title: "客户端协议",
      key: "protocol",
      width: 160,
      render: (_, route) => (
        <Tag>{route.match.protocol ? protocolName(route.match.protocol) : "所有兼容协议"}</Tag>
      ),
    },
    {
      title: "调用地址",
      key: "address",
      width: 310,
      ellipsis: true,
      className: "route-address-column",
      render: (_, route) => {
        const address = route.match.protocol
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
      width: 140,
      fixed: "right",
      render: (_, route) =>
        <Space size={6} wrap={false}>
            <AntButton size="small" onClick={() => setEditing(route.id)}>
              编辑
            </AntButton>
            <AntButton size="small" danger onClick={() => setPendingDelete(route)}>
              删除
            </AntButton>
          </Space>,
    },
  ];
  return (
    <>
      {noticeContext}
      <section className="data-panel">
        <div className="toolbar">
          <div>
            <h2>路由列表</h2>
            <p>客户端使用一个简单模型名发起请求，AI Router 再将请求转发到指定的上游模型。</p>
          </div>
          <button className="primary" onClick={() => setEditing(null)}>
            + 添加路由
          </button>
        </div>
        <div className="antd-table-shell">
          <Table<RouteConfig>
            className="airoute-data-table route-data-table"
            columns={columns}
            dataSource={data}
            rowKey="id"
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 1080 }}
            locale={{ emptyText: "还没有配置路由" }}
            footer={() => `共 ${data.length} 条模型路由`}
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
      <ConfirmDialog
        open={Boolean(pendingDelete)}
        title="删除路由？"
        description={<>将删除客户端模型路由 <b>{pendingDelete?.match.model || pendingDelete?.id}</b>，使用该模型名的请求将不再匹配这条规则。</>}
        confirmLabel="删除路由"
        danger
        busy={deleting}
        onCancel={() => setPendingDelete(null)}
        onConfirm={remove}
      />
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
  const initialProvider = providers[0];
  const initialUpstreamModel = initialProvider?.models?.[0] || "";
  const initialAlias = routeIdentifier(initialUpstreamModel);
	const [generatedAt] = useState(() => routeIDTimestamp());
	const initialProtocol = raw?.match?.protocol || "anthropic-messages";
  const [form, setForm] = useState({
	id:
		raw?.id || generatedRouteID(initialAlias, initialProtocol, generatedAt),
    model: raw?.match?.model || initialAlias,
	protocol: initialProtocol,
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
      `${initialProvider?.id || ""}:${initialUpstreamModel}`,
  });
  const [modelEdited, setModelEdited] = useState(false);
  const [idEdited, setIDEdited] = useState(false);
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
      const targets: Array<{ provider: string; model: string }> = form.targets.split(",").map((x: string) => {
        const [provider, ...model] = x.trim().split(":");
        return { provider, model: model.join(":") };
      });
      if (targets.some((target) => !target.provider || !target.model)) {
        throw new Error("请选择一个有效的上游模型。");
      }
      const modelAlias = form.model.trim() || routeIdentifier(targets[0].model);
	  const routeID =
		  form.id.trim() ||
		  generatedRouteID(modelAlias, form.protocol, routeIDTimestamp());
      const value = {
        ...raw,
        id: routeID,
        priority: raw?.priority ?? 100,
        match: { ...match, model: modelAlias },
        targets,
      };
      if (
        doc.routes.some(
          (route: any) => route.id === routeID && route.id !== existing,
        )
      ) {
        throw new Error(`路由 ID “${routeID}” 已存在，请修改客户端模型名或高级设置中的路由 ID。`);
      }
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
      const message = (e as Error).message;
      setError(
        /config schema:|jsonschema:/i.test(message)
          ? "路由配置未通过校验，请检查客户端模型名、路由 ID 和目标上游模型。"
          : message,
      );
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
        <div className="route-help-box">
          <b>路由就是一条模型映射规则</b>
          <span>客户端模型名（例如 qwen3） → 实际上游服务与模型</span>
        </div>
        <div className="route-wizard-fields">
          <label>
            目标上游模型
            <select
              aria-label="选择接入模型"
              value={form.targets.split(",")[0].trim()}
              onChange={(e) => {
                const selected = e.target.value;
                const model = selected.split(":").slice(1).join(":");
                const alias = routeIdentifier(model);
                const nextModel =
                  existing === null && !modelEdited ? alias : form.model;
                setForm({
                  ...form,
                  targets: selected,
                  model: nextModel,
                  id:
                    existing === null && !idEdited
                      ? generatedRouteID(nextModel, form.protocol, generatedAt)
                      : form.id,
                });
              }}
            >
              {providers.flatMap((provider) =>
                provider.models.map((model) => (
                  <option
                    key={`${provider.id}:${model}`}
                    value={`${provider.id}:${model}`}
                  >
                    {providerModelLabel(
                      provider.name,
                      provider.id,
                      model,
                    )}
                  </option>
                )),
              )}
            </select>
          </label>
          <label>
            客户端模型名（别名）
            <input
              aria-label="客户端模型名"
              placeholder="例如 coding-model"
              value={form.model}
              onChange={(e) => {
                const model = e.target.value;
                setModelEdited(true);
                setForm({
                  ...form,
                  model,
                  id:
                    existing === null && !idEdited
                      ? generatedRouteID(model, form.protocol, generatedAt)
                      : form.id,
                });
              }}
            />
          </label>
          <label>
            客户端调用协议
            <select
              aria-label="客户端调用协议"
              value={form.protocol}
			  onChange={(e) => {
				  const protocol = e.target.value;
				  setForm({
					  ...form,
					  protocol,
					  id:
						  existing === null && !idEdited
							  ? generatedRouteID(form.model, protocol, generatedAt)
							  : form.id,
				  });
			  }}
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
            <small>客户端调用地址</small>
            <code>{routeAddress(gateway, form.protocol, form.model)}</code>
          </div>
          <span>
            请求中的 model 填写 <b>{form.model || "模型名"}</b>，系统会自动转发到上方选择的模型。
          </span>
        </div>
        <details className="advanced-settings">
          <summary>高级匹配设置</summary>
          <div className="form-grid">
            <label>
              路由 ID
              <input
                value={form.id}
                onChange={(e) => {
                  setIDEdited(true);
                  setForm({ ...form, id: e.target.value });
                }}
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
