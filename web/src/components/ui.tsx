import type { ReactNode } from "react";
import { Inbox } from "lucide-react";
import type { Agent, TaskStatus } from "../lib/types";
import { agentColor } from "../lib/format";

/** An agent's identity chip: coloured initial, monospace name, presence dot. */
export function AgentTag({
  name,
  status,
  tool,
  size = "base",
}: {
  name?: string;
  status?: Agent["status"];
  tool?: string;
  size?: "sm" | "base";
}) {
  if (!name) return <span className="dim" style={{ fontSize: 11.5 }}>unassigned</span>;
  const color = agentColor(name);
  return (
    <span className="agent">
      <span
        className={`agent-mark ${size === "sm" ? "sm" : ""}`}
        style={{ background: color }}
        aria-hidden
      >
        {name.slice(0, 1)}
      </span>
      <span className="agent-name" style={{ color }}>
        {name}
      </span>
      {status && (
        <span
          className={`status-dot ${status === "active" ? "pulse" : ""}`}
          style={{
            background:
              status === "active"
                ? "var(--good)"
                : status === "idle"
                  ? "var(--warn)"
                  : "var(--border-strong)",
          }}
          title={status}
        />
      )}
      {tool && <span className="agent-tool">{tool}</span>}
    </span>
  );
}

const STATUS_TONE: Record<TaskStatus, string> = {
  todo: "",
  in_progress: "accent",
  blocked: "crit",
  review: "warn",
  done: "good",
  cancelled: "",
};

export function StatusTag({ status }: { status: TaskStatus }) {
  return <span className={`tag ${STATUS_TONE[status]}`}>{status.replace("_", " ")}</span>;
}

export function Panel({
  title,
  hint,
  action,
  children,
  className = "",
  flush = false,
}: {
  title?: string;
  hint?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
  flush?: boolean;
}) {
  return (
    <section className={`panel ${className}`}>
      {(title || action) && (
        <div className="panel-head">
          {title && <h2>{title}</h2>}
          {hint && <span className="hint">{hint}</span>}
          {action && <span style={{ marginLeft: "auto" }}>{action}</span>}
        </div>
      )}
      <div className={`panel-body ${flush ? "flush" : ""}`}>{children}</div>
    </section>
  );
}

export function Kpi({
  label,
  value,
  sub,
  icon,
  onClick,
}: {
  label: string;
  value: ReactNode;
  sub?: string;
  icon?: ReactNode;
  onClick?: () => void;
}) {
  return (
    <div
      className={`kpi ${onClick ? "link" : ""}`}
      onClick={onClick}
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={(e) => {
        if (onClick && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onClick();
        }
      }}
    >
      <div className="label">
        {icon}
        {label}
      </div>
      <div className="val tnum">{value}</div>
      {sub && <div className="sub">{sub}</div>}
    </div>
  );
}

/** Empty copy is deliberately concrete — it says what to do, not just "no data". */
export function Empty({
  title,
  desc,
  icon,
  action,
}: {
  title: string;
  desc?: string;
  icon?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      {icon ?? <Inbox size={28} strokeWidth={1.5} />}
      <span className="empty-title">{title}</span>
      {desc && <span className="empty-desc">{desc}</span>}
      {action && <div style={{ marginTop: 6 }}>{action}</div>}
    </div>
  );
}

export function Skeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="skel-stack" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          className="skel"
          style={{ width: `${88 - ((i * 17) % 45)}%` }}
        />
      ))}
    </div>
  );
}

export function PageHead({
  title,
  id,
  actions,
}: {
  title: string;
  id?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-head">
      <h1>{title}</h1>
      {id && <span className="id mono">{id}</span>}
      {actions && <div className="head-actions">{actions}</div>}
    </div>
  );
}
