// Mirrors internal/store/model.go. Kept hand-written rather than generated so
// the shapes stay readable at the call site.

export type TaskStatus =
  | "todo"
  | "in_progress"
  | "blocked"
  | "review"
  | "done"
  | "cancelled";

export const BOARD_COLUMNS: TaskStatus[] = [
  "todo",
  "in_progress",
  "blocked",
  "review",
  "done",
];

export const STATUS_LABEL: Record<TaskStatus, string> = {
  todo: "To do",
  in_progress: "In progress",
  blocked: "Blocked",
  review: "Review",
  done: "Done",
  cancelled: "Cancelled",
};

export type AgentStatus = "active" | "idle" | "dead";

export interface Project {
  id: string;
  display_name: string;
  root_path: string;
  git_remote?: string;
  created_at: number;
  last_seen_at: number;
}

export interface Agent {
  id: string;
  project_id: string;
  name: string;
  tool: string;
  session_key: string;
  pid?: number;
  cwd?: string;
  status: AgentStatus;
  registered_at: number;
  last_heartbeat_at: number;
}

export interface Plan {
  id: string;
  project_id: string;
  title: string;
  body_md: string;
  status: string;
  created_by?: string;
  created_at: number;
  updated_at: number;
}

export interface Task {
  id: string;
  project_id: string;
  plan_id?: string;
  title: string;
  body_md: string;
  status: TaskStatus;
  priority: number;
  sort_key: number;
  assignee_agent_id?: string;
  assignee_name?: string;
  created_at: number;
  updated_at: number;
  done_at?: number;
  depends_on: string[];
  blocked: boolean;
}

export interface Claim {
  project_id: string;
  path: string;
  agent_id: string;
  agent_name: string;
  task_id?: string;
  mode: string;
  claimed_at: number;
  expires_at: number;
}

export interface Decision {
  id: string;
  project_id: string;
  kind: "decision" | "note" | "handoff";
  title: string;
  body_md: string;
  author_agent_id?: string;
  author_name?: string;
  target_agent_name?: string;
  created_at: number;
  ack_at?: number;
}

export type MessageKind = "message" | "question" | "answer" | "announce";

/** One post in the agent room. Replies are nested one level deep. */
export interface Message {
  id: string;
  project_id: string;
  parent_id?: string;
  kind: MessageKind;
  author_id?: string;
  author_name: string;
  mentions: string[];
  body_md: string;
  resolved_at?: number;
  resolved_by?: string;
  created_at: number;
  replies?: Message[];
}

export interface RoomPayload {
  messages: Message[];
  open_questions: Message[];
  total: number;
  open: number;
}

/** One documentation page, from /api/docs. */
export interface DocSection {
  id: string;
  title: string;
  summary: string;
}

/** An agent with what it currently holds, as returned by /api/overview. */
export interface AgentSummary extends Agent {
  held_files: number;
  open_tasks: number;
  /** Room messages waiting for this agent's next turn. */
  pending_messages: number;
  pending_mentions: number;
}

/** One node of the sidebar tree: a project and the agents working in it. */
export interface ProjectSummary extends Project {
  agents: AgentSummary[];
  open_tasks: number;
  claims: number;
}

export interface EventRecord {
  id: number;
  project_id: string;
  type: string;
  agent_id?: string;
  agent_name?: string;
  subject_id?: string;
  payload?: Record<string, unknown>;
  created_at: number;
}
