import React from "react";
import { currentLocale } from "../../app/i18n";
import type { Status } from "../../types";

function number(value: number | undefined) {
  return Number(value || 0).toLocaleString(currentLocale());
}

export function formatUptime(seconds = 0, locale = currentLocale()) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (locale === "en-US") {
    if (days > 0) return `${days}d ${hours}h`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    if (minutes > 0) return `${minutes}m`;
    return `${Math.max(0, Math.floor(seconds))}s`;
  }
  if (days > 0) return `${days} 天 ${hours} 小时`;
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`;
  if (minutes > 0) return `${minutes} 分钟`;
  return `${Math.max(0, Math.floor(seconds))} 秒`;
}

export function OverviewPage({ status }: { status: Status | null }) {
  const metrics = status?.metrics;
  const requests = metrics?.requests || 0;
  const errors = metrics?.errors || 0;
  const successRate = requests
    ? Math.max(0, ((requests - errors) / requests) * 100)
    : null;
  const totalTokens = (metrics?.input_tokens || 0) + (metrics?.output_tokens || 0);
  const hasRequests = requests > 0;
  const running = status?.status === "running";

  return (
    <div className="console-page overview-page overview-classic-page">
      <header className="overview-compact-header">
        <div>
          <h1>运行概览</h1>
          <p>查看本地网关的实时请求、Token 消耗和链路性能。</p>
        </div>
        <div className={`overview-inline-status ${running ? "ok" : "bad"}`}>
          <i />
          <b>{running ? "网关运行中" : "网关已关闭"}</b>
          <span>· 已运行 {formatUptime(status?.uptime_seconds)}</span>
        </div>
      </header>

      <section className="overview-metric-grid">
        <article className="overview-metric-card">
          <span>累计请求</span>
          <strong>{number(requests)}</strong>
          <small>当前进程</small>
        </article>
        <article className="overview-metric-card">
          <span>成功率</span>
          <strong>{successRate === null ? "—" : `${successRate.toFixed(1)}%`}</strong>
          <small>{number(errors)} 次错误</small>
        </article>
        <article className="overview-metric-card">
          <span>Token 总消耗</span>
          <strong>{number(totalTokens)}</strong>
          <small>输入 + 输出</small>
        </article>
        <article className="overview-metric-card">
          <span>当前并发</span>
          <strong>{number(metrics?.in_flight)}</strong>
          <small>正在处理</small>
        </article>
      </section>

      <section className="overview-detail-grid">
        <article className="overview-detail-card">
          <header>
            <h2>Token 消耗</h2>
            <p>当前进程累计统计</p>
          </header>
          <div className="overview-detail-rows">
            <div><span>输入 Token</span><b>{number(metrics?.input_tokens)}</b></div>
            <div><span>输出 Token</span><b>{number(metrics?.output_tokens)}</b></div>
            <div><span>Token 合计</span><b>{number(totalTokens)}</b></div>
          </div>
        </article>

        <article className="overview-detail-card">
          <header>
            <h2>链路性能</h2>
            <p>当前进程请求延迟分位值</p>
          </header>
          <div className="overview-detail-rows">
            <div><span>P50 延迟</span><b>{hasRequests ? `${number(metrics?.p50_latency_ms)} ms` : "—"}</b></div>
            <div><span>P95 延迟</span><b>{hasRequests ? `${number(metrics?.p95_latency_ms)} ms` : "—"}</b></div>
            <div><span>重试 / 故障切换</span><b>{number(metrics?.retries)} / {number(metrics?.fallbacks)}</b></div>
          </div>
        </article>
      </section>
    </div>
  );
}
