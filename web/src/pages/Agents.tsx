import { Clock, FolderOpen, Lock, LogOut, Users } from "lucide-react";
import { api } from "../lib/api";
import type { Agent, Claim, Task } from "../lib/types";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Skeleton } from "../components/ui";
import { useConfirm } from "../components/Confirm";
import { relTime, untilTime } from "../lib/format";

/**
 * Says what the status actually means. An agent has no process between turns,
 * so "active" means recently prompted, not currently working — and an idle
 * agent will not answer anything until its session is prompted again.
 */
function statusLabel(status: string): string {
  switch (status) {
    case "active":
      return "taking turns";
    case "idle":
      return "idle";
    case "dead":
      return "gone";
  }
  return status;
}

/** Splits a path so the filename can stay legible when the rest is truncated. */
function splitPath(p: string): { dir: string; file: string } {
  const i = p.lastIndexOf("/");
  return i < 0 ? { dir: "", file: p } : { dir: p.slice(0, i + 1), file: p.slice(i + 1) };
}

/** A titled, independently scrolling section inside an agent card. */
function CardSection({
  icon,
  label,
  count,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  count: number;
  children: React.ReactNode;
}) {
  return (
    <section className="agent-section">
      <header className="agent-section-head">
        {icon}
        <span>{label}</span>
        <span className="count tnum">{count}</span>
      </header>
      <div className="agent-section-body">{children}</div>
    </section>
  );
}

export function Agents({ state }: { state: ProjectState }) {
  const { project, agents, claims, tasks, loading, refresh } = state;
  const confirm = useConfirm();

  async function evict(id: string, name: string) {
    const held = claims.filter((c) => c.agent_id === id).length;
    const ok = await confirm({
      title: `Remove ${name}?`,
      message: held
        ? `${name} will be marked gone and the ${held} file${held === 1 ? "" : "s"} it holds will be released. If the session is still running it will re-register on its next turn.`
        : `${name} will be marked gone. If the session is still running it will re-register on its next turn.`,
      confirmLabel: "Remove",
      danger: true,
    });
    if (!ok) return;
    await api.removeAgent(id);
    refresh();
  }

  if (loading) {
    return (
      <>
        <PageHead title="Agents" id={project?.display_name} />
        <div className="agent-grid">
          {[0, 1].map((i) => (
            <div key={i} className="agent-card">
              <div style={{ padding: 14 }}>
                <Skeleton rows={5} />
              </div>
            </div>
          ))}
        </div>
      </>
    );
  }

  if (agents.length === 0) {
    return (
      <>
        <PageHead title="Agents" id={project?.display_name} />
        <Empty
          icon={<Users size={28} strokeWidth={1.5} />}
          title="No agents yet"
          desc="Open an agent session in this repository. succubus registers it on session start, so it appears here without the agent doing anything."
        />
      </>
    );
  }

  const live = agents.filter((a) => a.status === "active").length;

  return (
    <>
      <PageHead
        title="Agents"
        id={project?.display_name}
        actions={
          <span className="dim" style={{ fontSize: 12.5, alignSelf: "center" }}>
            {live} of {agents.length} taking turns
          </span>
        }
      />

      <div className="agent-grid">
        {agents.map((a: Agent) => {
          const held: Claim[] = claims.filter((c) => c.agent_id === a.id);
          const owned: Task[] = tasks.filter(
            (t) =>
              t.assignee_name === a.name &&
              t.status !== "done" &&
              t.status !== "cancelled",
          );
          const doing = owned.filter((t) => t.status === "in_progress").length;

          return (
            <article key={a.id} className={`agent-card ${a.status}`}>
              {/* Header stays put; only the sections below it scroll. */}
              <header className="agent-card-head">
                <div className="agent-card-id">
                  <AgentTag name={a.name} status={a.status} />
                  <span className="agent-card-tool mono">{a.tool}</span>
                </div>
                <button
                  className="btn ghost sm"
                  onClick={() => evict(a.id, a.name)}
                  aria-label={`Remove ${a.name}`}
                  title="Remove and release its claims"
                >
                  <LogOut size={14} strokeWidth={1.7} />
                </button>
              </header>

              <div className="agent-card-meta">
                <span className="agent-meta-item">
                  <Clock size={11} strokeWidth={1.9} />
                  {statusLabel(a.status)} · {relTime(a.last_heartbeat_at)}
                </span>
                {a.pid ? <span className="agent-meta-item mono">pid {a.pid}</span> : null}
              </div>

              <div className="agent-card-stats">
                <div className="agent-stat">
                  <span className="agent-stat-val tnum">{owned.length}</span>
                  <span className="agent-stat-label">
                    task{owned.length === 1 ? "" : "s"}
                    {doing > 0 ? ` · ${doing} active` : ""}
                  </span>
                </div>
                <div className="agent-stat">
                  <span className="agent-stat-val tnum">{held.length}</span>
                  <span className="agent-stat-label">
                    file{held.length === 1 ? "" : "s"} locked
                  </span>
                </div>
              </div>

              <div className="agent-card-body">
                {owned.length === 0 && held.length === 0 && (
                  <p className="panel-empty" style={{ padding: "14px 15px" }}>
                    Not working on anything yet.
                  </p>
                )}

                {owned.length > 0 && (
                  <CardSection
                    icon={<FolderOpen size={11} strokeWidth={1.9} />}
                    label="Working on"
                    count={owned.length}
                  >
                    {owned.map((t) => (
                      <div key={t.id} className="agent-row">
                        <span className="agent-row-main truncate" title={t.title}>
                          {t.title}
                        </span>
                        <span className={`tag ${t.status === "in_progress" ? "accent" : ""}`}>
                          {t.status.replace("_", " ")}
                        </span>
                      </div>
                    ))}
                  </CardSection>
                )}

                {held.length > 0 && (
                  <CardSection
                    icon={<Lock size={11} strokeWidth={1.9} />}
                    label="Holding"
                    count={held.length}
                  >
                    {held.map((c) => {
                      const { dir, file } = splitPath(c.path);
                      return (
                        <div key={c.path} className="agent-row">
                          {/* The directory truncates from the left so the
                              filename — the part you actually read — survives. */}
                          <span className="agent-path mono" title={c.path}>
                            {dir && <span className="agent-path-dir">{dir}</span>}
                            <span className="agent-path-file">{file}</span>
                          </span>
                          <span className="dim agent-row-meta tnum">
                            {untilTime(c.expires_at)}
                          </span>
                        </div>
                      );
                    })}
                  </CardSection>
                )}
              </div>
            </article>
          );
        })}
      </div>
    </>
  );
}
