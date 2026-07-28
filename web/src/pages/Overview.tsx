import { Columns3, Lock, TriangleAlert, Users } from "lucide-react";
import type { ProjectState } from "../lib/useProjectState";
import type { Page } from "../components/Sidebar";
import { AgentTag, Empty, Kpi, PageHead, Panel, Skeleton, StatusTag } from "../components/ui";
import { relTime, shortPath, untilTime } from "../lib/format";

export function Overview({
  state,
  onNavigate,
}: {
  state: ProjectState;
  onNavigate: (p: Page) => void;
}) {
  const { project, agents, tasks, claims, plans, decisions, loading } = state;

  const live = agents.filter((a) => a.status === "active");
  const open = tasks.filter((t) => t.status !== "done" && t.status !== "cancelled");
  const blocked = tasks.filter((t) => t.blocked && t.status !== "done");
  const inFlight = tasks.filter((t) => t.status === "in_progress");
  const activePlan = plans.find((p) => p.status === "active");
  const planTasks = activePlan ? tasks.filter((t) => t.plan_id === activePlan.id) : [];
  const planDone = planTasks.filter((t) => t.status === "done").length;
  const unread = decisions.filter((d) => d.target_agent_name && !d.ack_at);

  return (
    <>
      <PageHead title="Overview" id={project?.display_name} />

      <div className="kpis">
        <Kpi
          label="Agents live"
          icon={<Users size={13} strokeWidth={1.7} />}
          value={live.length}
          sub={
            agents.length > live.length
              ? `${agents.length - live.length} idle or gone`
              : "all active"
          }
          onClick={() => onNavigate("agents")}
        />
        <Kpi
          label="Open tasks"
          icon={<Columns3 size={13} strokeWidth={1.7} />}
          value={open.length}
          sub={`${inFlight.length} in progress`}
          onClick={() => onNavigate("board")}
        />
        <Kpi
          label="Files locked"
          icon={<Lock size={13} strokeWidth={1.7} />}
          value={claims.length}
          sub={claims.length ? "leases auto-expire" : "nothing held"}
          onClick={() => onNavigate("claims")}
        />
        <Kpi
          label="Blocked"
          icon={<TriangleAlert size={13} strokeWidth={1.7} />}
          value={blocked.length}
          sub={blocked.length ? "waiting on dependencies" : "nothing blocked"}
          onClick={() => onNavigate("board")}
        />
      </div>

      {/* Row 1 — the plan spans two columns and two rows, so the two short
          panels beside it stack instead of leaving a gap under one of them. */}
      <div className="cols rows-sm">
        <Panel
          title="Active plan"
          className="span-2 tall"
          hint={
            activePlan && planTasks.length > 0
              ? `${planDone}/${planTasks.length} done`
              : undefined
          }
        >
          {loading ? (
            <Skeleton rows={5} />
          ) : !activePlan ? (
            <Empty
              title="No active plan"
              desc="A plan is the shared story of what you are building. Agents read it every turn."
            />
          ) : (
            <>
              <p style={{ margin: 0, fontWeight: 600, fontSize: 14 }}>{activePlan.title}</p>
              {planTasks.length > 0 && (
                <div className="meter-track" style={{ margin: "10px 0 0" }}>
                  <i
                    className={planDone === planTasks.length ? "good" : ""}
                    style={{ width: `${(planDone / planTasks.length) * 100}%` }}
                  />
                </div>
              )}
              {activePlan.body_md && (
                <p className="prose" style={{ marginTop: 12 }}>
                  {activePlan.body_md}
                </p>
              )}
            </>
          )}
        </Panel>

        <Panel title="Who is here" hint={`${live.length} live`}>
          {loading ? (
            <Skeleton rows={3} />
          ) : agents.length === 0 ? (
            <p className="panel-empty">
              No agents yet. They register themselves on session start.
            </p>
          ) : (
            agents.map((a) => (
              <div key={a.id} className="stat-line">
                <span className="k">
                  <AgentTag name={a.name} status={a.status} size="sm" />
                </span>
                <span className="v dim">{relTime(a.last_heartbeat_at)}</span>
              </div>
            ))
          )}
        </Panel>

        <Panel title="In flight" hint={inFlight.length ? `${inFlight.length}` : "idle"}>
          {loading ? (
            <Skeleton rows={3} />
          ) : inFlight.length === 0 ? (
            <p className="panel-empty">Nothing is being worked on right now.</p>
          ) : (
            inFlight.map((t) => (
              <div key={t.id} className="stat-line">
                <span className="k truncate" style={{ maxWidth: "58%" }} title={t.title}>
                  {t.title}
                </span>
                <span className="v">
                  <AgentTag name={t.assignee_name} size="sm" />
                </span>
              </div>
            ))
          )}
        </Panel>
      </div>

      {/* Row 2 — three equal panels, all the same height. */}
      <div className="cols rows-md">
        <Panel
          title="Files being edited"
          hint={claims.length ? `${claims.length} held` : undefined}
        >
          {loading ? (
            <Skeleton rows={3} />
          ) : claims.length === 0 ? (
            <p className="panel-empty">
              No claims. Agents lease files with succubus_claim_files before editing.
            </p>
          ) : (
            claims.map((c) => (
              <div key={c.path} className="stat-line">
                <span className="k mono truncate" style={{ maxWidth: "62%" }} title={c.path}>
                  {shortPath(c.path, 34)}
                </span>
                <span className="v row" style={{ gap: 8, justifyContent: "flex-end" }}>
                  <AgentTag name={c.agent_name} size="sm" />
                  <span className="dim" style={{ fontSize: 11.5 }}>
                    {untilTime(c.expires_at)}
                  </span>
                </span>
              </div>
            ))
          )}
        </Panel>

        <Panel
          title="Handoffs & decisions"
          hint={unread.length ? `${unread.length} unread` : undefined}
        >
          {loading ? (
            <Skeleton rows={3} />
          ) : decisions.length === 0 ? (
            <p className="panel-empty">No decisions or handoffs recorded.</p>
          ) : (
            decisions.map((d) => (
              <div key={d.id} className="stat-line">
                <span className="k truncate" style={{ maxWidth: "68%" }} title={d.title}>
                  {d.title}
                </span>
                <span className="v">
                  {d.target_agent_name && !d.ack_at ? (
                    <span className="tag warn">unread</span>
                  ) : (
                    <span className="tag">{d.kind}</span>
                  )}
                </span>
              </div>
            ))
          )}
        </Panel>

        <Panel
          title="Blocked"
          hint={blocked.length ? `${blocked.length}` : undefined}
        >
          {loading ? (
            <Skeleton rows={3} />
          ) : blocked.length === 0 ? (
            <p className="panel-empty">Nothing is waiting on a dependency.</p>
          ) : (
            blocked.map((t) => (
              <div key={t.id} className="stat-line">
                <span className="k truncate" style={{ maxWidth: "62%" }} title={t.title}>
                  {t.title}
                </span>
                <span className="v row" style={{ gap: 8, justifyContent: "flex-end" }}>
                  <StatusTag status={t.status} />
                  <span className="dim" style={{ fontSize: 11.5 }}>
                    {t.depends_on.length} dep{t.depends_on.length === 1 ? "" : "s"}
                  </span>
                </span>
              </div>
            ))
          )}
        </Panel>
      </div>
    </>
  );
}
