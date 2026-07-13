import React from "react";

export function WorkflowSteps({ active }: { active: 1 | 2 | 3 }) {
  return (
    <div className="workflow-steps" aria-label="配置流程">
      {["模型接入", "路由配置", "应用配置"].map((label, index) => (
        <React.Fragment key={label}>
          <div
            className={
              active === index + 1 ? "active" : active > index + 1 ? "done" : ""
            }
          >
            <span>{index + 1}</span>
            <b>{label}</b>
          </div>
          {index < 2 && <i />}
        </React.Fragment>
      ))}
    </div>
  );
}
