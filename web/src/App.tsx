import { useCallback, useEffect, useState } from "react";
import { useConfirm } from "./components/Confirm";
import { Sidebar, type Page } from "./components/Sidebar";
import { Topbar } from "./components/Topbar";
import { useTheme } from "./lib/useTheme";
import { useProjectState } from "./lib/useProjectState";
import { api } from "./lib/api";
import type { ProjectSummary } from "./lib/types";
import { Overview } from "./pages/Overview";
import { Board } from "./pages/Board";
import { Plans } from "./pages/Plans";
import { Agents } from "./pages/Agents";
import { Claims } from "./pages/Claims";
import { Notes } from "./pages/Notes";
import { ActivityPage } from "./pages/Activity";
import { Room } from "./pages/Room";
import { Docs } from "./pages/Docs";

type Boot = "checking" | "empty" | "ready" | "offline";

export default function App() {
  const { theme, toggleTheme } = useTheme();
  const confirm = useConfirm();
  const [boot, setBoot] = useState<Boot>("checking");
  const [tree, setTree] = useState<ProjectSummary[]>([]);
  const [activeProject, setActiveProject] = useState<string | null>(null);
  const [page, setPage] = useState<Page>("overview");
  const [menuOpen, setMenuOpen] = useState(false);

  // The tree is owned here rather than in the sidebar: the shell needs it to
  // decide what to render before any page mounts. One request covers every
  // project and its agents.
  const loadTree = useCallback(async () => {
    try {
      const list = await api.overview();
      setTree(list);
      setActiveProject((cur) => cur ?? (list.length > 0 ? list[0].id : null));
      setBoot(list.length === 0 ? "empty" : "ready");
    } catch {
      setBoot("offline");
    }
  }, []);

  useEffect(() => {
    loadTree();
  }, [loadTree]);

  const state = useProjectState(activeProject ?? undefined);

  // The tree shows live agents and per-project counts, so anything that moves
  // those numbers has to refresh it — not just agent events.
  useEffect(() => {
    if (!state.lastEventType) return;
    const kind = state.lastEventType.split(".")[0];
    if (kind === "agent" || kind === "claim" || kind === "task") loadTree();
  }, [state.lastEventType, loadTree]);

  const selectProject = useCallback((id: string) => {
    setActiveProject(id);
    setPage("overview");
  }, []);

  const refreshAll = useCallback(() => {
    loadTree();
    state.refresh();
  }, [loadTree, state]);

  const forgetProject = useCallback(
    async (p: ProjectSummary) => {
      const ok = await confirm({
        title: `Forget ${p.display_name}?`,
        message: (
          <>
            Its agents, plans, tasks, claims, and room history are deleted from
            succubus. <b>Files in the repository are untouched</b> — the project
            re-registers itself the next time an agent runs there.
          </>
        ),
        confirmLabel: "Forget",
        danger: true,
      });
      if (!ok) return;
      await api.deleteProject(p.id);
      // Fall back to whatever project is left, if we just deleted the open one.
      setActiveProject((cur) => (cur === p.id ? null : cur));
      loadTree();
    },
    [confirm, loadTree],
  );

  if (boot === "checking") {
    return (
      <div className="app-loading">
        <img className="brand-mark" src="/icon-192.png" alt="" aria-hidden="true" />
        Loading…
      </div>
    );
  }

  if (boot === "offline") {
    return (
      <div className="app-loading" style={{ flexDirection: "column", gap: 14 }}>
        <img
          className="brand-mark"
          src="/icon-192.png"
          alt=""
          aria-hidden="true"
          style={{ width: 40, height: 40 }}
        />
        <div style={{ textAlign: "center", lineHeight: 1.6 }}>
          <p style={{ color: "var(--text)", margin: 0, fontWeight: 500 }}>
            Cannot reach the succubus daemon
          </p>
          <p style={{ margin: "4px 0 0" }}>
            Start it with <code className="mono">succubus daemon</code>, then reload.
          </p>
        </div>
        <button className="btn" onClick={loadTree}>
          Retry
        </button>
      </div>
    );
  }

  if (boot === "empty") {
    return (
      <div className="app-loading" style={{ flexDirection: "column", gap: 14 }}>
        <img
          className="brand-mark"
          src="/icon-192.png"
          alt=""
          aria-hidden="true"
          style={{ width: 40, height: 40 }}
        />
        <div style={{ textAlign: "center", lineHeight: 1.6, maxWidth: "44ch" }}>
          <p style={{ color: "var(--text)", margin: 0, fontWeight: 500 }}>
            No projects registered yet
          </p>
          <p style={{ margin: "4px 0 0" }}>
            Open an agent session in a repository, or run{" "}
            <code className="mono">succubus status</code> there. succubus registers the
            project on first contact.
          </p>
        </div>
        <button className="btn" onClick={loadTree}>
          Check again
        </button>
      </div>
    );
  }

  const projectName =
    tree.find((p) => p.id === activeProject)?.display_name ?? null;

  return (
    <div className="app">
      {menuOpen && (
        <div className="sidebar-scrim" onClick={() => setMenuOpen(false)} aria-hidden />
      )}

      <Sidebar
        page={page}
        onNavigate={setPage}
        tree={tree}
        activeProject={activeProject}
        onSelectProject={selectProject}
        openQuestions={state.openQuestions}
        onForgetProject={forgetProject}
        open={menuOpen}
        onClose={() => setMenuOpen(false)}
      />

      <div className="main">
        <Topbar
          page={page}
          projectName={projectName}
          theme={theme}
          onToggleTheme={toggleTheme}
          onOpenMenu={() => setMenuOpen(true)}
          onRefresh={refreshAll}
          streamStatus={state.streamStatus}
        />

        {/* Docs own their scrolling; every other page lets .content scroll. */}
        <div className={`content ${page === "docs" ? "content-docs" : ""}`}>
          {state.error && <div className="error-state">{state.error}</div>}

          {page === "overview" && <Overview state={state} onNavigate={setPage} />}
          {page === "board" && <Board state={state} />}
          {page === "plans" && <Plans state={state} />}
          {page === "agents" && <Agents state={state} />}
          {page === "claims" && <Claims state={state} />}
          {page === "notes" && <Notes state={state} />}
          {page === "room" && <Room state={state} />}
          {page === "activity" && <ActivityPage state={state} />}
          {page === "docs" && <Docs />}
        </div>
      </div>
    </div>
  );
}
