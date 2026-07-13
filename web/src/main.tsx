import React from "react";
import { createRoot } from "react-dom/client";
import { ConfigProvider } from "antd";
import { App } from "./app/App";
import "./styles/tokens.css";
import "./styles.css";
import "./actions.css";
import "./sss-theme.css";

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ConfigProvider
      theme={{
        token: {
          colorPrimary: "#1677ff",
          colorText: "#1f1f1f",
          colorTextSecondary: "#667085",
          colorBorder: "#d9d9d9",
          borderRadius: 6,
          controlHeight: 38,
          fontFamily: 'Inter, "SF Pro Text", "PingFang SC", system-ui, sans-serif',
          fontSize: 14,
        },
        components: {
          Table: {
            headerBg: "#fafafa",
            headerColor: "#262626",
            headerSplitColor: "#f0f0f0",
            borderColor: "#f0f0f0",
            rowHoverBg: "#fafcff",
            cellPaddingBlock: 14,
            cellPaddingInline: 16,
          },
          Button: { fontWeight: 500 },
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
);
