import React from "react";
import ReactDOM from "react-dom/client";
import "./style.css";
import AppLayout from "./AppLayout";

const container = document.getElementById("root");
if (!container) throw new Error("Root element not found");

const root = ReactDOM.createRoot(container);

root.render(
  <React.StrictMode>
    <AppLayout />
  </React.StrictMode>
);
