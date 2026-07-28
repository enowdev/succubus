import { useEffect, useState } from "react";
import {
  Activity,
  BookOpen,
  ChevronRight,
  Columns3,
  FileText,
  FolderGit2,
  LayoutGrid,
  Lock,
  MessageSquare,
  MessagesSquare,
  Trash2,
  Users,
} from "lucide-react";
import type { AgentSummary, ProjectSummary } from "../lib/types";
import { agentColor } from "../lib/format";

export type Page =
  | "overview"
  | "board"
  | "plans"
  | "agents"
  | "claims"
  | "room"
  | "notes"
  | "activity"
  | "docs";

const NAV: { label: string; page: Page; icon: typeof LayoutGrid }[] = [
  { label: "Overview", page: "overview", icon: LayoutGrid },
  { label: "Board", page: "board", icon: Columns3 },
  { label: "Plans", page: "plans", icon: FileText },
  { label: "Agents", page: "agents", icon: Users },
  { label: "Claims", page: "claims", icon: Lock },
  { label: "Room", page: "room", icon: MessagesSquare },
  { label: "Notes", page: "notes", icon: MessageSquare },
  { label: "Activity", page: "activity", icon: Activity },
];

function dotColor(status: AgentSummary["status"]): string {
  if (status === "active") return "var(--good)";
  if (status === "idle") return "var(--warn)";
  return "var(--border-strong)";
}

interface Props {
  page: Page;
  onNavigate: (p: Page) => void;
  tree: ProjectSummary[];
  activeProject: string | null;
  onSelectProject: (id: string) => void;
  /** Unanswered questions in the room — the badge worth noticing. */
  openQuestions: number;
  /** Omit to hide the per-project delete affordance. */
  onForgetProject?: (p: ProjectSummary) => void;
  open: boolean;
  onClose: () => void;
}

/**
 * Persistent left rail: brand, page nav, then a project tree with each
 * project's agents nested beneath it.
 *
 * Nesting agents under their project is the point — an agent only exists in the
 * context of one project, and a flat list of names hides that. Collapsing lets
 * a machine with many projects stay readable.
 */
export function Sidebar({
  page, onNavigate, tree, activeProject, onSelectProject, openQuestions,
  onForgetProject, open, onClose,
}: Props) {
  // Collapsed by default for everything except the project you are in.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());

  useEffect(() => {
    // A newly selected project should always reveal its agents.
    if (activeProject) {
      setCollapsed((prev) => {
        if (!prev.has(activeProject)) return prev;
        const next = new Set(prev);
        next.delete(activeProject);
        return next;
      });
    }
  }, [activeProject]);

  const active = tree.find((p) => p.id === activeProject);

  function toggle(id: string) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  function go(p: Page) {
    onNavigate(p);
    onClose();
  }

  return (
    <aside className={`sidebar ${open ? "open" : ""}`}>
      <div className="brand">
        <img className="brand-mark" src="/icon-192.png" alt="" aria-hidden="true" />
        <div className="brand-name">
          succubus<span>·agents</span>
        </div>
      </div>

      {NAV.map(({ label, page: p, icon: Icon }) => {
        const badge =
          p === "board"
            ? (active?.open_tasks ?? 0)
            : p === "claims"
              ? (active?.claims ?? 0)
              : p === "agents"
                ? (active?.agents.length ?? 0)
                : p === "room"
                  ? openQuestions
                  : 0;
        return (
          <div
            key={p}
            className={`nav-item ${page === p ? "active" : ""}`}
            onClick={() => go(p)}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                go(p);
              }
            }}
          >
            <Icon size={15} strokeWidth={1.6} />
            {label}
            {badge > 0 && (
              // An unanswered question is waiting on a person, so it gets
              // colour where the other counts stay neutral.
              <span className={`badge tnum ${p === "room" ? "alert" : ""}`}>{badge}</span>
            )}
          </div>
        );
      })}

      <div className="nav-label">
        Projects
        {tree.length > 0 && <span className="count tnum">{tree.length}</span>}
      </div>

      <div className="side-list grow">
        {tree.length === 0 ? (
          <div className="side-empty">No projects registered yet.</div>
        ) : (
          tree.map((proj) => {
            const isOpen = !collapsed.has(proj.id);
            const isActive = proj.id === activeProject;
            const live = proj.agents.filter((a) => a.status === "active").length;

            return (
              <div key={proj.id} className="tree-node">
                <div
                  className={`row-item tree-project ${isActive ? "active" : ""}`}
                  onClick={() => {
                    onSelectProject(proj.id);
                    onClose();
                  }}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelectProject(proj.id);
                      onClose();
                    }
                  }}
                  title={proj.root_path}
                >
                  <button
                    className={`tree-caret ${isOpen ? "open" : ""}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      toggle(proj.id);
                    }}
                    aria-label={isOpen ? "Collapse" : "Expand"}
                    aria-expanded={isOpen}
                    tabIndex={-1}
                  >
                    <ChevronRight size={12} strokeWidth={2} />
                  </button>
                  <FolderGit2 size={13} strokeWidth={1.7} className="tree-icon" />
                  <span className="row-name mono">{proj.display_name}</span>
                  {live > 0 && (
                    <span className="count tnum" title={`${live} agent(s) live`}>
                      {live}
                    </span>
                  )}
                  {onForgetProject && (
                    <button
                      className="row-action"
                      onClick={(e) => {
                        e.stopPropagation();
                        onForgetProject(proj);
                      }}
                      aria-label={`Forget ${proj.display_name}`}
                      title="Forget this project"
                      tabIndex={-1}
                    >
                      <Trash2 size={12} strokeWidth={1.8} />
                    </button>
                  )}
                </div>

                {isOpen && (
                  <div className="tree-children">
                    {proj.agents.length === 0 ? (
                      <div className="tree-empty">no agents</div>
                    ) : (
                      proj.agents.map((a) => (
                        <div
                          key={a.id}
                          className="row-item tree-agent"
                          onClick={() => {
                            if (!isActive) onSelectProject(proj.id);
                            go("agents");
                          }}
                          role="button"
                          tabIndex={0}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              if (!isActive) onSelectProject(proj.id);
                              go("agents");
                            }
                          }}
                          title={
                            `${a.name} — ${a.tool} (${a.status})` +
                            (a.held_files ? ` · ${a.held_files} file(s) locked` : "") +
                            (a.pending_messages
                              ? ` · ${a.pending_messages} message(s) waiting for its next turn`
                              : "")
                          }
                        >
                          <span
                            className={`status-dot ${a.status === "active" ? "pulse" : ""}`}
                            style={{ background: dotColor(a.status) }}
                          />
                          <span
                            className="row-name mono"
                            style={{ color: agentColor(a.name) }}
                          >
                            {a.name}
                          </span>
                          {/* An agent only reads the room when it next takes a
                              turn, so unread traffic is a prompt-me signal. */}
                          {a.pending_messages > 0 && (
                            <span
                              className={`count tnum ${a.pending_mentions ? "alert" : ""}`}
                              title={`${a.pending_messages} unread — prompt this session to deliver`}
                            >
                              <MessagesSquare size={10} strokeWidth={2} />
                              {a.pending_messages}
                            </span>
                          )}
                          {a.held_files > 0 && (
                            <span className="count tnum" title={`${a.held_files} locked`}>
                              <Lock size={10} strokeWidth={2} />
                              {a.held_files}
                            </span>
                          )}
                        </div>
                      ))
                    )}
                  </div>
                )}
              </div>
            );
          })
        )}
      </div>

      {/* Docs sit below the tree because they are not scoped to a project. */}
      <div className="sidebar-foot">
        <div
          className={`nav-item ${page === "docs" ? "active" : ""}`}
          style={{ flex: 1 }}
          onClick={() => go("docs")}
          role="button"
          tabIndex={0}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              go("docs");
            }
          }}
        >
          <BookOpen size={15} strokeWidth={1.6} />
          Docs
        </div>
      </div>
    </aside>
  );
}
