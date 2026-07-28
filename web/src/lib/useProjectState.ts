import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "./api";
import type {
  Agent,
  Claim,
  Decision,
  EventRecord,
  Plan,
  Project,
  Task,
} from "./types";
import { useEventStream } from "./sse";

export interface ProjectState {
  project: Project | null;
  agents: Agent[];
  plans: Plan[];
  tasks: Task[];
  claims: Claim[];
  decisions: Decision[];
  events: EventRecord[];
  /** Unanswered questions in the agent room, for the sidebar badge. */
  openQuestions: number;
  loading: boolean;
  error: string | null;
  streamStatus: ReturnType<typeof useEventStream>["status"];
  /** Type of the most recent event, so the shell can react to agent churn. */
  lastEventType: string | null;
  refresh: () => Promise<void>;
  // Optimistic helpers keep the board responsive; the SSE echo reconciles.
  patchTask: (id: string, patch: Partial<Task>) => void;
  removeTaskLocal: (id: string) => void;
}

/**
 * Loads a project's full state once, then keeps it current from the event
 * stream. Every mutation elsewhere in the app goes through the API and comes
 * back as an event, so this is the single place state is reconciled.
 */
export function useProjectState(projectId: string | undefined): ProjectState {
  const [project, setProject] = useState<Project | null>(null);
  const [agents, setAgents] = useState<Agent[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [claims, setClaims] = useState<Claim[]>([]);
  const [decisions, setDecisions] = useState<Decision[]>([]);
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [openQuestions, setOpenQuestions] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Coalesce bursts of events into one refetch rather than one per event.
  const pending = useRef<Set<string>>(new Set());
  const timer = useRef<number | null>(null);

  const load = useCallback(async () => {
    if (!projectId) return;
    try {
      const [p, a, pl, t, c, d, e, room] = await Promise.all([
        api.getProject(projectId),
        api.listAgents(projectId),
        api.listPlans(projectId),
        api.listTasks(projectId),
        api.listClaims(projectId),
        api.listDecisions(projectId),
        api.recentEvents(projectId, 80),
        // Only the counts are needed here; the Room page fetches the thread.
        api.room(projectId, 1).catch(() => null),
      ]);
      setProject(p);
      setAgents(a);
      setPlans(pl);
      setTasks(t);
      setClaims(c);
      setDecisions(d);
      setEvents(e);
      setOpenQuestions(room?.open ?? 0);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useEffect(() => {
    setLoading(true);
    load();
  }, [load]);

  const scheduleRefresh = useCallback(
    (kind: string) => {
      pending.current.add(kind);
      if (timer.current !== null) return;
      timer.current = window.setTimeout(async () => {
        const kinds = new Set(pending.current);
        pending.current.clear();
        timer.current = null;
        if (!projectId) return;

        // Refetch only the slices the events touched. `kinds` holds the prefix
        // of each event type — "task" for task.created, and the whole name for
        // the ones with no dot, like "handoff" and "report".
        const jobs: Promise<void>[] = [];
        if (kinds.has("agent"))
          jobs.push(api.listAgents(projectId).then(setAgents).catch(() => {}));
        if (kinds.has("task") || kinds.has("report"))
          jobs.push(api.listTasks(projectId).then(setTasks).catch(() => {}));
        if (kinds.has("plan"))
          jobs.push(api.listPlans(projectId).then(setPlans).catch(() => {}));
        // Claims move when an agent leaves or a task finishes, not only on
        // claim.* events.
        if (kinds.has("claim") || kinds.has("agent") || kinds.has("task"))
          jobs.push(api.listClaims(projectId).then(setClaims).catch(() => {}));
        if (kinds.has("decision") || kinds.has("handoff") || kinds.has("report"))
          jobs.push(api.listDecisions(projectId).then(setDecisions).catch(() => {}));
        if (kinds.has("room"))
          jobs.push(
            api
              .room(projectId, 1)
              .then((r) => setOpenQuestions(r.open))
              .catch(() => {}),
          );
        await Promise.all(jobs);
      }, 120);
    },
    [projectId],
  );

  const [lastEventType, setLastEventType] = useState<string | null>(null);

  const onEvent = useCallback(
    (ev: EventRecord) => {
      setEvents((prev) => {
        if (prev.some((p) => p.id === ev.id)) return prev;
        return [ev, ...prev].slice(0, 200);
      });
      setLastEventType(ev.type + ":" + ev.id); // id suffix so repeats still fire
      scheduleRefresh(ev.type.split(".")[0]);
    },
    [scheduleRefresh],
  );

  const { status: streamStatus } = useEventStream(projectId, { onEvent });

  // A stream that reconnects may have missed events; resync on recovery.
  const prevStatus = useRef(streamStatus);
  useEffect(() => {
    if (prevStatus.current !== "open" && streamStatus === "open") load();
    prevStatus.current = streamStatus;
  }, [streamStatus, load]);

  const patchTask = useCallback((id: string, patch: Partial<Task>) => {
    setTasks((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }, []);

  const removeTaskLocal = useCallback((id: string) => {
    setTasks((prev) => prev.filter((t) => t.id !== id));
  }, []);

  return useMemo(
    () => ({
      project, agents, plans, tasks, claims, decisions, events, openQuestions,
      loading, error, streamStatus, lastEventType,
      refresh: load, patchTask, removeTaskLocal,
    }),
    [project, agents, plans, tasks, claims, decisions, events, openQuestions,
     loading, error, streamStatus, lastEventType, load, patchTask, removeTaskLocal],
  );
}
