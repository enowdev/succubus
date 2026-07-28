import {
  ArrowLeftRight,
  Ban,
  CheckCircle2,
  FileText,
  Lock,
  LogIn,
  LogOut,
  Plus,
  Radio,
  Unlock,
} from "lucide-react";
import type { ReactNode } from "react";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Panel, Skeleton } from "../components/ui";
import { relTime } from "../lib/format";

const META: Record<string, { icon: ReactNode; label: string; tone: string }> = {
  "agent.registered": { icon: <LogIn size={15} strokeWidth={1.7} />, label: "joined", tone: "good" },
  "agent.left": { icon: <LogOut size={15} strokeWidth={1.7} />, label: "left", tone: "" },
  "task.created": { icon: <Plus size={15} strokeWidth={1.7} />, label: "created a task", tone: "" },
  "task.updated": { icon: <FileText size={15} strokeWidth={1.7} />, label: "updated a task", tone: "" },
  "task.moved": { icon: <ArrowLeftRight size={15} strokeWidth={1.7} />, label: "moved a task", tone: "accent" },
  "task.deleted": { icon: <Ban size={15} strokeWidth={1.7} />, label: "deleted a task", tone: "" },
  "plan.created": { icon: <Plus size={15} strokeWidth={1.7} />, label: "created a plan", tone: "" },
  "plan.updated": { icon: <FileText size={15} strokeWidth={1.7} />, label: "updated a plan", tone: "" },
  "plan.deleted": { icon: <Ban size={15} strokeWidth={1.7} />, label: "deleted a plan", tone: "" },
  "claim.granted": { icon: <Lock size={15} strokeWidth={1.7} />, label: "claimed files", tone: "warn" },
  "claim.denied": { icon: <Ban size={15} strokeWidth={1.7} />, label: "was denied a claim", tone: "crit" },
  "claim.released": { icon: <Unlock size={15} strokeWidth={1.7} />, label: "released files", tone: "" },
  "claim.expired": { icon: <Unlock size={15} strokeWidth={1.7} />, label: "lease expired", tone: "warn" },
  "decision.created": { icon: <CheckCircle2 size={15} strokeWidth={1.7} />, label: "recorded a decision", tone: "" },
  handoff: { icon: <ArrowLeftRight size={15} strokeWidth={1.7} />, label: "sent a handoff", tone: "accent" },
  report: { icon: <FileText size={15} strokeWidth={1.7} />, label: "reported progress", tone: "" },
};

/** A short, honest summary of what actually changed. */
function detail(payload: Record<string, unknown> | undefined): string {
  if (!payload) return "";
  if (typeof payload.title === "string") return payload.title;
  if (Array.isArray(payload.paths)) return (payload.paths as string[]).join(", ");
  if (typeof payload.path === "string") return payload.path;
  if (typeof payload.reason === "string") return payload.reason;
  if (typeof payload.count === "number") return `${payload.count} file(s)`;
  return "";
}

export function ActivityPage({ state }: { state: ProjectState }) {
  const { project, events, loading } = state;

  return (
    <>
      <PageHead title="Activity" id={project?.display_name} />

      <Panel flush>
        {loading ? (
          <div style={{ padding: 15 }}>
            <Skeleton rows={8} />
          </div>
        ) : events.length === 0 ? (
          <Empty
            icon={<Radio size={28} strokeWidth={1.5} />}
            title="Nothing has happened yet"
            desc="Agent registrations, task changes, and file claims stream in here live."
          />
        ) : (
          <div className="feed" style={{ padding: "4px 15px" }}>
            {events.map((e) => {
              const meta = META[e.type] ?? {
                icon: <FileText size={15} strokeWidth={1.7} />,
                label: e.type,
                tone: "",
              };
              const d = detail(e.payload);
              return (
                <div key={e.id} className="feed-row">
                  <span className={`feed-icon ${meta.tone}`}>{meta.icon}</span>
                  <div className="feed-main">
                    <div className="feed-line">
                      {e.agent_name ? (
                        <AgentTag name={e.agent_name} size="sm" />
                      ) : (
                        <span className="dim">system</span>
                      )}
                      <span>{meta.label}</span>
                    </div>
                    {d && <div className="feed-detail">{d}</div>}
                  </div>
                  <span className="feed-time">{relTime(e.created_at)}</span>
                </div>
              );
            })}
          </div>
        )}
      </Panel>
    </>
  );
}
