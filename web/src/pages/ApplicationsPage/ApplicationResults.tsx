import React from "react";
import { Check, CircleAlert, RotateCcw, Trash2 } from "lucide-react";
import type { ApplicationBackup, ApplicationVerifyResult } from "../../types";
import { currentLocale, localizeValue } from "../../app/i18n";

export function ApplicationResults({
  verifyResult,
  busy,
  verifyCLI,
  canRollback,
  backups,
  rollback,
  deleteBackup,
}: {
  verifyResult: ApplicationVerifyResult | null;
  busy: string;
  verifyCLI?: () => void;
  canRollback: boolean;
  backups: ApplicationBackup[];
  rollback: (backup: ApplicationBackup) => void;
  deleteBackup: (backup: ApplicationBackup) => void;
}) {
  return (
    <>
      {verifyResult && (
        <div className="application-verify-result">
          <div className="application-verify-heading">
            <b>验证结果</b>
            {verifyCLI && (
              <button disabled={Boolean(busy)} onClick={verifyCLI}>
                {busy === "cli" ? "正在执行…" : "运行 Claude Code 完整验证"}
              </button>
            )}
          </div>
          {verifyResult.stages.map((stage) => (
            <div className="application-verify-stage" key={stage.id}>
              {stage.ok ? <Check size={15} /> : <CircleAlert size={15} />}
              <div>
                <b>{localizeValue(stage.label)}</b>
                <span>{localizeValue(stage.message)}</span>
                {stage.detail && <small>{localizeValue(stage.detail)}</small>}
              </div>
              <em>{stage.latency_ms || 0} ms</em>
            </div>
          ))}
        </div>
      )}

      {canRollback && (
        <div className="application-backups">
          <div className="application-verify-heading">
            <div>
              <b>配置备份</b>
              <span>最近的自动备份可随时恢复</span>
            </div>
            <span>{backups.length} 份</span>
          </div>
          {backups.slice(0, 5).map((backup) => (
            <div className="application-backup-row" key={backup.name}>
              <div>
                <b>{backup.name}</b>
                <span>
                  {new Date(backup.modified_at).toLocaleString(currentLocale())}
                  {backup.contains_sensitive_config ? " · 可能含本地密钥" : ""}
                </span>
              </div>
              <div className="application-backup-actions">
                <button disabled={Boolean(busy)} onClick={() => rollback(backup)}>
                  <RotateCcw size={14} />
                  {busy === backup.name ? "恢复中…" : "恢复"}
                </button>
                <button
                  className="danger"
                  disabled={Boolean(busy)}
                  onClick={() => deleteBackup(backup)}
                >
                  <Trash2 size={14} />
                  删除
                </button>
              </div>
            </div>
          ))}
          {!backups.length && <small>写入配置后会在这里显示备份。</small>}
        </div>
      )}
    </>
  );
}
