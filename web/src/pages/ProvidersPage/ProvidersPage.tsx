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
import { TableViewTabs } from "../../components/TableViewTabs";
import { WorkflowSteps } from "../../components/WorkflowSteps";
import { protocolName } from "../../lib";
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
  const [notice, noticeContext] = notification.useNotification();
  const [view, setView] = useState<"all" | "connected" | "attention">("all");
  async function probe(id: string, testRequest = false) {
    setProbing(id);
    const provider = data.find((item) => item.id === id);
    try {
      const r = await api(`/api/providers/${id}/probe`, {
        method: "POST",
        body: JSON.stringify({ test_request: testRequest }),
      });
      const detail = r.ok
        ? `${r.latency_ms} ms · ${testRequest ? (r.test_ok ? "测试通过" : `测试失败（${r.test_status || "未知状态"}）`) : "连接正常"}`
        : r.error || `HTTP ${r.status || "未知状态"}`;
      setResult((x) => ({ ...x, [id]: detail }));
      if (r.ok) {
        notice.success({
          message: "连接测试通过",
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
  async function remove(id: string) {
    if (!confirm(`删除上游服务 ${id}？引用它的路由必须先移除。`)) return;
    try {
      const doc = parse(yaml) || {};
      doc.providers = (doc.providers || []).filter((p: any) => p.id !== id);
      const next = stringify(doc);
      const r = await api("/api/config", {
        method: "PUT",
        body: JSON.stringify({ yaml: next, expected_hash: hash }),
      });
      changed(next, r.hash);
    } catch (e) {
      alert((e as Error).message);
    }
  }
  const visibleProviders = data.filter((provider) => {
    if (view === "connected") return provider.api_key_set;
    if (view === "attention")
      return !provider.api_key_set || provider.health?.ok === false;
    return true;
  });
  const columns: TableColumnsType<Provider> = [
    {
      title: "模型服务",
      key: "service",
      width: 250,
      render: (_, provider) => (
        <div className="provider-identity table-provider-identity">
          <h3>{provider.name || provider.id}</h3>
          <Tag>{protocolName(provider.protocol)}</Tag>
        </div>
      ),
    },
    {
      title: "API 地址",
      dataIndex: "base_url",
      key: "base_url",
      width: 250,
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
      width: 190,
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
      title: "密钥",
      key: "status",
      width: 110,
      align: "center",
      className: "provider-status-column",
      render: (_, provider) => (
        <Tooltip
          title={
            provider.key_storage === "environment"
              ? `由环境变量 ${provider.key_reference} 提供${provider.health?.ok ? ` · ${provider.health.latency_ms || 0} ms` : ""}`
              : provider.key_storage === "plaintext"
                ? "密钥以明文保存在本地 0600 配置及备份中"
                : "尚未设置密钥"
          }
        >
          <Tag
            color={
              provider.key_storage === "environment"
                ? "success"
                : provider.api_key_set
                  ? "warning"
                  : "error"
            }
          >
            {provider.key_storage === "environment"
              ? "环境变量"
              : provider.api_key_set
                ? "本地明文"
                : "缺少密钥"}
          </Tag>
        </Tooltip>
      ),
    },
    {
      title: "操作",
      key: "actions",
      width: 174,
      align: "right",
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
          <AntButton size="small" danger onClick={() => remove(provider.id)}>
            删除
          </AntButton>
        </Space>
      ),
    },
  ];
  return (
    <>
      {noticeContext}
      <WorkflowSteps active={1} />
      <section className="data-panel">
        <div className="toolbar">
          <div>
            <b>模型列表</b>
            <p>填写 API 地址、密钥和模型名；测试成功后自动识别协议并录入。</p>
          </div>
          <button className="primary" onClick={() => setEditing(null)}>
            + 接入模型
          </button>
        </div>
        <TableViewTabs
          value={view}
          onChange={setView}
          items={[
            { value: "all", label: "全部" },
            { value: "connected", label: "已连接" },
            { value: "attention", label: "需处理" },
          ]}
        />
        <div className="antd-table-shell">
          <Table<Provider>
            className="airoute-data-table provider-data-table"
            columns={columns}
            dataSource={visibleProviders}
            rowKey="id"
            pagination={false}
            tableLayout="fixed"
            scroll={{ x: 950 }}
            locale={{ emptyText: "还没有接入模型" }}
            footer={() =>
              view === "all"
                ? `共 ${data.length} 个模型服务`
                : `筛选结果 ${visibleProviders.length} 个 · 全部 ${data.length} 个`
            }
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
    </>
  );
}
