import { useEffect, useState } from "react";
import { Check, MessageSquare, Plus, Trash2, X } from "lucide-react";
import { api } from "../lib/api";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Panel, Skeleton } from "../components/ui";
import { useConfirm } from "../components/Confirm";
import { relTime } from "../lib/format";

export function Notes({ state }: { state: ProjectState }) {
  const { project, decisions, agents, loading, refresh } = state;
  const confirm = useConfirm();

  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState("decision");
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  async function save() {
    if (!title.trim() || !project) return;
    setBusy(true);
    setError(null);
    try {
      await api.createDecision(project.id, {
        kind,
        title: title.trim(),
        body_md: body,
        author_name: "HUMAN",
        target_agent_name: kind === "handoff" ? target : undefined,
      });
      setTitle("");
      setBody("");
      setTarget("");
      setOpen(false);
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string, title: string) {
    const ok = await confirm({
      title: "Delete this note?",
      message: (
        <>
          <b>{title}</b> will be removed from the decision log permanently.
        </>
      ),
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    await api.deleteDecision(id);
    refresh();
  }

  const unread = decisions.filter((d) => d.target_agent_name && !d.ack_at).length;

  return (
    <>
      <PageHead
        title="Decisions & handoffs"
        id={project?.display_name}
        actions={
          <button className="btn primary" onClick={() => setOpen(true)}>
            <Plus size={14} strokeWidth={1.7} />
            New note
          </button>
        }
      />

      {loading ? (
        <Panel>
          <Skeleton rows={5} />
        </Panel>
      ) : decisions.length === 0 ? (
        <Empty
          icon={<MessageSquare size={28} strokeWidth={1.5} />}
          title="Nothing recorded yet"
          desc="Decisions explain why the code looks the way it does. Handoffs are addressed to a named agent and show up in that agent's next context."
          action={
            <button className="btn primary" onClick={() => setOpen(true)}>
              <Plus size={14} strokeWidth={1.7} />
              New note
            </button>
          }
        />
      ) : (
        <div className="cols rows-auto">
          {unread > 0 && (
            <div className="span-3">
              <div className="warn-box">
                {unread} handoff{unread === 1 ? "" : "s"} not yet read by the target agent.
              </div>
            </div>
          )}
          {decisions.map((d) => (
            <Panel
              key={d.id}
              className="span-3"
              title={d.title}
              action={
                <span className="row" style={{ gap: 4 }}>
                  <span className={`tag ${d.kind === "handoff" ? "warn" : d.kind === "decision" ? "accent" : ""}`}>
                    {d.kind}
                  </span>
                  {d.target_agent_name && !d.ack_at && (
                    <button
                      className="btn ghost sm"
                      onClick={() => api.ackDecision(d.id).then(refresh)}
                      aria-label="Mark read"
                      title="Mark read"
                    >
                      <Check size={14} strokeWidth={1.7} />
                    </button>
                  )}
                  <button
                    className="btn ghost sm danger"
                    onClick={() => remove(d.id, d.title)}
                    aria-label="Delete"
                  >
                    <Trash2 size={14} strokeWidth={1.7} />
                  </button>
                </span>
              }
            >
              <div className="row wrap" style={{ gap: 8, marginBottom: 8 }}>
                <AgentTag name={d.author_name} size="sm" />
                {d.target_agent_name && (
                  <>
                    <span className="dim">→</span>
                    <AgentTag name={d.target_agent_name} size="sm" />
                    {!d.ack_at && <span className="tag warn">unread</span>}
                  </>
                )}
                <span className="dim" style={{ fontSize: 11.5 }}>
                  {relTime(d.created_at)}
                </span>
              </div>
              {d.body_md && <p className="prose">{d.body_md}</p>}
            </Panel>
          ))}
        </div>
      )}

      {open && (
        <div className="modal-scrim" onClick={() => setOpen(false)}>
          <div
            className="modal"
            role="dialog"
            aria-modal="true"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="modal-head">
              <h2>New note</h2>
              <span style={{ marginLeft: "auto" }}>
                <button className="icon-btn" onClick={() => setOpen(false)} aria-label="Close">
                  <X size={15} strokeWidth={1.7} />
                </button>
              </span>
            </div>
            <div className="modal-body">
              <div className="field">
                <label htmlFor="n-kind">Kind</label>
                <select
                  id="n-kind"
                  className="select-box"
                  value={kind}
                  onChange={(e) => setKind(e.target.value)}
                >
                  <option value="decision">Decision</option>
                  <option value="note">Note</option>
                  <option value="handoff">Handoff to an agent</option>
                </select>
              </div>

              {kind === "handoff" && (
                <div className="field">
                  <label htmlFor="n-target">To agent</label>
                  <select
                    id="n-target"
                    className="select-box"
                    value={target}
                    onChange={(e) => setTarget(e.target.value)}
                  >
                    <option value="">Choose an agent…</option>
                    {agents.map((a) => (
                      <option key={a.id} value={a.name}>
                        {a.name}
                      </option>
                    ))}
                  </select>
                </div>
              )}

              <div className="field">
                <label htmlFor="n-title">Title</label>
                <input
                  id="n-title"
                  className="input"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  autoFocus
                />
              </div>

              <div className="field">
                <label htmlFor="n-body">Body</label>
                <textarea
                  id="n-body"
                  className="textarea"
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  rows={6}
                />
              </div>

              {error && <div className="error-state">{error}</div>}
            </div>
            <div className="modal-foot">
              <div className="right">
                <button className="btn" onClick={() => setOpen(false)} disabled={busy}>
                  Cancel
                </button>
                <button className="btn primary" onClick={save} disabled={busy || !title.trim()}>
                  {busy ? "Saving…" : "Create"}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
