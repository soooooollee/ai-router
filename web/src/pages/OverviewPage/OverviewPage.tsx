import React from "react";
import type { Status } from "../../types";
import { currentLocale } from "../../app/i18n";

function number(value: number | undefined) {
  return Number(value || 0).toLocaleString(currentLocale());
}

export function OverviewPage({ status }: { status: Status | null }) {
  const metrics = status?.metrics;
  const requests = metrics?.requests || 0;
  const errors = metrics?.errors || 0;
  const successRate = requests ? Math.max(0, ((requests - errors) / requests) * 100) : 100;
  const totalTokens = (metrics?.input_tokens || 0) + (metrics?.output_tokens || 0);
  return (
    <div className="console-page overview-page">
      <div className="page-toolbar">
        <div><h1>运行概览</h1><p>查看本地网关的实时请求、Token 消耗和链路性能。</p></div>
        <span className={`status-pill ${status?.status === "running" ? "ok" : "bad"}`}>
          {status?.status === "running" ? "网关运行中" : "网关已关闭"}
        </span>
      </div>
      <div className="metric-grid">
        <article><span>累计请求</span><strong>{number(requests)}</strong><small>当前进程</small></article>
        <article><span>成功率</span><strong>{successRate.toFixed(1)}%</strong><small>{number(errors)} 次错误</small></article>
        <article><span>Token 总消耗</span><strong>{number(totalTokens)}</strong><small>输入 + 输出</small></article>
        <article><span>当前并发</span><strong>{number(metrics?.in_flight)}</strong><small>正在处理</small></article>
      </div>
      <div className="overview-grid">
        <section className="plain-panel">
          <div className="panel-heading"><div><h2>Token 消耗</h2><p>当前进程累计统计</p></div></div>
          <div className="stat-rows">
            <div><span>输入 Token</span><b>{number(metrics?.input_tokens)}</b></div>
            <div><span>输出 Token</span><b>{number(metrics?.output_tokens)}</b></div>
            <div><span>Token 合计</span><b>{number(totalTokens)}</b></div>
          </div>
        </section>
        <section className="plain-panel">
          <div className="panel-heading"><div><h2>链路性能</h2><p>请求延迟分位值</p></div></div>
          <div className="stat-rows">
            <div><span>P50 延迟</span><b>{number(metrics?.p50_latency_ms)} ms</b></div>
            <div><span>P95 延迟</span><b>{number(metrics?.p95_latency_ms)} ms</b></div>
            <div><span>重试 / 故障切换</span><b>{number(metrics?.retries)} / {number(metrics?.fallbacks)}</b></div>
          </div>
        </section>
      </div>
    </div>
  );
}
