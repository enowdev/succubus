import { useEffect, useState } from "react";
import { Trash2, X } from "lucide-react";
import { api } from "../lib/api";
import { useConfirm } from "./Confirm";
import {
  BOARD_COLUMNS,
  STATUS_LABEL,
  type Agent,
  type Plan,
  type Task,
  type TaskStatus,
} from "../lib/types";

interface Props {
  projectId: string;
  task: Task | null;
  creating: boolean;
  agents: Agent[];
  plans: Plan[];
  allTasks: Task[];
  onClose: () => void;
  onSaved: () => void;
}

/** Create/edit surface for a task. Centred panel on desktop, bottom sheet on a
 *  phone — same component, driven by CSS. */
export function TaskModal({
  projectId, task, creating, agents, plans, allTasks, onClose, onSaved,
}: Props) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [status, setStatus] = useState<TaskStatus>("todo");
  const [priority, setPriority] = useState(2);
  const [assignee, setAssignee] = useState("");
  const [planId, setPlanId] = useState("");
  const [deps, setDeps] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const confirm = useConfirm();

  useEffect(() => {
    setTitle(task?.title ?? "");
    setBody(task?.body_md ?? "");
    setStatus(task?.status ?? "todo");
    setPriority(task?.priority ?? 2);
    setAssignee(task?.assignee_name ?? "");
    setPlanId(task?.plan_id ?? "");
    setDeps(task?.depends_on ?? []);
    setError(null);
  }, [task, creating]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function save() {
    if (!title.trim()) {
      setError("Title is required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (creating) {
        await api.createTask(projectId, {
          title: title.trim(), body_md: body, status, priority,
          plan_id: planId || undefined,
          assignee_name: assignee || undefined,
          depends_on: deps.length ? deps : undefined,
        });
      } else if (task) {
        await api.updateTask(task.id, {
          title: title.trim(), body_md: body, status, priority,
          plan_id: planId, assignee_name: assignee,
        });
        // Dependencies are a separate edge set, reconciled by diff.
        const before = new Set(task.depends_on);
        const after = new Set(deps);
        for (const d of after) if (!before.has(d)) await api.addDep(task.id, d);
        for (const d of before) if (!after.has(d)) await api.removeDep(task.id, d);
      }
      onSaved();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!task) return;
    const dependents = allTasks.filter((t) => t.depends_on.includes(task.id));
    const ok = await confirm({
      title: "Delete this task?",
      message: (
        <>
          <b>{task.title}</b> will be removed permanently.
          {dependents.length > 0 &&
            ` ${dependents.length} task${dependents.length === 1 ? "" : "s"} depending on it will be unblocked.`}
        </>
      ),
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    setBusy(true);
    try {
      await api.deleteTask(task.id);
      onSaved();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  const candidates = allTasks.filter((t) => t.id !== task?.id);

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={creating ? "New task" : "Edit task"}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2>{creating ? "New task" : "Edit task"}</h2>
          <span style={{ marginLeft: "auto" }}>
            <button className="icon-btn" onClick={onClose} aria-label="Close">
              <X size={15} strokeWidth={1.7} />
            </button>
          </span>
        </div>

        <div className="modal-body">
          <div className="field">
            <label htmlFor="t-title">Title</label>
            <input
              id="t-title"
              className="input"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="What needs doing?"
              autoFocus={creating}
            />
          </div>

          <div className="field">
            <label htmlFor="t-body">Details</label>
            <textarea
              id="t-body"
              className="textarea"
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={4}
              placeholder="Markdown supported"
            />
          </div>

          <div className="field-row">
            <div className="field">
              <label htmlFor="t-status">Status</label>
              <select
                id="t-status"
                className="select-box"
                value={status}
                onChange={(e) => setStatus(e.target.value as TaskStatus)}
              >
                {BOARD_COLUMNS.map((s) => (
                  <option key={s} value={s}>
                    {STATUS_LABEL[s]}
                  </option>
                ))}
                <option value="cancelled">Cancelled</option>
              </select>
            </div>
            <div className="field">
              <label htmlFor="t-prio">Priority</label>
              <select
                id="t-prio"
                className="select-box"
                value={priority}
                onChange={(e) => setPriority(Number(e.target.value))}
              >
                <option value={1}>High</option>
                <option value={2}>Normal</option>
                <option value={3}>Low</option>
              </select>
            </div>
          </div>

          <div className="field-row">
            <div className="field">
              <label htmlFor="t-assignee">Assignee</label>
              <select
                id="t-assignee"
                className="select-box"
                value={assignee}
                onChange={(e) => setAssignee(e.target.value)}
              >
                <option value="">Unassigned</option>
                {agents.map((a) => (
                  <option key={a.id} value={a.name}>
                    {a.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="t-plan">Plan</label>
              <select
                id="t-plan"
                className="select-box"
                value={planId}
                onChange={(e) => setPlanId(e.target.value)}
              >
                <option value="">None</option>
                {plans.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.title}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="field">
            <label>Depends on ({deps.length})</label>
            <div className="check-list">
              {candidates.length === 0 ? (
                <p className="panel-empty" style={{ padding: "6px 8px" }}>
                  No other tasks yet.
                </p>
              ) : (
                candidates.map((t) => (
                  <label key={t.id} className="check-row">
                    <input
                      type="checkbox"
                      checked={deps.includes(t.id)}
                      onChange={(e) =>
                        setDeps((prev) =>
                          e.target.checked ? [...prev, t.id] : prev.filter((x) => x !== t.id),
                        )
                      }
                    />
                    <span className="truncate">{t.title}</span>
                  </label>
                ))
              )}
            </div>
          </div>

          {error && <div className="error-state">{error}</div>}
        </div>

        <div className="modal-foot">
          {!creating && (
            <button className="btn ghost danger" onClick={remove} disabled={busy} aria-label="Delete task">
              <Trash2 size={14} strokeWidth={1.7} />
            </button>
          )}
          <div className="right">
            <button className="btn" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button className="btn primary" onClick={save} disabled={busy}>
              {busy ? "Saving…" : creating ? "Create" : "Save"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
