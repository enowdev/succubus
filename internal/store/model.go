package store

// Task / claim / agent status vocabularies. Kept as plain string constants so
// they serialize directly to JSON and SQLite without conversion helpers.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusBlocked    = "blocked"
	StatusReview     = "review"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// BoardColumns is the canonical left-to-right order of the kanban board.
var BoardColumns = []string{
	StatusTodo, StatusInProgress, StatusBlocked, StatusReview, StatusDone,
}

func ValidTaskStatus(s string) bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusBlocked, StatusReview, StatusDone, StatusCancelled:
		return true
	}
	return false
}

const (
	AgentActive = "active"
	AgentIdle   = "idle"
	AgentDead   = "dead"
)

type Project struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	RootPath    string `json:"root_path"`
	GitRemote   string `json:"git_remote,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	LastSeenAt  int64  `json:"last_seen_at"`
}

type Agent struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	Name            string `json:"name"`
	Tool            string `json:"tool"`
	SessionKey      string `json:"session_key"`
	PID             int    `json:"pid,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	Status          string `json:"status"`
	RegisteredAt    int64  `json:"registered_at"`
	LastHeartbeatAt int64  `json:"last_heartbeat_at"`
}

type Plan struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	BodyMD    string `json:"body_md"`
	Status    string `json:"status"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Task struct {
	ID              string   `json:"id"`
	ProjectID       string   `json:"project_id"`
	PlanID          string   `json:"plan_id,omitempty"`
	Title           string   `json:"title"`
	BodyMD          string   `json:"body_md"`
	Status          string   `json:"status"`
	Priority        int      `json:"priority"`
	SortKey         float64  `json:"sort_key"`
	AssigneeAgentID string   `json:"assignee_agent_id,omitempty"`
	AssigneeName    string   `json:"assignee_name,omitempty"`
	CreatedAt       int64    `json:"created_at"`
	UpdatedAt       int64    `json:"updated_at"`
	DoneAt          int64    `json:"done_at,omitempty"`
	DependsOn       []string `json:"depends_on"`
	// Blocked is derived, not stored: true when any dependency is unfinished.
	Blocked bool `json:"blocked"`
}

type Claim struct {
	ProjectID  string `json:"project_id"`
	Path       string `json:"path"`
	AgentID    string `json:"agent_id"`
	AgentName  string `json:"agent_name"`
	TaskID     string `json:"task_id,omitempty"`
	Mode       string `json:"mode"`
	ClaimedAt  int64  `json:"claimed_at"`
	ExpiresAt  int64  `json:"expires_at"`
	ReleasedAt int64  `json:"released_at,omitempty"`
}

// ClaimResult is the per-path outcome of a claim attempt.
type ClaimResult struct {
	Path    string `json:"path"`
	Granted bool   `json:"granted"`
	// Holder is set only when Granted is false.
	Holder    string `json:"holder,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Event struct {
	ID        int64  `json:"id"`
	ProjectID string `json:"project_id"`
	Type      string `json:"type"`
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	SubjectID string `json:"subject_id,omitempty"`
	Payload   any    `json:"payload,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type Decision struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	BodyMD          string `json:"body_md"`
	AuthorAgentID   string `json:"author_agent_id,omitempty"`
	AuthorName      string `json:"author_name,omitempty"`
	TargetAgentName string `json:"target_agent_name,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	AckAt           int64  `json:"ack_at,omitempty"`
}

// Event type constants — these are the SSE event names the frontend listens on.
const (
	EvAgentRegistered = "agent.registered"
	EvAgentLeft       = "agent.left"
	EvAgentHeartbeat  = "agent.heartbeat"
	EvPlanCreated     = "plan.created"
	EvPlanUpdated     = "plan.updated"
	EvPlanDeleted     = "plan.deleted"
	EvTaskCreated     = "task.created"
	EvTaskUpdated     = "task.updated"
	EvTaskDeleted     = "task.deleted"
	EvTaskMoved       = "task.moved"
	EvClaimGranted    = "claim.granted"
	EvClaimDenied     = "claim.denied"
	EvClaimReleased   = "claim.released"
	EvClaimExpired    = "claim.expired"
	EvDecisionCreated = "decision.created"
	EvHandoff         = "handoff"
	EvReport          = "report"
	EvRoomMessage     = "room.message"
	EvRoomResolved    = "room.resolved"
)
