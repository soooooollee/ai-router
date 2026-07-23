import React, { useCallback, useEffect, useState } from "react";
import {
  Button as AntButton,
  Space,
  Table,
  Tag,
  Tooltip,
  notification,
  type TableColumnsType,
} from "antd";
import { Check, Copy, DatabaseBackup, KeyRound, Plus, RotateCw, ShieldAlert, X } from "lucide-react";
import { api } from "../../app/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import type {
  ClientCredential,
  ClientListResponse,
  ClientPolicy,
  GatewayClient,
} from "../../types";

const protocols = [
  ["openai-responses", "OpenAI Responses"],
  ["openai-chat", "OpenAI Chat"],
  ["anthropic-messages", "Anthropic Messages"],
  ["gemini-generate-content", "Gemini"],
] as const;

const applications = [
  ["codex", "Codex CLI / ChatGPT App"],
  ["claude-code", "Claude Code"],
  ["claude-app", "Claude App"],
  ["mimo-code", "MiMo Code"],
] as const;

const credentialStatusLabel: Record<ClientCredential["status"], string> = {
  active: "启用",
  disabled: "停用",
  expired: "已过期",
  revoked: "已撤销",
};

type ClientDraft = {
  name: string;
  description: string;
  expiresAt: string;
  models: string;
  cidrs: string;
  protocols: string[];
  rpm: string;
  burst: string;
  concurrent: string;
  dailyRequests: string;
  dailyInput: string;
  dailyOutput: string;
  maxOutput: string;
  confirmUnlimited: boolean;
};

const emptyDraft: ClientDraft = {
  name: "",
  description: "",
  expiresAt: "",
  models: "",
  cidrs: "",
  protocols: [],
  rpm: "",
  burst: "",
  concurrent: "",
  dailyRequests: "",
  dailyInput: "",
  dailyOutput: "",
  maxOutput: "",
  confirmUnlimited: false,
};

function defaultClientDraft(gatewayPublic: boolean): ClientDraft {
  const draft = { ...emptyDraft };
  if (gatewayPublic) {
    const expires = new Date(Date.now() + 90 * 24 * 60 * 60 * 1000);
    draft.expiresAt = new Date(expires.getTime() - expires.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
  }
  return draft;
}

type SecretResult = {
  secret: string;
  clientName: string;
  credential: ClientCredential;
  applications?: { id: string; ok: boolean; stage: string; error?: string }[];
};

type RotationState = {
  credential: ClientCredential;
  clientName: string;
  expiresAt: string;
  revokePrevious: boolean;
};

type AccessKeyRow = {
  item: GatewayClient;
  credential: ClientCredential;
};

function numberValue(value: string) {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? Math.floor(number) : 0;
}

function csv(value: string) {
  return Array.from(new Set(value.split(",").map((item) => item.trim()).filter(Boolean)));
}

function policyFromDraft(draft: ClientDraft): Omit<ClientPolicy, "id"> {
  return {
    allowed_models: csv(draft.models),
    allowed_protocols: draft.protocols,
    allowed_cidrs: csv(draft.cidrs),
    requests_per_minute: numberValue(draft.rpm),
    burst: numberValue(draft.burst),
    max_concurrent: numberValue(draft.concurrent),
    daily_request_limit: numberValue(draft.dailyRequests),
    daily_input_tokens: numberValue(draft.dailyInput),
    daily_output_tokens: numberValue(draft.dailyOutput),
    max_output_tokens: numberValue(draft.maxOutput),
  };
}

function draftFromClient(item: GatewayClient): ClientDraft {
  return {
    ...emptyDraft,
    name: item.client.name,
    description: item.client.description || "",
    models: (item.policy.allowed_models || []).join(", "),
    cidrs: (item.policy.allowed_cidrs || []).join(", "),
    protocols: item.policy.allowed_protocols || [],
    rpm: item.policy.requests_per_minute ? String(item.policy.requests_per_minute) : "",
    burst: item.policy.burst ? String(item.policy.burst) : "",
    concurrent: item.policy.max_concurrent ? String(item.policy.max_concurrent) : "",
    dailyRequests: item.policy.daily_request_limit ? String(item.policy.daily_request_limit) : "",
    dailyInput: item.policy.daily_input_tokens ? String(item.policy.daily_input_tokens) : "",
    dailyOutput: item.policy.daily_output_tokens ? String(item.policy.daily_output_tokens) : "",
    maxOutput: item.policy.max_output_tokens ? String(item.policy.max_output_tokens) : "",
  };
}

export function ClientsPage({ models }: { models: string[] }) {
  const [data, setData] = useState<ClientListResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [draft, setDraft] = useState<ClientDraft>({ ...emptyDraft });
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<GatewayClient | null>(null);
  const [secret, setSecret] = useState<SecretResult | null>(null);
  const [rotating, setRotating] = useState<RotationState | null>(null);
  const [pendingRevoke, setPendingRevoke] = useState<{ credential: ClientCredential; item: GatewayClient } | null>(null);
	const [pendingDelete, setPendingDelete] = useState<{ credential: ClientCredential; item: GatewayClient } | null>(null);
  const [busy, setBusy] = useState("");
  const [notice, noticeContext] = notification.useNotification();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setData(await api("/api/clients"));
    } catch (error) {
      notice.error({ message: "读取客户端密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setLoading(false);
    }
  }, [notice]);

  useEffect(() => {
    void load();
  }, [load]);

  async function createClient() {
    if (!draft.name.trim()) {
      notice.warning({ message: "请输入密钥名称", placement: "bottomRight" });
      return;
    }
    setBusy("create");
    try {
      const created = await api("/api/clients", {
        method: "POST",
        body: JSON.stringify({
          name: draft.name.trim(),
          description: draft.description.trim(),
          policy: policyFromDraft(draft),
          confirm_unlimited: draft.confirmUnlimited,
          create_credential: true,
          expires_at: draft.expiresAt ? new Date(draft.expiresAt).toISOString() : undefined,
        }),
      });
      setSecret({ secret: created.secret, clientName: created.client.name, credential: created.credential });
      setCreating(false);
      setDraft(defaultClientDraft(Boolean(data?.gateway_public)));
      await load();
    } catch (error) {
      notice.error({ message: "创建客户端密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

  async function updatePolicy() {
    if (!editing) return;
    setBusy("policy");
    try {
      await api(`/api/clients/${editing.client.id}`, {
        method: "PATCH",
        body: JSON.stringify({ name: draft.name.trim(), description: draft.description.trim() }),
      });
      await api(`/api/clients/${editing.client.id}/policy`, {
        method: "PUT",
        body: JSON.stringify(policyFromDraft(draft)),
      });
      setEditing(null);
      await load();
    } catch (error) {
      notice.error({ message: "保存客户端策略失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

  async function updateCredential(credential: ClientCredential, action: "toggle" | "revoke" | "rotate", item: GatewayClient) {
    if (action === "rotate") {
      setRotating({ credential, clientName: item.client.name, expiresAt: "", revokePrevious: false });
      return;
    }
    if (action === "revoke") {
      setPendingRevoke({ credential, item });
      return;
    }
    setBusy(credential.id + action);
    try {
      await api(`/api/credentials/${credential.id}`, {
        method: "PATCH",
        body: JSON.stringify({ status: credential.status === "active" ? "disabled" : "active" }),
      });
      await load();
    } catch (error) {
      notice.error({ message: "修改密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

  async function revokeCredential() {
    if (!pendingRevoke) return;
    setBusy("revoke");
    try {
      await api(`/api/credentials/${pendingRevoke.credential.id}/revoke`, { method: "POST", body: "{}" });
      setPendingRevoke(null);
      await load();
    } catch (error) {
      notice.error({ message: "修改密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

	async function deleteCredential() {
		if (!pendingDelete) return;
		setBusy("delete");
		try {
			await api(`/api/credentials/${pendingDelete.credential.id}`, { method: "DELETE" });
			setPendingDelete(null);
			await load();
		} catch (error) {
			notice.error({ message: "删除密钥失败", description: (error as Error).message, placement: "bottomRight" });
		} finally {
			setBusy("");
		}
	}

  async function rotateCredential() {
    if (!rotating) return;
    setBusy("rotation");
    try {
      const expiresAt = rotating.expiresAt ? new Date(rotating.expiresAt).toISOString() : undefined;
      const result = await api(`/api/credentials/${rotating.credential.id}/rotate`, {
        method: "POST",
        body: JSON.stringify({ expires_at: expiresAt, revoke_previous: rotating.revokePrevious }),
      });
      setSecret({ secret: result.secret, clientName: rotating.clientName, credential: result.credential, applications: result.applications });
      setRotating(null);
      await load();
    } catch (error) {
      notice.error({ message: "轮换密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

  async function migrateLegacy(id: string) {
    setBusy("legacy:" + id);
    try {
      await api("/api/clients/migrate-legacy", { method: "POST", body: JSON.stringify({ key_id: id, name: id }) });
      await load();
      notice.success({ message: "旧密钥已迁移", description: `${id} 已转为托管客户端密钥`, placement: "bottomRight" });
    } catch (error) {
      notice.error({ message: "迁移旧密钥失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

  async function closeSecret() {
    const current = secret;
    setSecret(null);
    if (!current?.credential.id) return;
    try {
      await api(`/api/credentials/${current.credential.id}/secret-acknowledged`, { method: "POST", body: "{}" });
    } catch {
      // Closing still destroys the one-time value locally. A failed audit write
      // must not keep sensitive material visible in the browser.
    }
  }

  async function backupClientState() {
    setBusy("backup");
    try {
      const result = await api("/api/client-state/backups", { method: "POST", body: "{}" });
      notice.success({ message: "客户端状态已备份", description: result.backup?.id || "", placement: "bottomRight" });
    } catch (error) {
      notice.error({ message: "备份客户端状态失败", description: (error as Error).message, placement: "bottomRight" });
    } finally {
      setBusy("");
    }
  }

	async function enableAuthentication(credential: ClientCredential) {
		setBusy("enable-auth:" + credential.id);
		try {
			await api("/api/clients/enable-auth", { method: "POST", body: JSON.stringify({ credential_id: credential.id }) });
			await load();
			notice.success({ message: "访问鉴权已开启", placement: "bottomRight" });
		} catch (error) {
			notice.error({ message: "启用访问鉴权失败", description: (error as Error).message, placement: "bottomRight" });
		} finally {
			setBusy("");
		}
	}

  const rows: AccessKeyRow[] = (data?.clients || []).flatMap((item) => item.credentials.map((credential) => ({ item, credential })));

  const columns: TableColumnsType<AccessKeyRow> = [
    {
      title: "密钥名称",
      key: "name",
      width: 190,
      render: (_, row) => <div className="client-name-cell"><b>{row.item.client.name}</b><span>{row.item.client.description || (row.credential.recoverable ? "托管 Key" : "仅验证 Key")}</span></div>,
    },
    {
      title: "密钥",
      key: "credential",
      width: 220,
      render: (_, row) => <Tooltip title={row.credential.prefix}><code className="table-code table-ellipsis">{row.credential.prefix}</code></Tooltip>,
    },
    {
      title: "状态",
      key: "status",
      width: 105,
      render: (_, row) => <Tag color={row.credential.status === "active" && row.item.client.status === "active" ? "success" : row.credential.status === "revoked" ? "error" : "default"}>{row.item.client.status !== "active" ? "停用" : credentialStatusLabel[row.credential.status]}</Tag>,
    },
    {
      title: "访问范围",
      key: "scope",
      width: 190,
      render: (_, row) => <span className="client-scope">{row.item.policy.allowed_models?.length ? row.item.policy.allowed_models.join(", ") : "全部模型"}<small>{row.item.policy.allowed_protocols?.length ? row.item.policy.allowed_protocols.length + " 个协议" : "全部协议"}</small></span>,
    },
    {
      title: "限制",
      key: "limits",
      width: 145,
      render: (_, row) => <span className="client-scope">{row.item.policy.requests_per_minute ? `${row.item.policy.requests_per_minute} RPM` : "不限速"}<small>{row.item.policy.max_concurrent ? `${row.item.policy.max_concurrent} 并发` : "不限并发"}</small></span>,
    },
    {
      title: "今日用量",
      key: "usage",
      width: 145,
      render: (_, row) => <span className="client-scope">{row.item.today.requests || 0} 次请求<small>{(row.item.today.input_tokens || 0) + (row.item.today.output_tokens || 0)} Token</small></span>,
    },
    {
      title: "最近使用 / 到期",
      key: "lifecycle",
      width: 175,
      render: (_, row) => <span className="client-scope">{row.credential.last_used_at ? new Date(row.credential.last_used_at).toLocaleString() : "尚未使用"}<small>{row.credential.expires_at ? `到期 ${new Date(row.credential.expires_at).toLocaleString()}` : "永不过期"}</small></span>,
    },
    {
      title: "操作",
      key: "actions",
      width: 280,
      fixed: "right",
      render: (_, row) => <Space size={6} wrap={false}>
        <AntButton size="small" onClick={() => { setEditing(row.item); setDraft(draftFromClient(row.item)); }}>权限</AntButton>
		{!data?.authentication_enabled && row.credential.recoverable && row.credential.status === "active" && <AntButton size="small" loading={busy === "enable-auth:" + row.credential.id} onClick={() => enableAuthentication(row.credential)}>启用鉴权</AntButton>}
        {row.credential.status !== "revoked" && row.credential.status !== "expired" && <AntButton size="small" loading={busy === row.credential.id + "toggle"} onClick={() => updateCredential(row.credential, "toggle", row.item)}>{row.credential.status === "active" ? "停用" : "启用"}</AntButton>}
        {row.credential.status === "active" && <AntButton size="small" icon={<RotateCw size={12} />} loading={busy === row.credential.id + "rotate"} onClick={() => updateCredential(row.credential, "rotate", row.item)}>轮换</AntButton>}
        {row.credential.status !== "revoked" && <AntButton size="small" danger loading={busy === "revoke"} onClick={() => updateCredential(row.credential, "revoke", row.item)}>撤销</AntButton>}
		{(row.credential.status === "revoked" || row.credential.status === "expired") && <AntButton size="small" danger loading={busy === "delete"} onClick={() => setPendingDelete({ credential: row.credential, item: row.item })}>删除</AntButton>}
      </Space>,
    },
  ];

  return <>
    {noticeContext}
    <section className="data-panel client-page">
      <div className="toolbar">
        <div>
          <h2>访问密钥</h2>
          <p>直接生成网关 Key；需要配置应用时，在“应用配置”中选择对应 Key。当前有 {models.length} 个可用模型。</p>
        </div>
        <div className="client-toolbar-actions">
          <button className="secondary" disabled={busy === "backup"} onClick={backupClientState}>
            <DatabaseBackup size={14} />
            {busy === "backup" ? "备份中…" : "备份状态"}
          </button>
          <button className="primary" onClick={() => { setDraft(defaultClientDraft(Boolean(data?.gateway_public))); setCreating(true); }}>
            <Plus size={14} />
            生成密钥
          </button>
        </div>
      </div>
      {!data?.authentication_enabled && <div className="client-auth-warning"><ShieldAlert size={18} /><div><b>访问鉴权尚未开启</b><span>先生成 Key，再到“应用配置”选择并写入；确认可用后即可启用鉴权。</span></div></div>}
      {data?.legacy_keys?.length ? <div className="legacy-key-panel"><div><b>旧版静态密钥</b><span>迁移后原 Key 保持不变；此类 Key 只能用于验证，不能在应用配置中重新选择。</span></div>{data.legacy_keys.map((key) => <div key={key.id}><code>{key.id}</code><AntButton size="small" loading={busy === "legacy:" + key.id} onClick={() => migrateLegacy(key.id)}>迁移</AntButton></div>)}</div> : null}
      <div className="antd-table-shell">
        <Table<AccessKeyRow>
          className="airoute-data-table client-data-table"
          columns={columns}
          dataSource={rows}
          rowKey={(row) => row.credential.id}
          loading={loading}
          pagination={false}
          tableLayout="fixed"
          scroll={{ x: 1420 }}
          locale={{ emptyText: "还没有访问密钥" }}
          footer={() => `共 ${rows.length} 个访问密钥`}
        />
      </div>
    </section>
    {creating && <ClientDialog title="生成访问密钥" draft={draft} setDraft={setDraft} busy={busy === "create"} close={() => setCreating(false)} submit={createClient} creating gatewayPublic={Boolean(data?.gateway_public)} />}
    {editing && <ClientDialog title="编辑访问权限" draft={draft} setDraft={setDraft} busy={busy === "policy"} close={() => setEditing(null)} submit={updatePolicy} />}
    {rotating && <RotationDialog state={rotating} setState={setRotating} busy={busy === "rotation"} close={() => setRotating(null)} submit={rotateCredential} />}
    {secret && <SecretDialog result={secret} authenticationEnabled={Boolean(data?.authentication_enabled)} enabled={() => load()} close={closeSecret} />}
    <ConfirmDialog open={Boolean(pendingRevoke)} title="撤销访问密钥？" description={pendingRevoke?.item.active_credentials === 1 ? <>这是 <b>{pendingRevoke.item.client.name}</b> 最后一个有效 Key。撤销后，使用它的应用将无法访问网关。</> : <>撤销后无法恢复；正在进行的请求可正常结束，新请求会立即被拒绝。</>} confirmLabel="确认撤销" danger busy={busy === "revoke"} onCancel={() => setPendingRevoke(null)} onConfirm={revokeCredential} />
	<ConfirmDialog open={Boolean(pendingDelete)} title="删除访问密钥？" description={<>将彻底删除 <b>{pendingDelete?.item.client.name}</b> 的这条已撤销或已过期密钥及其索引，此操作无法恢复。</>} confirmLabel="确认删除" danger busy={busy === "delete"} onCancel={() => setPendingDelete(null)} onConfirm={deleteCredential} />
  </>;
}

function ClientDialog({ title, draft, setDraft, busy, close, submit, creating = false, gatewayPublic = false }: { title: string; draft: ClientDraft; setDraft: React.Dispatch<React.SetStateAction<ClientDraft>>; busy: boolean; close: () => void; submit: () => void; creating?: boolean; gatewayPublic?: boolean }) {
  const update = (field: keyof ClientDraft, value: string | string[]) => setDraft((current) => ({ ...current, [field]: value }));
  const toggle = (value: string) => setDraft((current) => ({ ...current, protocols: current.protocols.includes(value) ? current.protocols.filter((item) => item !== value) : [...current.protocols, value] }));
  return <div className="modal"><section className="dialog client-dialog access-key-dialog">
    <div className="panel-title"><div><span className="step-number"><KeyRound size={15} /></span><h2>{title}</h2></div><button aria-label="关闭" onClick={close}><X size={17} /></button></div>
    <div className="client-form-grid">
      <label className="full">密钥名称<input value={draft.name} onChange={(event) => update("name", event.target.value)} placeholder="例如：我的 Codex" /></label>
      <label className="full">说明<input value={draft.description} onChange={(event) => update("description", event.target.value)} placeholder="设备、团队或自动化任务" /></label>
		{creating && <label>过期时间<input type="datetime-local" value={draft.expiresAt} onChange={(event) => update("expiresAt", event.target.value)} /></label>}
      <label className={creating ? "" : "full"}>允许模型<input value={draft.models} onChange={(event) => update("models", event.target.value)} placeholder="留空表示全部模型，多个用逗号分隔" /></label>
      <label className="full">允许来源 IP / CIDR<input value={draft.cidrs} onChange={(event) => update("cidrs", event.target.value)} placeholder="留空表示不限制，例如 10.0.0.0/8, 192.168.1.10/32" /></label>
      <fieldset className="full"><legend>允许协议</legend><div className="check-grid">{protocols.map(([value, label]) => <label key={value}><input type="checkbox" checked={draft.protocols.includes(value)} onChange={() => toggle(value)} />{label}</label>)}</div><small>不勾选表示允许全部协议</small></fieldset>
      <label>每分钟请求<input type="number" min="0" value={draft.rpm} onChange={(event) => update("rpm", event.target.value)} placeholder="0 = 不限制" /></label>
      <label>突发容量<input type="number" min="0" value={draft.burst} onChange={(event) => update("burst", event.target.value)} placeholder="默认等于 RPM" /></label>
      <label>最大并发<input type="number" min="0" value={draft.concurrent} onChange={(event) => update("concurrent", event.target.value)} placeholder="0 = 不限制" /></label>
      <label>每日请求<input type="number" min="0" value={draft.dailyRequests} onChange={(event) => update("dailyRequests", event.target.value)} placeholder="0 = 不限制" /></label>
      <label>每日输入 Token<input type="number" min="0" value={draft.dailyInput} onChange={(event) => update("dailyInput", event.target.value)} placeholder="0 = 不限制" /></label>
      <label>每日输出 Token<input type="number" min="0" value={draft.dailyOutput} onChange={(event) => update("dailyOutput", event.target.value)} placeholder="0 = 不限制" /></label>
      <label>单次最大输出 Token<input type="number" min="0" value={draft.maxOutput} onChange={(event) => update("maxOutput", event.target.value)} placeholder="0 = 不限制" /></label>
		{creating && gatewayPublic && !draft.rpm && !draft.concurrent && !draft.dailyRequests && !draft.dailyInput && !draft.dailyOutput && <label className="full client-unlimited-confirm"><input type="checkbox" checked={draft.confirmUnlimited} onChange={(event) => setDraft((current) => ({ ...current, confirmUnlimited: event.target.checked }))} />我确认该公网监听 Key 不设置速率、并发或每日配额</label>}
    </div>
		<div className="dialog-actions"><button className="secondary" onClick={close}>取消</button><button className="primary" disabled={busy} onClick={submit}>{busy ? "保存中…" : creating ? "生成 Key" : "保存权限"}</button></div>
  </section></div>;
}

function RotationDialog({ state, setState, busy, close, submit }: { state: RotationState; setState: React.Dispatch<React.SetStateAction<RotationState | null>>; busy: boolean; close: () => void; submit: () => void }) {
  const update = (values: Partial<RotationState>) => setState((current) => current ? { ...current, ...values } : current);
  return <div className="modal"><section className="dialog client-dialog access-key-dialog">
		<div className="panel-title"><div><span className="step-number"><RotateCw size={15} /></span><h2>轮换访问密钥</h2></div><button aria-label="关闭" onClick={close}><X size={17} /></button></div>
		<div className="one-time-warning"><ShieldAlert size={18} /><div><b>默认保留旧 Key</b><span>生成后可在“应用配置”中选择新 Key；确认应用切换完成后再撤销旧 Key。</span></div></div>
    <div className="client-form-grid">
      <label>过期时间<input type="datetime-local" value={state.expiresAt} onChange={(event) => update({ expiresAt: event.target.value })} /></label>
		<label className="client-unlimited-confirm"><input type="checkbox" checked={state.revokePrevious} onChange={(event) => update({ revokePrevious: event.target.checked })} />生成后立即撤销旧 Key</label>
    </div>
		<div className="dialog-actions"><button className="secondary" onClick={close}>取消</button><button className="primary" disabled={busy} onClick={submit}>{busy ? "轮换中…" : "生成新 Key"}</button></div>
  </section></div>;
}

function SecretDialog({ result, authenticationEnabled, enabled, close }: { result: SecretResult; authenticationEnabled: boolean; enabled: () => void; close: () => void }) {
  const [copied, setCopied] = useState(false);
  const [enabling, setEnabling] = useState(false);
  const [error, setError] = useState("");
  const deploymentFailed = Boolean(result.applications?.some((application) => !application.ok));
  async function enableAuthentication() {
    setEnabling(true);
    setError("");
    try {
      await api("/api/clients/enable-auth", { method: "POST", body: JSON.stringify({ credential_id: result.credential.id }) });
      enabled();
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      setEnabling(false);
    }
  }
  return <div className="modal"><section className="dialog secret-result-dialog access-key-dialog">
    <div className="panel-title"><div><span className="step-number"><Check size={15} /></span><h2>访问密钥已生成</h2></div><button aria-label="关闭" onClick={close}><X size={17} /></button></div>
		<div className="one-time-warning"><ShieldAlert size={18} /><div><b>完整 Key 只在这里显示</b><span>AI Router 已加密托管，可在“应用配置”中按名称选择；管理页面不会再次返回完整内容。</span></div></div>
    <label>密钥名称<span>{result.clientName}</span></label>
    <div className="secret-copy-row"><code>{result.secret}</code><button className="secondary" onClick={async () => { await navigator.clipboard.writeText(result.secret); setCopied(true); }}><Copy size={14} />{copied ? "已复制" : "复制"}</button></div>
    {result.applications?.length ? <div className="deployment-results">{result.applications.map((item) => <span key={item.id} className={item.ok ? "ok" : "bad"}>{item.ok ? <Check size={13} /> : <X size={13} />}{applications.find(([id]) => id === item.id)?.[1] || item.id} · {item.stage}{item.error ? ` · ${item.error}` : ""}</span>)}</div> : null}
    {deploymentFailed && !authenticationEnabled && <div className="notice error">所选应用尚未全部写入并验证，暂不能启用客户端鉴权。</div>}
    {error && <div className="notice error">{error}</div>}
    <div className="dialog-actions"><button className="secondary" onClick={close}>我已保存，关闭</button>{!authenticationEnabled && <button className="primary" disabled={enabling || deploymentFailed} onClick={enableAuthentication}>{enabling ? "正在验证并启用…" : "验证密钥并启用鉴权"}</button>}</div>
  </section></div>;
}
