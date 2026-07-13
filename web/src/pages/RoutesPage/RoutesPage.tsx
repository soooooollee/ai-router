import React, { useState } from "react";
import {
  Button as AntButton,
  Space,
  Table,
  Tag,
  Tooltip,
  type TableColumnsType,
} from "antd";
import { Save } from "lucide-react";
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { TableViewTabs } from "../../components/TableViewTabs";
import { WorkflowSteps } from "../../components/WorkflowSteps";
import { protocolName } from "../../lib";
import type { Provider, RouteConfig, RouteTableRow } from "../../types";

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
