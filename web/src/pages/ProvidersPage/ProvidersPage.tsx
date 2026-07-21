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
import { parse, stringify } from "yaml";
import { api } from "../../app/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { protocolName, removeProviderReferences } from "../../lib";
import { ProviderDialog } from "./ProviderDialog";
import type { Provider } from "../../types";

export function ProvidersPage({
  data,
  yaml,
  hash,
  changed,
}: {
  data: Provider[];
  yaml: string;
  hash: string;
  changed: (y: string, h: string) => void;
}) {
  const [probing, setProbing] = useState("");
  const [result, setResult] = useState<Record<string, string>>({});
  const [editing, setEditing] = useState<string | null | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<Provider | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [notice, noticeContext] = notification.useNotification();
  async function probe(id: string) {
    setProbing(id);
    const provider = data.find((item) => item.id === id);
    try {
      const r = await api(`/api/providers/${id}/probe`, {
        method: "POST",
        body: JSON.stringify({}),
      });
      const detail = r.ok
        ? `${r.latency_ms} ms · 真实请求通过${r.models_ok ? "" : " · 模型列表不可用"}`
        : r.error || `HTTP ${r.status || "未知状态"}`;
      setResult((x) => ({ ...x, [id]: detail }));
      if (r.ok) {
        const notify = r.models_ok ? notice.success : notice.warning;
        notify({
          message: r.models_ok
            ? "连接测试通过"
            : "真实请求可用，模型发现不可用",
          description: `${provider?.name || id} · ${detail}`,
          placement: "bottomRight",
          duration: 4,
        });
      } else {
        notice.error({
          message: "连接测试失败",
          description: `${provider?.name || id} · ${detail}`,
          placement: "bottomRight",
          duration: 6,
        });
      }
    } catch (e) {
      const detail = (e as Error).message;
      setResult((x) => ({ ...x, [id]: detail }));
      notice.error({
        message: "连接测试失败",
        description: `${provider?.name || id} · ${detail}`,
        placement: "bottomRight",
        duration: 6,
      });
    } finally {
      setProbing("");
    }
  }
  async function remove() {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      const doc = parse(yaml) || {};
      removeProviderReferences(doc, pendingDelete.id);
      const next = stringify(doc);
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      changed(next, r.hash);
      setPendingDelete(null);
    } catch (e) {
      notice.error({
        message: "删除模型服务失败",
        description: (e as Error).message,
        placement: "bottomRight",
      });
    } finally {
      setDeleting(false);
    }
  }
  const columns: TableColumnsType<Provider> = [
    {
      title: "模型服务",
      key: "service",
      width: 175,
      render: (_, provider) => (
        <div className="provider-identity table-provider-identity">
          <h3>{provider.name || provider.id}</h3>
        </div>
      ),
    },
    {
      title: "协议",
      key: "protocol",
      width: 245,
      render: (_, provider) => <Tag>{protocolName(provider.protocol)}</Tag>,
    },
    {
      title: "API 地址",
      dataIndex: "base_url",
      key: "base_url",
      width: 200,
      ellipsis: true,
      className: "provider-api-column",
      render: (value: string) => (
        <Tooltip title={value} placement="topLeft">
          <code className="table-code table-ellipsis">{value}</code>
        </Tooltip>
      ),
    },
    {
      title: "模型",
      key: "models",
      width: 145,
      ellipsis: true,
      className: "provider-model-column",
      render: (_, provider) => (
        <Tooltip title={provider.models.join(", ")} placement="topLeft">
          <div className="table-model-cell">
            <Tag>{provider.models[0] || "—"}</Tag>
            {provider.models.length > 1 && (
              <span>+{provider.models.length - 1}</span>
            )}
          </div>
        </Tooltip>
      ),
    },
    {
      title: "API Key",
      key: "status",
      width: 160,
      className: "provider-status-column",
      render: (_, provider) => (
        <Tooltip title={provider.api_key || "尚未设置密钥"} placement="topLeft">
          <code className="table-code table-ellipsis provider-key-value">
            {provider.api_key || "未设置"}
          </code>
        </Tooltip>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 190,
      fixed: "right",
      render: (_, provider) => (
        <Space size={6} wrap={false}>
          <AntButton
            size="small"
            loading={probing === provider.id}
            aria-label={result[provider.id] || "测试"}
            onClick={() => probe(provider.id)}
          >
            测试
          </AntButton>
          <AntButton size="small" onClick={() => setEditing(provider.id)}>
            编辑
          </AntButton>
          <AntButton size="small" danger onClick={() => setPendingDelete(provider)}>
            删除
          </AntButton>
        </Space>
      ),
    },
  ];
  return (
    <>
      {noticeContext}
      <section className="data-panel">
        <div className="toolbar">
          <div>
            <h2>模型列表</h2>
            <p>填写 API 地址、密钥和模型名；测试成功后自动识别协议并录入。</p>
          </div>
          <button className="primary" onClick={() => setEditing(null)}>
            + 接入模型
          </button>
        </div>
        <div className="antd-table-shell">
          <Table<Provider>
            className="airoute-data-table provider-data-table"
            columns={columns}
            dataSource={data}
            rowKey="id"
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 1010 }}
            locale={{ emptyText: "还没有接入模型" }}
            footer={() => `共 ${data.length} 个模型服务`}
          />
        </div>
      </section>
      {editing !== undefined && (
        <ProviderDialog
          yaml={yaml}
          hash={hash}
          existing={editing}
          close={() => setEditing(undefined)}
          saved={changed}
        />
      )}
      <ConfirmDialog
        open={Boolean(pendingDelete)}
        title="删除模型服务？"
        description={<>将删除 <b>{pendingDelete?.name || pendingDelete?.id}</b>，并清理引用它的路由目标；失去全部目标的路由会一并删除。</>}
        confirmLabel="删除模型服务"
        danger
        busy={deleting}
        onCancel={() => setPendingDelete(null)}
        onConfirm={remove}
      />
    </>
  );
}
