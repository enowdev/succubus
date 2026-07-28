import { Lock, Unlock } from "lucide-react";
import { api } from "../lib/api";
import type { ProjectState } from "../lib/useProjectState";
import { AgentTag, Empty, PageHead, Panel, Skeleton } from "../components/ui";
import { useConfirm } from "../components/Confirm";
import { relTime, untilTime } from "../lib/format";

export function Claims({ state }: { state: ProjectState }) {
  const { project, claims, loading, refresh } = state;
  const confirm = useConfirm();

  async function release(agentId: string, path: string, holder: string) {
    if (!project) return;
    const ok = await confirm({
      title: "Force-release this file?",
      message: (
        <>
          <code className="mono">{path}</code> will be freed for other agents to
          claim. {holder} may still be editing it, which is exactly what this
          lock exists to prevent.
        </>
      ),
      confirmLabel: "Release",
      danger: true,
    });
    if (!ok) return;
    await api.releaseClaims(project.id, agentId, [path]);
    refresh();
  }

  return (
    <>
      <PageHead
        title="File claims"
        id={project?.display_name}
        actions={
          <span className="dim" style={{ fontSize: 12.5, alignSelf: "center" }}>
            {claims.length} held
          </span>
        }
      />

      <Panel flush>
        {loading ? (
          <div style={{ padding: 15 }}>
            <Skeleton rows={4} />
          </div>
        ) : claims.length === 0 ? (
          <Empty
            icon={<Lock size={28} strokeWidth={1.5} />}
            title="No files locked"
            desc="Claims appear here when an agent calls succubus_claim_files before editing."
          />
        ) : (
          claims.map((c) => (
            <div key={c.path} className="list-row">
              <div className="grow">
                <code className="path" title={c.path}>
                  {c.path}
                </code>
                <span className="dim" style={{ fontSize: 11.5 }}>
                  claimed {relTime(c.claimed_at)} · expires in {untilTime(c.expires_at)}
                </span>
              </div>
              <AgentTag name={c.agent_name} size="sm" />
              <button
                className="btn ghost sm"
                onClick={() => release(c.agent_id, c.path, c.agent_name)}
                aria-label={`Release ${c.path}`}
                title="Force release"
              >
                <Unlock size={14} strokeWidth={1.7} />
              </button>
            </div>
          ))
        )}
      </Panel>

      <p className="dim" style={{ fontSize: 11.5, lineHeight: 1.55 }}>
        Leases expire on their own and a dead agent's claims are ignored, so a crashed
        session never blocks a file permanently.
      </p>
    </>
  );
}
