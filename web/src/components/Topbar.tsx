import { Menu, Moon, RefreshCw, Sun } from "lucide-react";
import type { Theme } from "../lib/useTheme";
import type { StreamStatus } from "../lib/sse";
import type { Page } from "./Sidebar";

const PAGE_LABEL: Record<Page, string> = {
  overview: "Overview",
  board: "Board",
  plans: "Plans",
  agents: "Agents",
  claims: "Claims",
  room: "Agent room",
  notes: "Notes",
  activity: "Activity",
  docs: "Docs",
};

interface Props {
  page: Page;
  projectName: string | null;
  theme: Theme;
  onToggleTheme: () => void;
  onOpenMenu: () => void;
  onRefresh: () => void;
  streamStatus: StreamStatus;
}

export function Topbar({
  page, projectName, theme, onToggleTheme, onOpenMenu, onRefresh, streamStatus,
}: Props) {
  const live = streamStatus === "open";
  const dot = live
    ? "var(--good)"
    : streamStatus === "connecting"
      ? "var(--warn)"
      : "var(--crit)";

  return (
    <div className="topbar">
      <button className="icon-btn menu-btn" onClick={onOpenMenu} aria-label="Open menu">
        <Menu size={15} strokeWidth={1.7} />
      </button>

      {/* Docs are not scoped to a project, so the crumb drops the project. */}
      <div className="crumb">
        {page === "docs" ? (
          <b>Documentation</b>
        ) : (
          <>
            <span className="hide-sm">Projects</span>
            <span className="sep hide-sm">/</span>
            <b className="mono">{projectName || "—"}</b>
            <span className="sep">/</span>
            <span>{PAGE_LABEL[page]}</span>
          </>
        )}
      </div>

      <div className="topbar-spacer" />

      {/* Connection state is a single dot: it only needs to be noticed when it
          is not green, and the title carries the detail. */}
      <span
        className={`stream-dot ${live ? "pulse" : ""}`}
        style={{ background: dot }}
        title={`Event stream: ${streamStatus}`}
        role="status"
        aria-label={`Event stream ${streamStatus}`}
      />

      <button className="icon-btn" onClick={onRefresh} aria-label="Refresh" title="Refresh">
        <RefreshCw size={15} strokeWidth={1.7} />
      </button>

      <button className="icon-btn" onClick={onToggleTheme} aria-label="Toggle theme" title="Toggle theme">
        {theme === "dark" ? <Sun size={15} strokeWidth={1.7} /> : <Moon size={15} strokeWidth={1.7} />}
      </button>
    </div>
  );
}
