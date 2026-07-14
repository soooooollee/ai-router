import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Button, Select, Table, Tag, type TableColumnsType } from "antd";
import { RefreshCw, X } from "lucide-react";
import { api } from "../../app/api";
import type { LogRecord } from "../../types";
import { currentLocale } from "../../app/i18n";

type ChatEntry = { role: string; content: string };

function parseJSON(raw?: string): any | null {
  if (!raw) return null;
  try { return JSON.parse(raw); } catch { return null; }
}

function contentText(content: any): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return content == null ? "" : JSON.stringify(content, null, 2);
  return content.map((block) => {
    if (typeof block === "string") return block;
    if (block?.text) return block.text;
    if (block?.thinking) return `[思考]\n${block.thinking}`;
    if (block?.type === "tool_use" || block?.type === "tool_call") return `[工具调用 ${block.name || ""}]\n${JSON.stringify(block.input || block.arguments || {}, null, 2)}`;
    if (block?.type === "tool_result") return `[工具结果]\n${contentText(block.content || block.result)}`;
    if (block?.type === "image" || block?.type === "image_url") return `[图片 ${block.source?.media_type || block.image_url?.url || ""}]`;
    return JSON.stringify(block, null, 2);
  }).filter(Boolean).join("\n\n");
}

export function requestEntries(body: any): ChatEntry[] {
  if (!body) return [];
  const entries: ChatEntry[] = [];
  if (body.system) entries.push({ role: "system", content: contentText(body.system) });
  if (body.instructions) entries.push({ role: "system", content: contentText(body.instructions) });
  const source = body.messages ?? body.input ?? [];
  const messages = Array.isArray(source) ? source : [source];
  for (const message of messages) {
    if (typeof message === "string") {
      entries.push({ role: "user", content: message });
      continue;
    }
    if (message?.type === "function_call") {
      entries.push({ role: "assistant", content: `[工具调用 ${message.name || ""}]\n${message.arguments || "{}"}` });
      continue;
    }
    if (message?.type === "function_call_output") {
      entries.push({ role: "tool", content: `[工具结果]\n${contentText(message.output)}` });
      continue;
    }
    if (message?.type === "reasoning") {
      entries.push({ role: "assistant", content: `[思考]\n${contentText(message.summary)}` });
      continue;
    }
    entries.push({ role: message?.role || "user", content: contentText(message?.content ?? message?.parts ?? message) });
  }
  if (!entries.length && body.contents) {
    for (const message of body.contents) entries.push({ role: message.role || "user", content: contentText(message.parts) });
  }
  return entries.filter((entry) => entry.content);
}

export function responseEntries(body: any): ChatEntry[] {
  if (!body) return [];
  if (Array.isArray(body.events)) {
    let text = "", reasoning = "";
    const tools: string[] = [];
    for (const event of body.events) {
      if (event.type === "text.delta") text += event.delta || "";
      if (event.type === "reasoning.delta") reasoning += event.delta || "";
      if (event.type === "tool_call.start" && event.block) tools.push(`[工具调用 ${event.block.name || ""}]\n${JSON.stringify(event.block.arguments || {}, null, 2)}`);
      if (event.type === "tool_call.arguments.delta" && event.arguments) tools.push(event.arguments);
    }
    const combined = [reasoning && `[思考]\n${reasoning}`, text, ...tools].filter(Boolean).join("\n\n");
    return combined ? [{ role: "assistant", content: combined }] : [];
  }
  if (body.choices?.[0]?.message) return [{ role: "assistant", content: contentText(body.choices[0].message.content) }];
  if (Array.isArray(body.output)) {
    const content = body.output.map((item: any) => {
      if (item?.type === "reasoning") return `[思考]\n${contentText(item.summary)}`;
      if (item?.type === "message") return contentText(item.content);
      if (item?.type === "function_call") return `[工具调用 ${item.name || ""}]\n${item.arguments || "{}"}`;
      return "";
    }).filter(Boolean).join("\n\n");
    return content ? [{ role: "assistant", content }] : [];
  }
  if (body.content) return [{ role: body.role || "assistant", content: contentText(body.content) }];
  if (body.messages) return body.messages.map((message: any) => ({ role: message.role || "assistant", content: contentText(message.content) }));
  const parts = body.candidates?.[0]?.content?.parts;
  return parts ? [{ role: "assistant", content: contentText(parts) }] : [];
}

function prettyBody(raw?: string) {
  const parsed = parseJSON(raw);
  return parsed ? JSON.stringify(parsed, null, 2) : raw || "当时未记录正文。";
}

function LogDetail({ record, loading, close }: { record: LogRecord | null; loading: boolean; close: () => void }) {
  const [tab, setTab] = useState("chat");
  useEffect(() => { if (record) setTab("chat"); }, [record?.id]);
  if (!record && !loading) return null;
  const request = parseJSON(record?.request_body);
  const response = parseJSON(record?.response_body);
  const chat = [...requestEntries(request), ...responseEntries(response)];
  return (
    <div className="log-detail-backdrop" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <aside className="log-detail-panel" aria-label="日志详情" aria-modal="true" role="dialog">
        <header>
          <div><span>调用日志详情</span><h2>{record?.id || "正在加载…"}</h2></div>
          <button aria-label="关闭日志详情" onClick={close}><X size={19} /></button>
        </header>
        {loading || !record ? <div className="log-detail-loading">正在读取完整日志…</div> : <>
          <div className="log-detail-summary">
            <div><span>状态</span><b className={record.status < 400 ? "ok" : "bad"}>{record.status} {record.error_code || "成功"}</b></div>
            <div><span>模型</span><b>{record.requested_model || "—"}</b></div>
            <div><span>路由 / 上游</span><b>{record.route_id || "—"} / {record.provider_id || record.attempts?.[0]?.provider_id || "—"}</b></div>
            <div><span>耗时 / Token</span><b>{record.duration_ms} ms / {record.usage?.total_tokens || 0}</b></div>
          </div>
          <nav className="log-detail-tabs">
            {[["chat", "聊天内容"], ["request", "请求正文"], ["response", "响应正文"], ["execution", "执行详情"]].map(([id, label]) => <button className={tab === id ? "active" : ""} key={id} onClick={() => setTab(id)}>{label}</button>)}
          </nav>
          <div className="log-detail-content">
            {tab === "chat" && <div className="chat-transcript">
              {!record.request_body && <div className="log-body-empty">这条历史日志生成时尚未开启正文记录，无法恢复聊天内容。</div>}
              {record.request_body && !chat.length && <div className="log-body-empty">正文存在，但没有识别到标准聊天消息。可在“请求正文”和“响应正文”中查看原始内容。</div>}
              {chat.map((entry, index) => <article className={`chat-entry ${entry.role}`} key={`${entry.role}-${index}`}><div>{entry.role === "assistant" ? "助手" : entry.role === "system" ? "系统" : entry.role === "tool" ? "工具" : "用户"}</div><pre>{entry.content}</pre></article>)}
            </div>}
            {tab === "request" && <pre className="log-raw-body">{prettyBody(record.request_body)}</pre>}
            {tab === "response" && <pre className="log-raw-body">{prettyBody(record.response_body)}</pre>}
            {tab === "execution" && <div className="log-execution">
              <section><h3>上游尝试</h3>{record.attempts?.length ? record.attempts.map((attempt) => <div className="log-attempt" key={attempt.number}><b>第 {attempt.number} 次 · {attempt.provider_id}</b><span>{attempt.model}</span><em>{attempt.status || attempt.error || "失败"} · {attempt.duration_ms} ms</em></div>) : <p>没有上游尝试记录。</p>}</section>
              <section><h3>协议诊断</h3>{record.diagnostics?.length ? record.diagnostics.map((item, index) => <div className="log-diagnostic" key={`${item.code}-${index}`}><b>{item.code}</b><span>{item.message}</span><em>{item.action}</em></div>) : <p>没有协议诊断信息。</p>}</section>
            </div>}
          </div>
        </>}
      </aside>
    </div>
  );
}

export function LogsPage() {
  const [logs, setLogs] = useState<LogRecord[]>([]);
  const [keyword, setKeyword] = useState("");
  const [status, setStatus] = useState("all");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [detail, setDetail] = useState<LogRecord | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const load = useCallback(async () => { setLoading(true); setError(""); try { const result = await api("/api/logs?limit=200"); setLogs(Array.isArray(result) ? result : result.logs || []); } catch (loadError) { setLogs([]); setError((loadError as Error).message); } finally { setLoading(false); } }, []);
  useEffect(() => { load(); }, [load]);
  const openDetail = async (item: LogRecord) => { setDetail(item); setDetailLoading(true); try { setDetail(await api(`/api/logs/${encodeURIComponent(item.id)}`) as LogRecord); } catch (loadError) { setError((loadError as Error).message); } finally { setDetailLoading(false); } };
  const visible = useMemo(() => logs.filter((item) => { const matchesStatus = status === "all" || (status === "success" ? item.status < 400 : item.status >= 400); const haystack = `${item.id} ${item.requested_model} ${item.route_id} ${item.provider_id}`.toLowerCase(); return matchesStatus && haystack.includes(keyword.trim().toLowerCase()); }), [keyword, logs, status]);
  const columns: TableColumnsType<LogRecord> = [
    { title: "时间 / 请求 ID", key: "started_at", width: 230, render: (_, item) => <div className="log-primary-cell"><b>{new Date(item.started_at).toLocaleString(currentLocale())}</b><small>{item.id}</small></div> },
    { title: "协议", dataIndex: "client_protocol", width: 160, render: (value: string) => <Tag>{value}</Tag> },
    { title: "请求模型", key: "model", width: 210, render: (_, item) => <div className="log-primary-cell"><b>{item.requested_model || "—"}</b><small>{item.resolved_model || "—"}</small></div> },
    { title: "路由 / 上游", key: "route", width: 200, render: (_, item) => <div className="log-primary-cell"><b>{item.route_id || "—"}</b><small>{item.provider_id || item.attempts?.[0]?.provider_id || "—"}</small></div> },
    { title: "状态", key: "status", width: 110, render: (_, item) => <div className="log-primary-cell"><Tag color={item.status < 400 ? "success" : "error"}>{item.status || "—"}</Tag>{item.error_code && <small>{item.error_code}</small>}</div> },
    { title: "耗时", key: "duration", width: 150, render: (_, item) => <div className="log-primary-cell"><b>{item.duration_ms} ms</b><small>首 Token {item.first_token_ms || 0} ms</small></div> },
    { title: "Token", key: "tokens", width: 160, render: (_, item) => { const total = item.usage?.total_tokens || (item.usage?.input_tokens || 0) + (item.usage?.output_tokens || 0); return <div className="log-primary-cell"><b>{total.toLocaleString(currentLocale())}</b><small>{item.usage?.input_tokens || 0} 入 / {item.usage?.output_tokens || 0} 出</small></div>; } },
  ];
  return <div className="console-page logs-page">
    <div className="page-toolbar"><div><h1>调用日志</h1><p>点击任意一条记录，查看完整聊天内容、原始正文和上游执行过程。</p></div><Button icon={<RefreshCw size={14} />} onClick={load} loading={loading}>刷新</Button></div>
    <div className="list-filter-bar"><label><span>关键词</span><input className="ant-like-input" value={keyword} onChange={(e) => setKeyword(e.target.value)} placeholder="请求 ID、模型、路由、上游" /></label><label><span>状态</span><Select value={status} onChange={setStatus} options={[{ value: "all", label: "全部状态" }, { value: "success", label: "成功" }, { value: "error", label: "失败" }]} /></label><div className="filter-result"><span>结果</span><b>显示 {visible.length} / {logs.length}</b></div></div>
    {error && <div className="notice error">日志加载失败：{error}</div>}
    <div className="antd-table-shell log-table-wrap"><Table<LogRecord> className="airoute-data-table clickable-log-table" columns={columns} dataSource={visible} rowKey="id" loading={loading} pagination={{ pageSize: 20, showSizeChanger: false, hideOnSinglePage: true }} scroll={{ x: 1220 }} locale={{ emptyText: "暂无调用日志" }} onRow={(item) => ({ onClick: () => openDetail(item), onKeyDown: (event) => { if (event.key === "Enter" || event.key === " ") openDetail(item); }, tabIndex: 0 })} /></div>
    <LogDetail record={detail} loading={detailLoading} close={() => setDetail(null)} />
  </div>;
}
