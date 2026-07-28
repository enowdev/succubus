import { useCallback, useEffect, useState } from "react";
import { Check, MessagesSquare, Trash2 } from "lucide-react";
import { api } from "../lib/api";
import type { Message, MessageKind, RoomPayload } from "../lib/types";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Panel, Skeleton } from "../components/ui";
import { useConfirm } from "../components/Confirm";
import { relTime } from "../lib/format";

const KIND_TAG: Record<MessageKind, { label: string; tone: string }> = {
  question: { label: "question", tone: "warn" },
  answer: { label: "answer", tone: "good" },
  announce: { label: "announce", tone: "accent" },
  message: { label: "", tone: "" },
};

/**
 * The agent room: one shared conversation per project.
 *
 * This is a window onto what the agents are saying to each other, not a chat
 * client. There is deliberately no composer.
 *
 * The reason is timing, not capability. A channel event can only be inserted
 * into a turn that is already running, so a message reaches a *working* agent
 * in under a second and an *idle* one not at all until it is next given work.
 * Agents are almost always mid-turn when they post — that is what makes
 * agent-to-agent reliable and human-to-agent unpredictable. A text box that
 * looks like chat but behaves like email is worse than no text box.
 *
 * To reach an agent yourself, prompt its session directly, or run
 * `succubus wake --agent NAME` to start a short headless turn that answers.
 */
export function Room({ state }: { state: ProjectState }) {
  const { project, agents, lastEventType } = state;
  const confirm = useConfirm();

  const [room, setRoom] = useState<RoomPayload | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!project) return;
    try {
      setRoom(await api.room(project.id));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [project]);

  useEffect(() => {
    load();
  }, [load]);

  // Live: any room event refetches, so a message posted by an agent appears
  // without a reload. Handoffs land in the room too and carry no "room."
  // prefix, so they are matched explicitly.
  useEffect(() => {
    if (!lastEventType) return;
    if (lastEventType.startsWith("room.") || lastEventType.startsWith("handoff")) load();
  }, [lastEventType, load]);

  async function resolve(m: Message) {
    await api.resolveQuestion(m.id, "HUMAN");
    load();
  }

  async function remove(m: Message) {
    const replies = m.replies?.length ?? 0;
    const ok = await confirm({
      title: "Delete this message?",
      message: replies
        ? `It disappears for every agent too, along with its ${replies} repl${replies === 1 ? "y" : "ies"}.`
        : "It disappears for every agent too.",
      confirmLabel: "Delete",
      danger: true,
    });
    if (!ok) return;
    await api.deleteMessage(m.id);
    load();
  }

  const agentOf = (name: string) => agents.find((a) => a.name === name);
  const openCount = room?.open ?? 0;

  return (
    <>
      <PageHead
        title="Agent room"
        id={project?.display_name}
        actions={
          openCount > 0 ? (
            <span className="tag warn" style={{ alignSelf: "center" }}>
              {openCount} unanswered
            </span>
          ) : undefined
        }
      />

      {error && <div className="error-state">{error}</div>}

      {!room ? (
        <Panel>
          <Skeleton rows={6} />
        </Panel>
      ) : room.messages.length === 0 ? (
        <Empty
          icon={<MessagesSquare size={28} strokeWidth={1.5} />}
          title="The room is quiet"
          desc="Agents post here when they are unsure about something — an ambiguous requirement, a convention they cannot infer. Their questions and answers to each other appear here as they happen."
        />
      ) : (
        <div className="room">
          {room.messages.map((m) => {
            const tag = KIND_TAG[m.kind];
            const unanswered = m.kind === "question" && !m.resolved_at;
            return (
              <article key={m.id} className={`msg ${unanswered ? "unanswered" : ""}`}>
                <div className="msg-head">
                  <AgentTag
                    name={m.author_name}
                    status={agentOf(m.author_name)?.status}
                    tool={agentOf(m.author_name)?.tool}
                    size="sm"
                  />
                  {tag.label && <span className={`tag ${tag.tone}`}>{tag.label}</span>}
                  {m.resolved_at && <span className="tag good">resolved</span>}
                  {m.mentions.length > 0 && (
                    <span className="dim" style={{ fontSize: 11.5 }}>
                      → {m.mentions.join(", ")}
                    </span>
                  )}
                  <span className="msg-time">{relTime(m.created_at)}</span>
                </div>

                <p className="msg-body">{m.body_md}</p>

                {m.replies && m.replies.length > 0 && (
                  <div className="msg-replies">
                    {m.replies.map((r) => (
                      <div key={r.id} className="msg-reply">
                        <div className="msg-head">
                          <AgentTag
                            name={r.author_name}
                            status={agentOf(r.author_name)?.status}
                            tool={agentOf(r.author_name)?.tool}
                            size="sm"
                          />
                          {KIND_TAG[r.kind].label && (
                            <span className={`tag ${KIND_TAG[r.kind].tone}`}>
                              {KIND_TAG[r.kind].label}
                            </span>
                          )}
                          <span className="msg-time">{relTime(r.created_at)}</span>
                        </div>
                        <p className="msg-body">{r.body_md}</p>
                      </div>
                    ))}
                  </div>
                )}

                {/* Housekeeping only: closing a question that has been dealt
                    with, and removing noise. Not a way to talk back. */}
                <div className="msg-actions">
                  {unanswered && (
                    <button className="btn ghost sm" onClick={() => resolve(m)}>
                      <Check size={13} strokeWidth={1.8} />
                      Mark resolved
                    </button>
                  )}
                  <button
                    className="btn ghost sm danger"
                    onClick={() => remove(m)}
                    aria-label="Delete message"
                    style={{ marginLeft: "auto" }}
                  >
                    <Trash2 size={13} strokeWidth={1.8} />
                  </button>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </>
  );
}
