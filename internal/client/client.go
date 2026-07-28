// Package client is the HTTP client used by the CLI, hook, and MCP modes to
// talk to the daemon.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/enowdev/succubus/internal/store"
)

type Client struct {
	Base string
	HTTP *http.Client
}

func New(addr string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	return &Client{
		Base: "http://" + addr,
		HTTP: &http.Client{Timeout: timeout},
	}
}

// ErrDaemonDown signals that the daemon is unreachable. Callers must degrade
// gracefully: succubus never blocks an agent because its own daemon is missing.
type ErrDaemonDown struct{ Err error }

func (e *ErrDaemonDown) Error() string { return "succubus daemon unreachable: " + e.Err.Error() }
func (e *ErrDaemonDown) Unwrap() error { return e.Err }

func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &ErrDaemonDown{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Health() error { return c.do("GET", "/api/health", nil, nil) }

func (c *Client) ResolveProject(cwd string) (*store.Project, error) {
	var p store.Project
	err := c.do("POST", "/api/projects/resolve", map[string]string{"cwd": cwd}, &p)
	return &p, err
}

// DeleteProject forgets a project. Files in the repository are untouched.
func (c *Client) DeleteProject(id string) error {
	return c.do("DELETE", "/api/projects/"+id, nil, nil)
}

func (c *Client) ListProjects() ([]store.Project, error) {
	var out []store.Project
	err := c.do("GET", "/api/projects", nil, &out)
	return out, err
}

type RegisterResult struct {
	Agent   *store.Agent `json:"agent"`
	Reused  bool         `json:"reused"`
	Adopted string       `json:"adopted"`
}

func (c *Client) Register(projectID, preferredName, tool, sessionKey, cwd string, pid int) (*RegisterResult, error) {
	var out RegisterResult
	err := c.do("POST", "/api/projects/"+projectID+"/agents/register", map[string]any{
		"preferred_name": preferredName, "tool": tool,
		"session_key": sessionKey, "cwd": cwd, "pid": pid,
	}, &out)
	return &out, err
}

func (c *Client) Heartbeat(agentID string) error {
	return c.do("POST", "/api/agents/"+agentID+"/heartbeat", nil, nil)
}

func (c *Client) Unregister(agentID string) error {
	return c.do("DELETE", "/api/agents/"+agentID, nil, nil)
}

func (c *Client) RenameAgent(agentID, name string) (*store.Agent, error) {
	var a store.Agent
	err := c.do("POST", "/api/agents/"+agentID+"/rename", map[string]string{"name": name}, &a)
	return &a, err
}

func (c *Client) ListAgents(projectID string) ([]store.Agent, error) {
	var out []store.Agent
	err := c.do("GET", "/api/projects/"+projectID+"/agents", nil, &out)
	return out, err
}

// Context mirrors httpapi.ContextPayload without importing it, keeping the
// client free of a dependency on the server package.
type Context struct {
	Project    *store.Project   `json:"project"`
	Me         *store.Agent     `json:"me"`
	Agents     []store.Agent    `json:"agents"`
	ActivePlan *store.Plan      `json:"active_plan"`
	MyTasks    []store.Task     `json:"my_tasks"`
	OpenTasks  []store.Task     `json:"open_tasks"`
	Claims     []store.Claim    `json:"claims"`
	Handoffs   []store.Decision `json:"handoffs"`
	// Room traffic this agent has not been shown yet. RoomMentions entries
	// carry DirectMention when the agent was named specifically.
	RoomUnread    []store.Message `json:"room_unread"`
	RoomMentions  []store.Message `json:"room_mentions"`
	OpenQuestions []store.Message `json:"open_questions"`
	Text          string          `json:"text"`
}

func (c *Client) Context(projectID, agentID string) (*Context, error) {
	var out Context
	q := ""
	if agentID != "" {
		q = "?agent_id=" + url.QueryEscape(agentID)
	}
	err := c.do("GET", "/api/projects/"+projectID+"/context"+q, nil, &out)
	return &out, err
}

// ---- plans -----------------------------------------------------------------

func (c *Client) ListPlans(projectID string) ([]store.Plan, error) {
	var out []store.Plan
	err := c.do("GET", "/api/projects/"+projectID+"/plans", nil, &out)
	return out, err
}

func (c *Client) GetPlan(id string) (*store.Plan, error) {
	var p store.Plan
	err := c.do("GET", "/api/plans/"+id, nil, &p)
	return &p, err
}

func (c *Client) CreatePlan(projectID, title, body, status, createdBy string) (*store.Plan, error) {
	var p store.Plan
	err := c.do("POST", "/api/projects/"+projectID+"/plans", map[string]string{
		"title": title, "body_md": body, "status": status, "created_by": createdBy,
	}, &p)
	return &p, err
}

func (c *Client) UpdatePlan(id string, patch store.PlanPatch) (*store.Plan, error) {
	var p store.Plan
	err := c.do("PATCH", "/api/plans/"+id, patch, &p)
	return &p, err
}

func (c *Client) DeletePlan(id string) error {
	return c.do("DELETE", "/api/plans/"+id, nil, nil)
}

// ---- tasks -----------------------------------------------------------------

func (c *Client) ListTasks(projectID string, filter map[string]string) ([]store.Task, error) {
	q := url.Values{}
	for k, v := range filter {
		if v != "" {
			q.Set(k, v)
		}
	}
	p := "/api/projects/" + projectID + "/tasks"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	var out []store.Task
	err := c.do("GET", p, nil, &out)
	return out, err
}

func (c *Client) GetTask(id string) (*store.Task, error) {
	var t store.Task
	err := c.do("GET", "/api/tasks/"+id, nil, &t)
	return &t, err
}

type NewTask struct {
	PlanID          string   `json:"plan_id,omitempty"`
	Title           string   `json:"title"`
	BodyMD          string   `json:"body_md,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        int      `json:"priority,omitempty"`
	AssigneeAgentID string   `json:"assignee_agent_id,omitempty"`
	AssigneeName    string   `json:"assignee_name,omitempty"`
	DependsOn       []string `json:"depends_on,omitempty"`
}

func (c *Client) CreateTask(projectID string, t NewTask) (*store.Task, error) {
	var out store.Task
	err := c.do("POST", "/api/projects/"+projectID+"/tasks", t, &out)
	return &out, err
}

func (c *Client) UpdateTask(id string, patch store.TaskPatch) (*store.Task, error) {
	var out store.Task
	err := c.do("PATCH", "/api/tasks/"+id, patch, &out)
	return &out, err
}

func (c *Client) DeleteTask(id string) error {
	return c.do("DELETE", "/api/tasks/"+id, nil, nil)
}

func (c *Client) AddDep(taskID, dependsOn string) (*store.Task, error) {
	var out store.Task
	err := c.do("POST", "/api/tasks/"+taskID+"/deps", map[string]string{"depends_on": dependsOn}, &out)
	return &out, err
}

func (c *Client) RemoveDep(taskID, depID string) (*store.Task, error) {
	var out store.Task
	err := c.do("DELETE", "/api/tasks/"+taskID+"/deps/"+depID, nil, &out)
	return &out, err
}

func (c *Client) ClaimTask(taskID, agentID, agentName string, force bool) (*store.Task, error) {
	var out store.Task
	err := c.do("POST", "/api/tasks/"+taskID+"/claim", map[string]any{
		"agent_id": agentID, "agent_name": agentName, "force": force,
	}, &out)
	return &out, err
}

func (c *Client) ReorderTask(taskID, status string, index int) (*store.Task, error) {
	var out store.Task
	err := c.do("POST", "/api/tasks/"+taskID+"/reorder", map[string]any{
		"status": status, "index": index,
	}, &out)
	return &out, err
}

// ---- claims ----------------------------------------------------------------

type ClaimResponse struct {
	Granted bool                `json:"granted"`
	Results []store.ClaimResult `json:"results"`
}

func (c *Client) ClaimFiles(projectID, agentID, agentName, taskID string, paths []string, ttlSec int64) (*ClaimResponse, error) {
	var out ClaimResponse
	err := c.do("POST", "/api/projects/"+projectID+"/claims", map[string]any{
		"agent_id": agentID, "agent_name": agentName, "task_id": taskID,
		"paths": paths, "ttl_sec": ttlSec,
	}, &out)
	return &out, err
}

func (c *Client) ReleaseFiles(projectID, agentID string, paths []string, all bool) (int64, error) {
	var out struct {
		Released int64 `json:"released"`
	}
	err := c.do("POST", "/api/projects/"+projectID+"/claims/release", map[string]any{
		"agent_id": agentID, "paths": paths, "all": all,
	}, &out)
	return out.Released, err
}

func (c *Client) RenewClaims(projectID, agentID string, ttlSec int64) error {
	return c.do("POST", "/api/projects/"+projectID+"/claims/renew", map[string]any{
		"agent_id": agentID, "ttl_sec": ttlSec,
	}, nil)
}

type CheckResponse struct {
	Conflict bool                `json:"conflict"`
	Holder   string              `json:"holder"`
	Results  []store.ClaimResult `json:"results"`
}

func (c *Client) CheckFiles(projectID, agentID string, paths []string) (*CheckResponse, error) {
	var out CheckResponse
	err := c.do("POST", "/api/projects/"+projectID+"/claims/check", map[string]any{
		"agent_id": agentID, "paths": paths,
	}, &out)
	return &out, err
}

func (c *Client) ListClaims(projectID string) ([]store.Claim, error) {
	var out []store.Claim
	err := c.do("GET", "/api/projects/"+projectID+"/claims", nil, &out)
	return out, err
}

// ---- decisions & events ----------------------------------------------------

func (c *Client) CreateDecision(projectID, kind, title, body, authorID, authorName, target string) (*store.Decision, error) {
	var out store.Decision
	err := c.do("POST", "/api/projects/"+projectID+"/decisions", map[string]string{
		"kind": kind, "title": title, "body_md": body,
		"author_agent_id": authorID, "author_name": authorName, "target_agent_name": target,
	}, &out)
	return &out, err
}

func (c *Client) ListDecisions(projectID, target string, unackedOnly bool, limit int) ([]store.Decision, error) {
	q := url.Values{}
	if target != "" {
		q.Set("target", target)
	}
	if unackedOnly {
		q.Set("unacked", "1")
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	p := "/api/projects/" + projectID + "/decisions"
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	var out []store.Decision
	err := c.do("GET", p, nil, &out)
	return out, err
}

func (c *Client) AckDecision(id string) error {
	return c.do("POST", "/api/decisions/"+id+"/ack", nil, nil)
}

// ---- agent room ------------------------------------------------------------

type RoomPayload struct {
	Messages      []store.Message `json:"messages"`
	OpenQuestions []store.Message `json:"open_questions"`
	Total         int             `json:"total"`
	Open          int             `json:"open"`
}

func (c *Client) Room(projectID string, limit int) (*RoomPayload, error) {
	var out RoomPayload
	err := c.do("GET", fmt.Sprintf("/api/projects/%s/room?limit=%d", projectID, limit), nil, &out)
	return &out, err
}

func (c *Client) PostMessage(projectID, parentID, kind, authorID, authorName, body string, mentions []string) (*store.Message, error) {
	var out store.Message
	err := c.do("POST", "/api/projects/"+projectID+"/room", map[string]any{
		"parent_id": parentID, "kind": kind, "author_id": authorID,
		"author_name": authorName, "body_md": body, "mentions": mentions,
	}, &out)
	return &out, err
}

func (c *Client) ResolveQuestion(id, by string) error {
	return c.do("POST", "/api/room/"+id+"/resolve", map[string]string{"by": by}, nil)
}

// MarkRoomRead records that this agent has been shown the room up to now.
func (c *Client) MarkRoomRead(projectID, agentName string) error {
	if agentName == "" {
		return nil
	}
	return c.do("POST", "/api/projects/"+projectID+"/room/read",
		map[string]string{"agent_name": agentName}, nil)
}

func (c *Client) RecentEvents(projectID string, limit int) ([]store.Event, error) {
	var out []store.Event
	err := c.do("GET", fmt.Sprintf("/api/projects/%s/events?recent=1&limit=%d", projectID, limit), nil, &out)
	return out, err
}
