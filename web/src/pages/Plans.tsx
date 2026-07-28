import { useEffect, useState } from "react";
import { FileText, Pencil, Plus, Trash2, X } from "lucide-react";
import { api } from "../lib/api";
import type { Plan } from "../lib/types";
import type { ProjectState } from "../lib/useProjectState";
import { Empty, PageHead, Panel, Skeleton } from "../components/ui";
import { useConfirm } from "../components/Confirm";
import { relTime } from "../lib/format";

export function Plans({ state }: { state: ProjectState }) {
  const { project, plans, tasks, loading, refresh } = state;
  const confirm = useConfirm();

  const [editing, setEditing] = useState<Plan | null>(null);
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [status, setStatus] = useState("active");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const open = editing !== null || creating;

  useEffect(() => {
    setTitle(editing?.title ?? "");
    setBody(editing?.body_md ?? "");
    setStatus(editing?.status ?? "active");
    setError(null);
  }, [editing, creating]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  function close() {
    setEditing(null);
    setCreating(false);
  }

  async function save() {
    if (!title.trim() || !project) return;
    setBusy(true);
    setError(null);
    try {
      if (creating) {
        await api.createPlan(project.id, { title: title.trim(), body_md: body, status });
      } else if (editing) {
        await api.updatePlan(editing.id, { title: title.trim(), body_md: body, status });
      }
      refresh();
      close();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(p: Plan) {
    const owned = tasks.filter((t) => t.plan_id === p.id).length;
    const ok = await confirm({
      title: "Delete this plan?",
      message: (
        <>
          <b>{p.title}</b> will be removed.{" "}
          {owned > 0
            ? `Its ${owned} task${owned === 1 ? " is" : "s are"} kept, but no longer grouped under a plan.`
            : "It has no tasks attached."}
        </>
      ),
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    await api.deletePlan(p.id);
    refresh();
  }

  return (
    <>
      <PageHead
        title="Plans"
        id={project?.display_name}
        actions={
          <button className="btn primary" onClick={() => setCreating(true)}>
            <Plus size={14} strokeWidth={1.7} />
            New plan
          </button>
        }
      />

      {loading ? (
        <Panel>
          <Skeleton rows={5} />
        </Panel>
      ) : plans.length === 0 ? (
        <Empty
          icon={<FileText size={28} strokeWidth={1.5} />}
          title="No plans yet"
          desc="A plan is the shared story of what you are building. Agents read the active one on every turn."
          action={
            <button className="btn primary" onClick={() => setCreating(true)}>
              <Plus size={14} strokeWidth={1.7} />
              New plan
            </button>
          }
        />
      ) : (
        <div className="cols rows-auto">
          {plans.map((p) => {
            const owned = tasks.filter((t) => t.plan_id === p.id);
            const done = owned.filter((t) => t.status === "done").length;
            return (
              <Panel
                key={p.id}
                className="span-3"
                title={p.title}
                hint={owned.length > 0 ? `${done}/${owned.length} tasks` : undefined}
                action={
                  <span className="row" style={{ gap: 4 }}>
                    <span className={`tag ${p.status === "active" ? "good" : ""}`}>
                      {p.status}
                    </span>
                    <button
                      className="btn ghost sm"
                      onClick={() => setEditing(p)}
                      aria-label="Edit plan"
                    >
                      <Pencil size={14} strokeWidth={1.7} />
                    </button>
                    <button
                      className="btn ghost sm danger"
                      onClick={() => remove(p)}
                      aria-label="Delete plan"
                    >
                      <Trash2 size={14} strokeWidth={1.7} />
                    </button>
                  </span>
                }
              >
                {owned.length > 0 && (
                  <div className="meter-track" style={{ marginBottom: 12 }}>
                    <i
                      className={done === owned.length ? "good" : ""}
                      style={{ width: `${(done / owned.length) * 100}%` }}
                    />
                  </div>
                )}
                {p.body_md ? (
                  <p className="prose">{p.body_md}</p>
                ) : (
                  <p className="panel-empty">No body yet.</p>
                )}
                <p className="dim" style={{ fontSize: 11.5, marginTop: 12, marginBottom: 0 }}>
                  updated {relTime(p.updated_at)}
                  {p.created_by ? ` · by ${p.created_by}` : ""}
                </p>
              </Panel>
            );
          })}
        </div>
      )}

      {open && (
        <div className="modal-scrim" onClick={close}>
          <div
            className="modal"
            role="dialog"
            aria-modal="true"
            style={{ maxWidth: 620 }}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-head">
              <h2>{creating ? "New plan" : "Edit plan"}</h2>
              <span style={{ marginLeft: "auto" }}>
                <button className="icon-btn" onClick={close} aria-label="Close">
                  <X size={15} strokeWidth={1.7} />
                </button>
              </span>
            </div>
            <div className="modal-body">
              <div className="field">
                <label htmlFor="p-title">Title</label>
                <input
                  id="p-title"
                  className="input"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  autoFocus
                />
              </div>
              <div className="field">
                <label htmlFor="p-body">Body (markdown)</label>
                <textarea
                  id="p-body"
                  className="textarea"
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  rows={12}
                  style={{ minHeight: 200 }}
                />
              </div>
              <div className="field">
                <label htmlFor="p-status">Status</label>
                <select
                  id="p-status"
                  className="select-box"
                  value={status}
                  onChange={(e) => setStatus(e.target.value)}
                >
                  <option value="draft">Draft</option>
                  <option value="active">Active</option>
                  <option value="done">Done</option>
                  <option value="archived">Archived</option>
                </select>
              </div>
              {error && <div className="error-state">{error}</div>}
            </div>
            <div className="modal-foot">
              <div className="right">
                <button className="btn" onClick={close} disabled={busy}>
                  Cancel
                </button>
                <button className="btn primary" onClick={save} disabled={busy || !title.trim()}>
                  {busy ? "Saving…" : creating ? "Create" : "Save"}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
