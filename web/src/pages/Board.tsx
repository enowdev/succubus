import { useMemo, useState } from "react";
import { Lock, Plus, TriangleAlert } from "lucide-react";
import { api } from "../lib/api";
import { BOARD_COLUMNS, STATUS_LABEL, type Task, type TaskStatus } from "../lib/types";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Skeleton } from "../components/ui";
import { TaskModal } from "../components/TaskModal";

export function Board({ state }: { state: ProjectState }) {
  const { project, tasks, claims, agents, plans, loading, refresh, patchTask } = state;

  const [editing, setEditing] = useState<Task | null>(null);
  const [creating, setCreating] = useState(false);
  const [dragged, setDragged] = useState<Task | null>(null);
  const [overCol, setOverCol] = useState<TaskStatus | null>(null);

  const byColumn = useMemo(() => {
    const m = new Map<TaskStatus, Task[]>();
    for (const s of BOARD_COLUMNS) m.set(s, []);
    for (const t of tasks) {
      if (t.status === "cancelled") continue;
      m.get(t.status)?.push(t);
    }
    for (const list of m.values()) list.sort((a, b) => a.sort_key - b.sort_key);
    return m;
  }, [tasks]);

  async function drop(status: TaskStatus, index: number) {
    const t = dragged;
    setDragged(null);
    setOverCol(null);
    if (!t) return;
    if (t.status === status && index < 0) return;

    // Move locally first so the card does not snap back mid-request.
    patchTask(t.id, { status });
    try {
      await api.reorderTask(t.id, status, index < 0 ? 9999 : index);
    } finally {
      refresh();
    }
  }

  return (
    <>
      <PageHead
        title="Board"
        id={project?.display_name}
        actions={
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Plus size={14} strokeWidth={1.7} />
            New task
          </button>
        }
      />

      {loading ? (
        <div className="board">
          {BOARD_COLUMNS.map((s) => (
            <div key={s} className="board-col">
              <div className="board-col-head">
                <h2>{STATUS_LABEL[s]}</h2>
              </div>
              <div className="board-col-body">
                <Skeleton rows={2} />
              </div>
            </div>
          ))}
        </div>
      ) : tasks.length === 0 ? (
        <Empty
          title="No tasks yet"
          desc="Create one here, or let an agent add tasks with succubus_task_create."
          action={
            <button className="btn primary" onClick={() => setCreating(true)}>
              <Plus size={14} strokeWidth={1.7} />
              New task
            </button>
          }
        />
      ) : (
        <div className="board">
          {BOARD_COLUMNS.map((status) => {
            const list = byColumn.get(status) ?? [];
            return (
              <section
                key={status}
                className={`board-col ${overCol === status ? "drop" : ""}`}
                onDragOver={(e) => {
                  e.preventDefault();
                  setOverCol(status);
                }}
                onDragLeave={() => setOverCol((c) => (c === status ? null : c))}
                onDrop={(e) => {
                  e.preventDefault();
                  drop(status, -1);
                }}
              >
                <div className="board-col-head">
                  <h2>{STATUS_LABEL[status]}</h2>
                  <span className="count">{list.length}</span>
                </div>
                <div className="board-col-body">
                  {list.map((t, i) => {
                    const held = claims.filter((c) => c.task_id === t.id).length;
                    return (
                      <article
                        key={t.id}
                        className={`task p${t.priority} ${dragged?.id === t.id ? "dragging" : ""}`}
                        draggable
                        onDragStart={(e) => {
                          e.dataTransfer.effectAllowed = "move";
                          e.dataTransfer.setData("text/plain", t.id);
                          setDragged(t);
                        }}
                        onDragEnd={() => {
                          setDragged(null);
                          setOverCol(null);
                        }}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                          drop(status, i);
                        }}
                        onClick={() => setEditing(t)}
                        role="button"
                        tabIndex={0}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            setEditing(t);
                          }
                        }}
                      >
                        <p className="task-title">{t.title}</p>
                        <div className="task-meta">
                          <AgentTag name={t.assignee_name} size="sm" />
                          {t.blocked && (
                            <span className="flag crit">
                              <TriangleAlert size={12} strokeWidth={1.8} />
                              blocked
                            </span>
                          )}
                          {held > 0 && (
                            <span className="flag muted" title="files locked for this task">
                              <Lock size={12} strokeWidth={1.8} />
                              {held}
                            </span>
                          )}
                          {t.depends_on.length > 0 && !t.blocked && (
                            <span className="muted">
                              {t.depends_on.length} dep{t.depends_on.length === 1 ? "" : "s"}
                            </span>
                          )}
                        </div>
                      </article>
                    );
                  })}
                  {list.length === 0 && (
                    <p className="dim" style={{ fontSize: 11.5, textAlign: "center", padding: "14px 0" }}>
                      Empty
                    </p>
                  )}
                </div>
              </section>
            );
          })}
        </div>
      )}

      {(editing || creating) && project && (
        <TaskModal
          projectId={project.id}
          task={editing}
          creating={creating}
          agents={agents}
          plans={plans}
          allTasks={tasks}
          onClose={() => {
            setEditing(null);
            setCreating(false);
          }}
          onSaved={refresh}
        />
      )}
    </>
  );
}
