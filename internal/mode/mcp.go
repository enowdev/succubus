package mode

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/enowdev/succubus/assets"
	"github.com/enowdev/succubus/docs"
	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/store"
)

// MCP runs a Model Context Protocol server over stdio, implementing JSON-RPC
// 2.0 by hand so the binary stays dependency-free.
//
// Two rules govern this mode: stdout carries protocol frames only (every log
// goes to stderr), and no failure is ever fatal to the agent — if the daemon is
// down, tools return advisory text instead of errors.
func MCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	toolName := fs.String("tool", "", "agent tool name override")
	sessionKey := fs.String("session", "", "session key override")
	fs.Parse(args)

	if *toolName != "" {
		os.Setenv("SUCCUBUS_TOOL", *toolName)
	}
	if *sessionKey != "" {
		os.Setenv("SUCCUBUS_SESSION", *sessionKey)
	}

	srv := &mcpServer{out: bufio.NewWriter(os.Stdout)}
	srv.channel = newChannelPusher(srv)
	// Follows the daemon's event stream and pushes room messages addressed to
	// this agent straight into the live session. Harmless if the session was
	// started without channels enabled: the notifications are simply dropped.
	go srv.channel.Watch()

	rd := bufio.NewReaderSize(os.Stdin, 1<<20)

	for {
		line, err := rd.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			srv.handleLine(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type mcpServer struct {
	out  *bufio.Writer
	sess *Session
	// writeMu serialises stdout: channel notifications are pushed from a
	// background goroutine and must not interleave with a response frame.
	writeMu sync.Mutex
	// channel forwards room messages into a live session, when the session was
	// started with channels enabled.
	channel *channelPusher
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *mcpServer) handleLine(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.send(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	// Notifications carry no id and must not be answered.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
				// Declaring claude/channel is what makes Claude Code register a
				// listener for notifications/claude/channel — the path that
				// lets a room message reach a session mid-conversation instead
				// of waiting for its next turn.
				"experimental": map[string]any{
					"claude/channel": map[string]any{},
				},
			},
			"serverInfo": map[string]any{"name": "succubus", "version": Version},
			"instructions": "succubus coordinates multiple AI agents working on one project. " +
				"Call succubus_register first to adopt your identity, then succubus_context " +
				"to see the plan, your tasks, and files locked by other agents. " +
				"Always call succubus_claim_files before editing files.\n\n" +
				"Messages from the agent room may arrive mid-session as " +
				`<channel source="succubus" from="..." reply_to="...">. ` +
				"These are people or other agents addressing you directly. Read them, " +
				"and reply with succubus_say passing the reply_to value from the tag. " +
				"Answer before returning to what you were doing; a question left " +
				"unanswered blocks whoever asked it.\n\n" +
				"Asking another agent is cheap and fast: an agent that is working " +
				"receives your question within a second. Prefer succubus_ask over " +
				"guessing at something another agent already knows — which files they " +
				"are restructuring, why they chose an approach, whether they are about " +
				"to change something you depend on.",
		}})

	case "notifications/initialized", "initialized":
		// nothing to do

	case "tools/list":
		s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolSchemas()}})

	case "ping":
		s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &p)
		text, isErr := s.callTool(p.Name, p.Arguments)
		s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}})

	default:
		if !isNotification {
			s.send(rpcResponse{JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}})
		}
	}
}

func (s *mcpServer) send(resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	// Shared with pushChannel, which writes from a background goroutine.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
}

// session lazily opens (and registers) this process's identity.
func (s *mcpServer) session(register bool) (*Session, error) {
	if s.sess != nil && (!register || s.sess.AgentID != "") {
		return s.sess, nil
	}
	sess, err := OpenSession("", register)
	if err != nil {
		return sess, err
	}
	s.sess = sess
	// The channel pusher can only filter once it knows who this session is.
	if s.channel != nil && sess.AgentName != "" {
		s.channel.identify(sess.Project.ID, sess.AgentName)
	}
	return sess, nil
}

// daemonDownMsg is what an agent sees when the daemon is not running. It is
// deliberately phrased as guidance, not an error, so the agent keeps working.
const daemonDownMsg = "succubus daemon is not running, so coordination is unavailable right now. " +
	"Continue your work normally. To enable coordination, run: succubus daemon"

func (s *mcpServer) callTool(name string, rawArgs json.RawMessage) (string, bool) {
	args := map[string]any{}
	if len(rawArgs) > 0 {
		json.Unmarshal(rawArgs, &args)
	}

	// The schemas declare which arguments are required, but nothing enforced
	// it: a missing id was interpolated into the URL anyway, producing
	// /api/room//resolve and an error about the endpoint rather than about the
	// argument. Not every MCP client validates against the schema before
	// sending, so the server cannot assume it arrives complete.
	if missing := missingRequired(name, args); len(missing) > 0 {
		return fmt.Sprintf("Missing required argument(s): %s. Call tools/list for this tool's schema.",
			strings.Join(missing, ", ")), true
	}

	// Reading tools do not create an identity as a side effect. Docs go
	// further and need no daemon at all — they are compiled into the binary.
	if name == "succubus_docs" {
		if id, _ := args["section"].(string); id != "" {
			body, err := docs.Get(id)
			if err != nil {
				return "Unknown section. Call succubus_docs with no arguments to list them.", true
			}
			// Hand the agent real paths, not a placeholder it would have to
			// guess at.
			self, _ := os.Executable()
			if resolved, err := filepath.EvalSymlinks(self); err == nil {
				self = resolved
			}
			return assets.ResolvePaths(body, self), false
		}
		var b strings.Builder
		b.WriteString("succubus documentation. Pass `section` to read one:\n\n")
		for _, sec := range docs.List() {
			fmt.Fprintf(&b, "- **%s** (`%s`) — %s\n", sec.Title, sec.ID, sec.Summary)
		}
		return b.String(), false
	}

	needsID := name != "succubus_agents" && name != "succubus_task_list" && name != "succubus_plan_list"

	// A name requested via succubus_register has to reach the very first
	// registration — once an identity exists it is kept, so setting it later
	// would silently do nothing.
	if name == "succubus_register" {
		if want, ok := args["preferred_name"].(string); ok && want != "" {
			os.Setenv("SUCCUBUS_PREFERRED_NAME", want)
		}
	}

	sess, err := s.session(needsID)
	if err != nil {
		var down *client.ErrDaemonDown
		if errors.As(err, &down) {
			return daemonDownMsg, false
		}
		return "succubus error: " + err.Error(), true
	}
	c, pid := sess.Client, sess.Project.ID

	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	num := func(k string) int64 {
		if v, ok := args[k].(float64); ok {
			return int64(v)
		}
		return 0
	}
	list := func(k string) []string {
		raw, ok := args[k].([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if sv, ok := v.(string); ok && sv != "" {
				out = append(out, sv)
			}
		}
		return out
	}
	boolv := func(k string) bool {
		v, _ := args[k].(bool)
		return v
	}

	switch name {
	case "succubus_register":
		ctx, err := c.Context(pid, sess.AgentID)
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Registered with succubus.\n\n%s", ctx.Text), false

	case "succubus_whoami":
		if sess.AgentID == "" {
			return "You are not registered yet. Call succubus_register.", false
		}
		return fmt.Sprintf("You are %s (agent id %s) in project %s.",
			sess.AgentName, sess.AgentID, sess.Project.DisplayName), false

	case "succubus_context":
		ctx, err := c.Context(pid, sess.AgentID)
		if err != nil {
			return wrapErr(err)
		}
		return ctx.Text, false

	case "succubus_agents":
		agents, err := c.ListAgents(pid)
		if err != nil {
			return wrapErr(err)
		}
		claims, _ := c.ListClaims(pid)
		var b strings.Builder
		if len(agents) == 0 {
			return "No agents registered in this project yet.", false
		}
		for _, a := range agents {
			held := []string{}
			for _, cl := range claims {
				if cl.AgentID == a.ID {
					held = append(held, cl.Path)
				}
			}
			fmt.Fprintf(&b, "- %s (%s, %s)", a.Name, a.Tool, a.Status)
			if len(held) > 0 {
				fmt.Fprintf(&b, " holding: %s", strings.Join(held, ", "))
			}
			b.WriteString("\n")
		}
		return b.String(), false

	case "succubus_plan_list":
		plans, err := c.ListPlans(pid)
		if err != nil {
			return wrapErr(err)
		}
		if len(plans) == 0 {
			return "No plans yet. Create one with succubus_plan_create.", false
		}
		var b strings.Builder
		for _, p := range plans {
			fmt.Fprintf(&b, "- [%s] %s (id %s)\n", p.Status, p.Title, p.ID)
		}
		return b.String(), false

	case "succubus_plan_get":
		p, err := c.GetPlan(str("id"))
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("# %s\n_status: %s_\n\n%s", p.Title, p.Status, p.BodyMD), false

	case "succubus_plan_create":
		p, err := c.CreatePlan(pid, str("title"), str("body_md"), str("status"), sess.AgentName)
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Created plan %q (id %s).", p.Title, p.ID), false

	case "succubus_plan_update":
		patch := store.PlanPatch{}
		if v, ok := args["title"].(string); ok {
			patch.Title = &v
		}
		if v, ok := args["body_md"].(string); ok {
			patch.BodyMD = &v
		}
		if v, ok := args["status"].(string); ok {
			patch.Status = &v
		}
		p, err := c.UpdatePlan(str("id"), patch)
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Updated plan %q.", p.Title), false

	case "succubus_task_list":
		tasks, err := c.ListTasks(pid, map[string]string{
			"status": str("status"), "assignee": str("assignee"), "plan_id": str("plan_id"),
		})
		if err != nil {
			return wrapErr(err)
		}
		if len(tasks) == 0 {
			return "No tasks match.", false
		}
		var b strings.Builder
		for _, t := range tasks {
			flag := ""
			if t.Blocked {
				flag = " [BLOCKED]"
			}
			fmt.Fprintf(&b, "- [%s] %s — %s%s (id %s)\n",
				t.Status, t.Title, dash(t.AssigneeName), flag, t.ID)
		}
		return b.String(), false

	case "succubus_task_create":
		t, err := c.CreateTask(pid, client.NewTask{
			PlanID: str("plan_id"), Title: str("title"), BodyMD: str("body_md"),
			Status: str("status"), Priority: int(num("priority")),
			DependsOn: list("depends_on"),
		})
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Created task %q (id %s).", t.Title, t.ID), false

	case "succubus_task_update":
		patch := store.TaskPatch{}
		if v, ok := args["title"].(string); ok {
			patch.Title = &v
		}
		if v, ok := args["body_md"].(string); ok {
			patch.BodyMD = &v
		}
		if v, ok := args["status"].(string); ok {
			patch.Status = &v
		}
		if v, ok := args["assignee_name"].(string); ok {
			u := strings.ToUpper(v)
			patch.AssigneeName = &u
		}
		if v, ok := args["priority"].(float64); ok {
			n := int(v)
			patch.Priority = &n
		}
		t, err := c.UpdateTask(str("id"), patch)
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Updated task %q — status %s.", t.Title, t.Status), false

	case "succubus_task_claim":
		t, err := c.ClaimTask(str("id"), sess.AgentID, sess.AgentName, boolv("force"))
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("%s now owns task %q (status %s).", sess.AgentName, t.Title, t.Status), false

	case "succubus_claim_files":
		paths := list("paths")
		if len(paths) == 0 {
			return "No paths given. Pass the files you are about to edit.", true
		}
		res, err := c.ClaimFiles(pid, sess.AgentID, sess.AgentName, str("task_id"), paths, num("ttl_sec"))
		if err != nil {
			return wrapErr(err)
		}
		if res.Granted {
			return fmt.Sprintf("Claimed %d file(s) for %s. Release them with succubus_release_files when you are done.",
				len(res.Results), sess.AgentName), false
		}
		var b strings.Builder
		b.WriteString("CLAIM DENIED — another agent holds one or more of these files:\n")
		for _, r := range res.Results {
			if r.Holder != "" {
				fmt.Fprintf(&b, "  - %s is held by %s\n", r.Path, r.Holder)
			}
		}
		b.WriteString("\nDo not edit these files. Work on something else, or send the holder a note with succubus_handoff.")
		return b.String(), false

	case "succubus_release_files":
		n, err := c.ReleaseFiles(pid, sess.AgentID, list("paths"), boolv("all"))
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Released %d file claim(s).", n), false

	case "succubus_check_files":
		res, err := c.CheckFiles(pid, sess.AgentID, list("paths"))
		if err != nil {
			return wrapErr(err)
		}
		if !res.Conflict {
			return "All clear — no other agent holds these files.", false
		}
		var b strings.Builder
		b.WriteString("Conflicts found:\n")
		for _, r := range res.Results {
			if !r.Granted {
				fmt.Fprintf(&b, "  - %s held by %s\n", r.Path, r.Holder)
			}
		}
		return b.String(), false

	case "succubus_report":
		msg := str("message")
		if id := str("task_id"); id != "" {
			if st := str("status"); st != "" {
				c.UpdateTask(id, store.TaskPatch{Status: &st})
			}
		}
		_, err := c.CreateDecision(pid, "note", msg, str("detail"), sess.AgentID, sess.AgentName, "")
		if err != nil {
			return wrapErr(err)
		}
		return "Progress recorded.", false

	case "succubus_handoff":
		d, err := c.CreateDecision(pid, "handoff", str("title"), str("body_md"),
			sess.AgentID, sess.AgentName, str("to_agent"))
		if err != nil {
			return wrapErr(err)
		}
		return fmt.Sprintf("Handoff %q sent to %s.", d.Title, dash(d.TargetAgentName)), false

	case "succubus_ask":
		q, err := c.PostMessage(pid, "", store.MsgQuestion, sess.AgentID, sess.AgentName,
			str("question"), list("ask"))
		if err != nil {
			return wrapErr(err)
		}
		who := "the room"
		if len(q.Mentions) > 0 {
			who = strings.Join(q.Mentions, ", ")
		}
		// Being explicit about the delivery model matters: an agent that thinks
		// a reply is imminent will sit and wait for one that cannot come.
		return fmt.Sprintf("Asked %s (question id %s).\n\n"+
			"Other agents have no process running between their own turns, so they will "+
			"see this the next time their session is prompted — that may be a while. "+
			"The human sees it immediately in the dashboard.\n\n"+
			"Do not wait. Carry on with other work and check back with succubus_room. "+
			"If nothing can proceed without an answer, say so plainly and stop.",
			who, q.ID), false

	case "succubus_say":
		kind := store.MsgMessage
		if str("announce") != "" || boolv("announce") {
			kind = store.MsgAnnounce
		}
		m, err := c.PostMessage(pid, str("reply_to"), kind, sess.AgentID, sess.AgentName,
			str("message"), list("to"))
		if err != nil {
			return wrapErr(err)
		}
		if m.ParentID != "" {
			return "Replied in the agent room.", false
		}
		return "Posted to the agent room.", false

	case "succubus_room":
		room, err := c.Room(pid, 30)
		if err != nil {
			return wrapErr(err)
		}
		if room.Total == 0 {
			return "The agent room is empty. Start a conversation with succubus_ask or succubus_say.", false
		}
		var b strings.Builder
		if len(room.OpenQuestions) > 0 {
			fmt.Fprintf(&b, "UNANSWERED QUESTIONS (%d):\n", len(room.OpenQuestions))
			for _, q := range room.OpenQuestions {
				fmt.Fprintf(&b, "  - %s asked: %s (id %s)\n", q.AuthorName, q.BodyMD, q.ID)
			}
			b.WriteString("\n")
		}
		b.WriteString("RECENT CONVERSATION (newest first):\n")
		for _, m := range room.Messages {
			fmt.Fprintf(&b, "\n%s", store.FormatMessage(m))
			if m.Kind == store.MsgQuestion && m.ResolvedAt == 0 {
				fmt.Fprintf(&b, "  ← unanswered, id %s", m.ID)
			}
			b.WriteString("\n")
			for _, r := range m.Replies {
				fmt.Fprintf(&b, "    ↳ %s\n", store.FormatMessage(r))
			}
		}
		return b.String(), false

	case "succubus_resolve":
		if err := c.ResolveQuestion(str("id"), sess.AgentName); err != nil {
			return wrapErr(err)
		}
		return "Question marked resolved.", false

	case "succubus_decisions":
		if t := str("title"); t != "" {
			d, err := c.CreateDecision(pid, "decision", t, str("body_md"), sess.AgentID, sess.AgentName, "")
			if err != nil {
				return wrapErr(err)
			}
			return fmt.Sprintf("Recorded decision %q.", d.Title), false
		}
		ds, err := c.ListDecisions(pid, "", false, 30)
		if err != nil {
			return wrapErr(err)
		}
		if len(ds) == 0 {
			return "No decisions recorded yet.", false
		}
		var b strings.Builder
		for _, d := range ds {
			fmt.Fprintf(&b, "- [%s] %s — %s\n", d.Kind, d.Title, dash(d.AuthorName))
		}
		return b.String(), false
	}

	return "Unknown tool: " + name, true
}

func wrapErr(err error) (string, bool) {
	var down *client.ErrDaemonDown
	if errors.As(err, &down) {
		return daemonDownMsg, false
	}
	return "succubus: " + err.Error(), true
}

// missingRequired reports which of a tool's required arguments were absent or
// empty, checked against the very schemas the server publishes so the two can
// never drift apart.
//
// Empty counts as missing on purpose. An empty string id is not a value a
// caller meant to send — it is a variable that turned out blank — and passing
// it on produces a URL like /api/room//resolve, whose error names the wrong
// problem entirely.
func missingRequired(tool string, args map[string]any) []string {
	for _, t := range toolSchemas() {
		if t["name"] != tool {
			continue
		}
		schema, ok := t["inputSchema"].(map[string]any)
		if !ok {
			return nil
		}
		required, ok := schema["required"].([]string)
		if !ok {
			return nil
		}
		var missing []string
		for _, key := range required {
			v, present := args[key]
			if !present || v == nil {
				missing = append(missing, key)
				continue
			}
			switch typed := v.(type) {
			case string:
				if strings.TrimSpace(typed) == "" {
					missing = append(missing, key)
				}
			case []any:
				if len(typed) == 0 {
					missing = append(missing, key)
				}
			}
		}
		return missing
	}
	return nil
}

// obj is a small helper for building JSON Schema fragments.
func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func arrProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func toolSchemas() []map[string]any {
	return []map[string]any{
		{
			"name": "succubus_register",
			"description": "Adopt your identity in this project. Call this FIRST, before any other work. " +
				"Returns the name you must use for yourself, plus the current plan, your tasks, " +
				"and files locked by other agents.",
			"inputSchema": obj(map[string]any{
				"preferred_name": strProp("optional name to request; one is assigned if omitted"),
			}),
		},
		{
			"name":        "succubus_whoami",
			"description": "Recover your adopted identity if you have forgotten your name.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name": "succubus_context",
			"description": "Get the current shared state: active plan, your tasks, unassigned tasks, " +
				"other agents, files they hold, and handoffs addressed to you.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name":        "succubus_agents",
			"description": "List the other agents working in this project and what files each holds.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name":        "succubus_plan_list",
			"description": "List all plans in this project.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name":        "succubus_plan_get",
			"description": "Read the full body of one plan.",
			"inputSchema": obj(map[string]any{"id": strProp("plan id")}, "id"),
		},
		{
			"name":        "succubus_plan_create",
			"description": "Create a plan. Use this to record the overall approach so other agents can follow it.",
			"inputSchema": obj(map[string]any{
				"title":   strProp("short plan title"),
				"body_md": strProp("plan body in markdown"),
				"status":  strProp("draft, active, done, or archived (default active)"),
			}, "title"),
		},
		{
			"name":        "succubus_plan_update",
			"description": "Update a plan's title, body, or status.",
			"inputSchema": obj(map[string]any{
				"id": strProp("plan id"), "title": strProp("new title"),
				"body_md": strProp("new body"), "status": strProp("new status"),
			}, "id"),
		},
		{
			"name":        "succubus_task_list",
			"description": "List tasks, optionally filtered by status, assignee, or plan.",
			"inputSchema": obj(map[string]any{
				"status":   strProp("todo, in_progress, blocked, review, done, cancelled"),
				"assignee": strProp("agent name"), "plan_id": strProp("plan id"),
			}),
		},
		{
			"name":        "succubus_task_create",
			"description": "Create a task so other agents can see what needs doing and avoid duplicating it.",
			"inputSchema": obj(map[string]any{
				"title": strProp("what needs doing"), "body_md": strProp("details"),
				"plan_id": strProp("owning plan id"), "status": strProp("initial status (default todo)"),
				"priority":   numProp("1 high, 2 normal, 3 low"),
				"depends_on": arrProp("task ids that must finish first"),
			}, "title"),
		},
		{
			"name":        "succubus_task_update",
			"description": "Update a task's title, body, status, priority, or assignee.",
			"inputSchema": obj(map[string]any{
				"id": strProp("task id"), "title": strProp("new title"), "body_md": strProp("new body"),
				"status":        strProp("todo, in_progress, blocked, review, done, cancelled"),
				"assignee_name": strProp("agent name"), "priority": numProp("1-3"),
			}, "id"),
		},
		{
			"name":        "succubus_task_claim",
			"description": "Take ownership of a task so no other agent starts the same work.",
			"inputSchema": obj(map[string]any{
				"id": strProp("task id"), "force": boolProp("steal from a dead agent"),
			}, "id"),
		},
		{
			"name": "succubus_claim_files",
			"description": "Lease files BEFORE editing them. If another agent holds one, you will be told " +
				"who and must not edit it. Claims expire automatically, so a crashed agent never blocks a file forever.",
			"inputSchema": obj(map[string]any{
				"paths":   arrProp("repo-relative file paths you are about to edit"),
				"task_id": strProp("task these edits belong to"),
				"ttl_sec": numProp("lease seconds (default 900)"),
			}, "paths"),
		},
		{
			"name":        "succubus_release_files",
			"description": "Release file claims when you finish editing.",
			"inputSchema": obj(map[string]any{
				"paths": arrProp("paths to release"), "all": boolProp("release everything you hold"),
			}),
		},
		{
			"name":        "succubus_check_files",
			"description": "Check whether files are free before planning work on them. Does not claim anything.",
			"inputSchema": obj(map[string]any{"paths": arrProp("paths to check")}, "paths"),
		},
		{
			"name":        "succubus_report",
			"description": "Record progress on a task so other agents and the human can follow along.",
			"inputSchema": obj(map[string]any{
				"message": strProp("what you did"), "detail": strProp("longer detail"),
				"task_id": strProp("task id"), "status": strProp("new task status"),
			}, "message"),
		},
		{
			"name":        "succubus_handoff",
			"description": "Leave a note addressed to another agent by name. They see it in their next context.",
			"inputSchema": obj(map[string]any{
				"to_agent": strProp("recipient agent name, e.g. ORION"),
				"title":    strProp("subject"), "body_md": strProp("the note"),
			}, "to_agent", "title"),
		},
		{
			"name": "succubus_decisions",
			"description": "Read the decision log, or append to it by passing a title. " +
				"Use this for choices future agents need to know about.",
			"inputSchema": obj(map[string]any{
				"title": strProp("decision title (omit to read the log)"), "body_md": strProp("rationale"),
			}),
		},
		{
			"name": "succubus_ask",
			"description": "Ask the agent room a question when you are unsure and guessing would be " +
				"expensive — an ambiguous requirement, a convention you cannot infer, a file you do " +
				"not want to touch blind. Other agents and the human both see it. Do not block " +
				"waiting: post the question, work on something else, and check back.",
			"inputSchema": obj(map[string]any{
				"question": strProp("what you need to know, in full sentences"),
				"ask": arrProp("optional agent names to address, e.g. [\"ORION\"], " +
					"or [\"ALL\"] to reach everyone"),
			}, "question"),
		},
		{
			"name": "succubus_say",
			"description": "Post to the agent room: answer someone's question, share what you just " +
				"learned, or warn that you are about to change something broadly.",
			"inputSchema": obj(map[string]any{
				"message":  strProp("what you want to say"),
				"reply_to": strProp("message id you are replying to"),
				"to":       arrProp("agent names to address; use [\"ALL\"] for everyone"),
				"announce": boolProp("mark as an announcement rather than chat"),
			}, "message"),
		},
		{
			"name": "succubus_room",
			"description": "Read the agent room: unanswered questions first, then the recent " +
				"conversation with replies.",
			"inputSchema": obj(map[string]any{}),
		},
		{
			"name":        "succubus_resolve",
			"description": "Mark a question answered once it has been dealt with.",
			"inputSchema": obj(map[string]any{"id": strProp("question message id")}, "id"),
		},
		{
			"name": "succubus_docs",
			"description": "Read the succubus documentation: setup for each coding tool, " +
				"the full tool reference, architecture, and troubleshooting. " +
				"Call with no arguments to list the sections.",
			"inputSchema": obj(map[string]any{
				"section": strProp("section id, e.g. SETUP, MCP, ARCHITECTURE, TROUBLESHOOTING"),
			}),
		},
	}
}
