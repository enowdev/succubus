/** Deterministic colour per agent name, so ORION looks the same everywhere. */
const AGENT_COLORS = [
  "#7c5cff", "#0ea5e9", "#10b981", "#f59e0b", "#f43f5e", "#06b6d4",
  "#d946ef", "#84cc16", "#f97316", "#14b8a6", "#6366f1", "#ec4899",
];

export function agentColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return AGENT_COLORS[h % AGENT_COLORS.length];
}

export function relTime(ms: number): string {
  if (!ms) return "—";
  const d = Date.now() - ms;
  if (d < 0) return "now";
  const s = Math.floor(d / 1000);
  if (s < 45) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function untilTime(ms: number): string {
  const d = ms - Date.now();
  if (d <= 0) return "expired";
  const s = Math.floor(d / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  return `${Math.floor(m / 60)}h`;
}

/** Trims a repo-relative path to its tail, keeping the filename legible. */
export function shortPath(p: string, max = 38): string {
  if (p.length <= max) return p;
  const parts = p.split("/");
  const file = parts[parts.length - 1];
  if (file.length >= max - 3) return "…" + file.slice(-(max - 1));
  let out = file;
  for (let i = parts.length - 2; i >= 0; i--) {
    const next = parts[i] + "/" + out;
    if (next.length > max - 1) return "…/" + out;
    out = next;
  }
  return out;
}
