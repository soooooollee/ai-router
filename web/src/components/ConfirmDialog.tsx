import React from "react";
import { AlertTriangle } from "lucide-react";

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = "确认",
  danger = false,
  busy = false,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  description: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  if (!open) return null;
  return (
    <div className="modal confirm-dialog-backdrop" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget && !busy) onCancel();
    }}>
      <section className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="confirm-dialog-title">
        <div className={`confirm-dialog-icon ${danger ? "danger" : "warning"}`}>
          <AlertTriangle size={19} />
        </div>
        <div className="confirm-dialog-copy">
          <h2 id="confirm-dialog-title">{title}</h2>
          <div>{description}</div>
        </div>
        <div className="confirm-dialog-actions">
          <button className="secondary" type="button" onClick={onCancel} disabled={busy}>取消</button>
          <button className={danger ? "danger-action" : "primary"} type="button" onClick={onConfirm} disabled={busy}>
            {busy ? "处理中…" : confirmLabel}
          </button>
        </div>
      </section>
    </div>
  );
}
