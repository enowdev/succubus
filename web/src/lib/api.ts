import type {
  Agent,
  Claim,
  Decision,
  DocSection,
  EventRecord,
  Message,
  MessageKind,
  Plan,
  Project,
  ProjectSummary,
  RoomPayload,
  Task,
  TaskStatus,
} from "./types";

// In dev, Vite proxies /api to the daemon; in production the Go binary serves
// this SPA itself. Either way the API is same-origin.
const BASE = "/api";

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: init?.body
      ? { "Content-Type": "application/json", ...init?.headers }
      : init?.headers,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      // non-JSON error body; the status text will do
    }
    throw new ApiError(msg, res.status);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

const post = <T>(p: string, body?: unknown) =>
  req<T>(p, { method: "POST", body: body ? JSON.stringify(body) : undefined });
const patch = <T>(p: string, body: unknown) =>
  req<T>(p, { method: "PATCH", body: JSON.stringify(body) });
const del = <T>(p: string) => req<T>(p, { method: "DELETE" });

export const api = {
  health: () => req<{ ok: boolean; db: string }>("/health"),

  listProjects: () => req<Project[]>("/projects"),
  /** Every project with its agents attached — powers the sidebar tree. */
  overview: () => req<ProjectSummary[]>("/overview"),
  getProject: (pid: string) => req<Project>(`/projects/${pid}`),
  /** Forgets a project's succubus record. Repository files are untouched. */
  deleteProject: (pid: string) => del<{ ok: boolean }>(`/projects/${pid}`),
  resolveProject: (cwd: string) => post<Project>("/projects/resolve", { cwd }),

  listAgents: (pid: string) => req<Agent[]>(`/projects/${pid}/agents`),
  renameAgent: (id: string, name: string) =>
    post<Agent>(`/agents/${id}/rename`, { name }),
  removeAgent: (id: string) => del<{ ok: boolean }>(`/agents/${id}`),

  listPlans: (pid: string) => req<Plan[]>(`/projects/${pid}/plans`),
  getPlan: (id: string) => req<Plan>(`/plans/${id}`),
  createPlan: (pid: string, body: { title: string; body_md?: string; status?: string }) =>
    post<Plan>(`/projects/${pid}/plans`, body),
  updatePlan: (
    id: string,
    body: Partial<{ title: string; body_md: string; status: string }>,
  ) => patch<Plan>(`/plans/${id}`, body),
  deletePlan: (id: string) => del<{ ok: boolean }>(`/plans/${id}`),

  listTasks: (pid: string, filter?: { status?: string; assignee?: string; plan_id?: string }) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(filter ?? {})) if (v) q.set(k, v);
    const qs = q.toString();
    return req<Task[]>(`/projects/${pid}/tasks${qs ? `?${qs}` : ""}`);
  },
  getTask: (id: string) => req<Task>(`/tasks/${id}`),
  createTask: (
    pid: string,
    body: {
      title: string;
      body_md?: string;
      status?: string;
      priority?: number;
      plan_id?: string;
      assignee_name?: string;
      depends_on?: string[];
    },
  ) => post<Task>(`/projects/${pid}/tasks`, body),
  updateTask: (
    id: string,
    body: Partial<{
      title: string;
      body_md: string;
      status: TaskStatus;
      priority: number;
      plan_id: string;
      assignee_name: string;
      assignee_agent_id: string;
      sort_key: number;
    }>,
  ) => patch<Task>(`/tasks/${id}`, body),
  deleteTask: (id: string) => del<{ ok: boolean }>(`/tasks/${id}`),
  reorderTask: (id: string, status: TaskStatus, index: number) =>
    post<Task>(`/tasks/${id}/reorder`, { status, index }),
  addDep: (id: string, dependsOn: string) =>
    post<Task>(`/tasks/${id}/deps`, { depends_on: dependsOn }),
  removeDep: (id: string, depId: string) => del<Task>(`/tasks/${id}/deps/${depId}`),

  listClaims: (pid: string) => req<Claim[]>(`/projects/${pid}/claims`),
  releaseClaims: (pid: string, agentId: string, paths?: string[], all = false) =>
    post<{ released: number }>(`/projects/${pid}/claims/release`, {
      agent_id: agentId,
      paths,
      all,
    }),

  listDecisions: (pid: string, limit = 100) =>
    req<Decision[]>(`/projects/${pid}/decisions?limit=${limit}`),
  createDecision: (
    pid: string,
    body: {
      kind?: string;
      title: string;
      body_md?: string;
      author_name?: string;
      target_agent_name?: string;
    },
  ) => post<Decision>(`/projects/${pid}/decisions`, body),
  ackDecision: (id: string) => post<{ ok: boolean }>(`/decisions/${id}/ack`),
  deleteDecision: (id: string) => del<{ ok: boolean }>(`/decisions/${id}`),

  recentEvents: (pid: string, limit = 100) =>
    req<EventRecord[]>(`/projects/${pid}/events?recent=1&limit=${limit}`),

  room: (pid: string, limit = 60) => req<RoomPayload>(`/projects/${pid}/room?limit=${limit}`),
  postMessage: (
    pid: string,
    body: {
      body_md: string;
      kind?: MessageKind;
      parent_id?: string;
      author_name?: string;
      mentions?: string[];
    },
  ) => post<Message>(`/projects/${pid}/room`, body),
  resolveQuestion: (id: string, by = "HUMAN") =>
    post<{ ok: boolean }>(`/room/${id}/resolve`, { by }),
  deleteMessage: (id: string) => del<{ ok: boolean }>(`/room/${id}`),

  docsList: () => req<DocSection[]>("/docs"),
  /** Returns raw markdown — the endpoint serves text/markdown, not JSON. */
  docsSection: async (id: string): Promise<string> => {
    const res = await fetch(`${BASE}/docs/${encodeURIComponent(id)}`);
    if (!res.ok) throw new ApiError(res.statusText, res.status);
    return res.text();
  },
};
