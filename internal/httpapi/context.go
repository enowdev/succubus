package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/enowdev/succubus/internal/store"
)

// ContextPayload is everything an agent needs to know at the start of a turn:
// who it is, what the plan is, what it owns, and what other agents are holding.
type ContextPayload struct {
	Project    *store.Project   `json:"project"`
	Me         *store.Agent     `json:"me,omitempty"`
	Agents     []store.Agent    `json:"agents"`
	ActivePlan *store.Plan      `json:"active_plan,omitempty"`
	MyTasks    []store.Task     `json:"my_tasks"`
	OpenTasks  []store.Task     `json:"open_tasks"`
	Claims     []store.Claim    `json:"claims"`
	Handoffs   []store.Decision `json:"handoffs"`
	// Room traffic this agent has not been shown yet.
	RoomUnread    []store.Message `json:"room_unread"`
	RoomMentions  []store.Message `json:"room_mentions"`
	OpenQuestions []store.Message `json:"open_questions"`
	Text          string          `json:"text"`
}

func (s *Server) getContext(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	agentID := r.URL.Query().Get("agent_id")

	payload, err := s.BuildContext(pid, agentID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// BuildContext assembles the injection payload. It is shared by the HTTP
// endpoint, the MCP succubus_context tool, and the hook injector so all three
// tell an agent exactly the same story.
func (s *Server) BuildContext(projectID, agentID string) (*ContextPayload, error) {
	p, err := s.St.GetProject(projectID)
	if err != nil {
		return nil, err
	}
	out := &ContextPayload{Project: p, MyTasks: []store.Task{}, OpenTasks: []store.Task{}}

	if agentID != "" {
		if a, err := s.St.GetAgent(agentID); err == nil {
			out.Me = a
		}
	}

	agents, err := s.St.ListAgents(projectID, false)
	if err != nil {
		return nil, err
	}
	out.Agents = agents

	plans, err := s.St.ListPlans(projectID)
	if err != nil {
		return nil, err
	}
	for i := range plans {
		if plans[i].Status == "active" {
			out.ActivePlan = &plans[i]
			break
		}
	}

	tasks, err := s.St.ListTasks(projectID, store.TaskFilter{})
	if err != nil {
		return nil, err
	}
	myName := ""
	if out.Me != nil {
		myName = out.Me.Name
	}
	for _, t := range tasks {
		switch {
		case myName != "" && t.AssigneeName == myName && t.Status != store.StatusDone && t.Status != store.StatusCancelled:
			out.MyTasks = append(out.MyTasks, t)
		case t.Status == store.StatusTodo && t.AssigneeName == "":
			out.OpenTasks = append(out.OpenTasks, t)
		}
	}

	claims, err := s.St.ActiveClaims(projectID)
	if err != nil {
		return nil, err
	}
	out.Claims = claims

	if myName != "" {
		if hs, err := s.St.ListDecisions(projectID, myName, true, 0, 10); err == nil {
			out.Handoffs = hs
		}
		// Room traffic since this agent was last shown the conversation.
		if unread, mentions, err := s.St.UnreadFor(projectID, myName, 12); err == nil {
			out.RoomUnread, out.RoomMentions = unread, mentions
		}
	}
	if qs, err := s.St.OpenQuestions(projectID, 6); err == nil {
		out.OpenQuestions = qs
	}

	if out.Handoffs == nil {
		out.Handoffs = []store.Decision{}
	}
	if out.RoomUnread == nil {
		out.RoomUnread = []store.Message{}
	}
	if out.RoomMentions == nil {
		out.RoomMentions = []store.Message{}
	}
	if out.OpenQuestions == nil {
		out.OpenQuestions = []store.Message{}
	}

	out.Text = renderContext(out)
	return out, nil
}

// renderContext produces the plain-text block that hooks inject into an agent's
// context window. It is deliberately compact: it competes for tokens with the
// user's actual prompt.
func renderContext(c *ContextPayload) string {
	var b strings.Builder

	if c.Me != nil {
		fmt.Fprintf(&b, "You are **%s** in this project (succubus agent id %s).\n", c.Me.Name, c.Me.ID)
		b.WriteString("Use this identity in all succubus tool calls and when referring to yourself.\n\n")
	} else {
		b.WriteString("You are not yet registered with succubus. Call succubus_register first.\n\n")
	}

	others := []string{}
	for _, a := range c.Agents {
		if c.Me != nil && a.ID == c.Me.ID {
			continue
		}
		others = append(others, fmt.Sprintf("%s (%s, %s)", a.Name, a.Tool, a.Status))
	}
	if len(others) > 0 {
		fmt.Fprintf(&b, "Other agents active here: %s\n\n", strings.Join(others, ", "))
	} else {
		b.WriteString("You are currently the only agent in this project.\n\n")
	}

	if c.ActivePlan != nil {
		fmt.Fprintf(&b, "ACTIVE PLAN — %s\n", c.ActivePlan.Title)
		body := strings.TrimSpace(c.ActivePlan.BodyMD)
		if body != "" {
			if len(body) > 1500 {
				body = body[:1500] + "\n…(truncated; call succubus_plan_get for the rest)"
			}
			b.WriteString(body + "\n")
		}
		b.WriteString("\n")
	}

	if len(c.MyTasks) > 0 {
		b.WriteString("YOUR TASKS:\n")
		for _, t := range c.MyTasks {
			flag := ""
			if t.Blocked {
				flag = " [BLOCKED by dependencies]"
			}
			fmt.Fprintf(&b, "  - [%s] %s (%s)%s\n", t.Status, t.Title, t.ID, flag)
		}
		b.WriteString("\n")
	}

	if len(c.OpenTasks) > 0 {
		b.WriteString("UNASSIGNED TASKS (claim one with succubus_task_claim):\n")
		for i, t := range c.OpenTasks {
			if i >= 8 {
				fmt.Fprintf(&b, "  …and %d more\n", len(c.OpenTasks)-i)
				break
			}
			fmt.Fprintf(&b, "  - %s (%s)\n", t.Title, t.ID)
		}
		b.WriteString("\n")
	}

	// Only foreign claims matter here — they are the ones that will block edits.
	foreign := []store.Claim{}
	for _, cl := range c.Claims {
		if c.Me == nil || cl.AgentID != c.Me.ID {
			foreign = append(foreign, cl)
		}
	}
	if len(foreign) > 0 {
		b.WriteString("FILES LOCKED BY OTHER AGENTS — do not edit these:\n")
		for i, cl := range foreign {
			if i >= 15 {
				fmt.Fprintf(&b, "  …and %d more\n", len(foreign)-i)
				break
			}
			fmt.Fprintf(&b, "  - %s (held by %s)\n", cl.Path, cl.AgentName)
		}
		b.WriteString("\n")
	}

	if len(c.Handoffs) > 0 {
		b.WriteString("UNREAD HANDOFFS ADDRESSED TO YOU:\n")
		for _, h := range c.Handoffs {
			fmt.Fprintf(&b, "  - from %s: %s (%s)\n", h.AuthorName, h.Title, h.ID)
		}
		b.WriteString("\n")
	}

	// Being named directly is the loudest thing in the room. Broadcasts are
	// worth showing, but only a direct mention means someone is waiting on
	// *this* agent — collapsing the two trains agents to ignore both.
	direct := []store.Message{}
	broadcast := []store.Message{}
	for _, m := range c.RoomMentions {
		if m.DirectMention {
			direct = append(direct, m)
		} else {
			broadcast = append(broadcast, m)
		}
	}

	if len(direct) > 0 {
		// Phrased as a required first action, not a suggestion. An earlier
		// wording ("reply before continuing") was read as advice and skipped —
		// the message was seen, acknowledged in passing, and never answered.
		b.WriteString("*** ACTION REQUIRED — SOMEONE IS WAITING ON YOU ***\n")
		fmt.Fprintf(&b, "%d message(s) address you by name. Before you do anything else "+
			"in this turn — before answering the user, before touching a file — call "+
			"succubus_say with reply_to set to the id shown.\n", len(direct))
		b.WriteString("Do this even if you are mid-task; it takes one call. Mentioning " +
			"that a message arrived is not the same as replying to it.\n\n")
		for _, m := range direct {
			fmt.Fprintf(&b, "  - %s\n    → reply: succubus_say reply_to=%s\n",
				store.FormatMessage(m), m.ID)
		}
		b.WriteString("\n")
	}

	if len(broadcast) > 0 {
		b.WriteString("ANNOUNCEMENTS TO EVERYONE (@ALL):\n")
		for _, m := range broadcast {
			fmt.Fprintf(&b, "  - %s\n", store.FormatMessage(m))
		}
		b.WriteString("\n")
	}

	if len(c.RoomUnread) > 0 {
		fmt.Fprintf(&b, "AGENT ROOM — %d new message(s):\n", len(c.RoomUnread))
		for _, m := range c.RoomUnread {
			fmt.Fprintf(&b, "  - %s\n", store.FormatMessage(m))
		}
		b.WriteString("\n")
	}

	// Open questions are shown even when already seen: an unanswered question
	// nobody looks at again is the failure this is meant to prevent.
	if len(c.OpenQuestions) > 0 {
		b.WriteString("UNANSWERED QUESTIONS IN THE ROOM — answer any you can:\n")
		for _, q := range c.OpenQuestions {
			if c.Me != nil && q.AuthorName == c.Me.Name {
				continue // your own question
			}
			fmt.Fprintf(&b, "  - %s asked: %s (id %s)\n", q.AuthorName, trim(q.BodyMD, 160), q.ID)
		}
		b.WriteString("\n")
	}

	b.WriteString(standingOrders(c))
	return b.String()
}

// standingOrders is the part of the injection that tells an agent what to
// *write back*, not just what to read.
//
// Without this the coordination is one-way: agents happily read the plan and
// claim files, but nothing ever prompts them to record what they are doing, so
// the board only fills up if a human types into it. The instructions adapt to
// the current state, because "write a plan" is noise once a plan exists.
func standingOrders(c *ContextPayload) string {
	var b strings.Builder
	b.WriteString("HOW TO WORK HERE — these are standing instructions, not suggestions:\n")

	if c.ActivePlan == nil {
		b.WriteString("  1. There is NO ACTIVE PLAN. Before starting anything non-trivial, write one with\n" +
			"     succubus_plan_create — what you are building and the approach. Every other\n" +
			"     agent reads it on every turn; it is how four sessions stay pointed the same way.\n")
	} else {
		b.WriteString("  1. Keep the active plan honest. If the approach changes, update it with\n" +
			"     succubus_plan_update rather than letting it go stale.\n")
	}

	b.WriteString("  2. Break work into tasks with succubus_task_create as soon as you know what they\n" +
		"     are — before doing them, not after. An unrecorded task is one another agent\n" +
		"     may start in parallel.\n")

	if len(c.MyTasks) > 0 {
		b.WriteString("  3. Move YOUR tasks through the board as you go: succubus_task_update to\n" +
			"     in_progress when you start, review or done when you finish. Do not leave a\n" +
			"     task sitting in in_progress after you have stopped working on it.\n")
	} else if len(c.OpenTasks) > 0 {
		b.WriteString("  3. Take an unassigned task with succubus_task_claim before starting it, so no\n" +
			"     one else picks up the same work.\n")
	} else {
		b.WriteString("  3. Claim a task with succubus_task_claim before you start it.\n")
	}

	b.WriteString("  4. Claim files with succubus_claim_files BEFORE editing them, and release them\n" +
		"     with succubus_release_files when done. If a file is held by someone else, do\n" +
		"     not edit it.\n")
	b.WriteString("  5. Record anything the next agent would otherwise have to rediscover: a choice\n" +
		"     and its reasoning with succubus_decisions, progress with succubus_report.\n")
	b.WriteString("  6. If you are unsure — an ambiguous requirement, a convention you cannot infer —\n" +
		"     ask in the agent room with succubus_ask instead of guessing. Do not block\n" +
		"     waiting for a reply; carry on with something else.\n")
	return b.String()
}

// trim shortens a body to one readable line.
func trim(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
