package mode

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/store"
)

// Enforcement levels. Blocking is opt-in because a false positive stalls a
// working agent, which is worse than a missed conflict.
const (
	EnforceOff   = "off"
	EnforceNag   = "nag"
	EnforceBlock = "block"
)

func enforcement() string {
	if v := os.Getenv("SUCCUBUS_ENFORCEMENT"); v != "" {
		return v
	}
	return EnforceNag
}

// hookPayload is the union of the fields the supported dialects send. Each tool
// names things slightly differently, so we accept every spelling and normalize.
type hookPayload struct {
	// Common
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	Prompt    string `json:"prompt"`

	// Claude Code / Factory Droid / Codex
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
	StopActive    bool            `json:"stop_hook_active"`

	// Gemini CLI
	Event    string          `json:"event"`
	Tool     string          `json:"tool"`
	ToolArgs json.RawMessage `json:"tool_args"`
	Args     json.RawMessage `json:"args"`

	// Cursor
	Command   string `json:"command"`
	Workspace string `json:"workspace_roots"`
}

// normalized flattens dialect differences into one shape.
type normalized struct {
	Event   string
	Tool    string
	CWD     string
	Session string
	Paths   []string
	// StopActive is true when the harness has already restarted this turn once
	// because of a hook — blocking again would trap the agent in a loop.
	StopActive bool
}

// Hook handles one lifecycle event. It must never fail loudly: an agent's turn
// is more important than succubus bookkeeping, so every error path exits 0.
func Hook(args []string) error {
	fs := flag.NewFlagSet("hook", flag.ExitOnError)
	dialect := fs.String("dialect", "", "claude|droid|codex|gemini|cursor|opencode")
	fs.Parse(args)

	rest := fs.Args()
	event := ""
	if len(rest) > 0 {
		event = rest[0]
	}

	raw, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	var p hookPayload
	json.Unmarshal(raw, &p) // best effort: a malformed payload still gets a no-op

	d := *dialect
	if d == "" {
		d = detectDialect(&p)
	}
	n := normalize(&p, event, d)

	switch classify(n.Event) {
	case evSessionStart:
		return hookSessionStart(n)
	case evPrompt:
		return hookPrompt(n)
	case evPreTool:
		return hookPreTool(n, d)
	case evPostTool:
		return hookPostTool(n)
	case evTurnEnd:
		return hookTurnEnd(n)
	case evSessionEnd:
		return hookSessionEnd(n)
	}
	return nil
}

type evKind int

const (
	evUnknown evKind = iota
	evSessionStart
	evPrompt
	evPreTool
	evPostTool
	evTurnEnd
	evSessionEnd
)

// classify maps every dialect's event vocabulary onto our five actions.
func classify(event string) evKind {
	switch strings.ToLower(event) {
	case "sessionstart", "session_start", "start":
		return evSessionStart
	case "userpromptsubmit", "user_prompt_submit", "beforeagent", "before_agent", "beforesubmitprompt":
		return evPrompt
	case "pretooluse", "pre_tool_use", "beforetool", "before_tool", "beforetoolselection",
		"beforeshellexecution", "tool.execute.before":
		return evPreTool
	case "posttooluse", "post_tool_use", "aftertool", "after_tool", "afterfileedit",
		"aftershellexecution", "tool.execute.after":
		return evPostTool
	case "stop", "afteragent":
		// The agent finished a turn but the session is still alive — the last
		// chance to make it answer before it goes quiet.
		return evTurnEnd
	case "sessionend", "session_end", "session.idle":
		return evSessionEnd
	}
	return evUnknown
}

func detectDialect(p *hookPayload) string {
	switch {
	case p.HookEventName != "":
		return "claude"
	case p.Event != "":
		return "gemini"
	case p.Command != "":
		return "cursor"
	}
	return "claude"
}

func normalize(p *hookPayload, event, dialect string) normalized {
	n := normalized{Event: event, CWD: p.CWD, Session: p.SessionID, StopActive: p.StopActive}
	if n.Event == "" {
		n.Event = firstNonEmpty(p.HookEventName, p.Event)
	}
	n.Tool = firstNonEmpty(p.ToolName, p.Tool)
	if n.CWD == "" {
		n.CWD, _ = os.Getwd()
	}
	// The session id is the anchor for identity; without it a resumed session
	// would be handed a new name.
	//
	// An explicit SUCCUBUS_SESSION always wins. `succubus wake` sets it so the
	// headless turn speaks as the agent it stands in for; overwriting it here
	// would give that turn a fresh identity and post the reply under the wrong
	// name.
	if n.Session != "" && os.Getenv("SUCCUBUS_SESSION") == "" {
		os.Setenv("SUCCUBUS_SESSION", dialect+":"+n.Session)
	}
	n.Paths = extractPaths(p)
	return n
}

// extractPaths pulls file paths out of whichever tool-input shape arrived.
func extractPaths(p *hookPayload) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	for _, raw := range []json.RawMessage{p.ToolInput, p.ToolArgs, p.Args} {
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, key := range []string{"file_path", "path", "filePath", "notebook_path", "absolute_path"} {
			if v, ok := m[key].(string); ok {
				add(v)
			}
		}
		// MultiEdit-style batches carry a list of edits.
		if edits, ok := m["edits"].([]any); ok {
			for _, e := range edits {
				if em, ok := e.(map[string]any); ok {
					if v, ok := em["file_path"].(string); ok {
						add(v)
					}
				}
			}
		}
		if files, ok := m["files"].([]any); ok {
			for _, f := range files {
				if s, ok := f.(string); ok {
					add(s)
				}
			}
		}
	}
	return out
}

// isMutatingTool reports whether a tool call will modify files.
func isMutatingTool(tool string) bool {
	switch strings.ToLower(tool) {
	case "edit", "write", "multiedit", "notebookedit", "str_replace_editor",
		"str_replace_based_edit_tool", "apply_patch", "create_file", "edit_file", "write_file":
		return true
	}
	return false
}

// ---- handlers --------------------------------------------------------------

// hookSessionStart registers the agent on the session's behalf. This is the
// key move: an agent that never calls succubus_register is still visible,
// still named, and still gets told who it is.
func hookSessionStart(n normalized) error {
	sess, err := OpenSession(n.CWD, true)
	if err != nil {
		return silent(err)
	}
	ctx, err := sess.Client.Context(sess.Project.ID, sess.AgentID)
	if err != nil {
		return silent(err)
	}
	emitContext(ctx.Text, "SessionStart")

	// SessionStart shows the room too, so mark it read here as well —
	// otherwise the same messages arrive again on the first prompt.
	sess.Client.MarkRoomRead(sess.Project.ID, sess.AgentName)
	return nil
}

// hookPrompt re-injects identity and state on every turn so compaction cannot
// erase who the agent is.
func hookPrompt(n normalized) error {
	sess, err := OpenSession(n.CWD, true)
	if err != nil {
		return silent(err)
	}
	sess.Client.Heartbeat(sess.AgentID)
	sess.Client.RenewClaims(sess.Project.ID, sess.AgentID, 0)

	ctx, err := sess.Client.Context(sess.Project.ID, sess.AgentID)
	if err != nil {
		return silent(err)
	}
	emitContext(ctx.Text, "UserPromptSubmit")

	// The agent has now been shown the room, so do not repeat these messages
	// on its next turn. Open questions are still re-surfaced separately.
	sess.Client.MarkRoomRead(sess.Project.ID, sess.AgentName)
	return nil
}

// hookPreTool blocks an edit only when a live claim is held by a different
// agent. Everything else passes through untouched.
func hookPreTool(n normalized, dialect string) error {
	if !isMutatingTool(n.Tool) || len(n.Paths) == 0 {
		return nil
	}
	mode := enforcement()
	if mode == EnforceOff {
		return nil
	}

	sess, err := OpenSession(n.CWD, true)
	if err != nil {
		return silent(err)
	}
	res, err := sess.Client.CheckFiles(sess.Project.ID, sess.AgentID, n.Paths)
	if err != nil || !res.Conflict {
		return silent(err)
	}

	msg := fmt.Sprintf("succubus: %s is currently held by %s. Do not edit it. "+
		"Coordinate with succubus_handoff, or work on another file.",
		conflictPath(res), res.Holder)

	if mode != EnforceBlock {
		emitSystemMessage(msg)
		return nil
	}

	switch dialect {
	case "gemini":
		// Gemini treats exit code 2 as a hard block.
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	case "cursor":
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"permission": "deny", "userMessage": msg, "agentMessage": msg,
		})
	default: // claude, droid, codex
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":            "PreToolUse",
				"permissionDecision":       "deny",
				"permissionDecisionReason": msg,
			},
		})
	}
	return nil
}

// hookPostTool nags when a file was edited without a claim. It also
// opportunistically claims the file, so the next agent gets a real conflict
// signal rather than silence.
func hookPostTool(n normalized) error {
	if !isMutatingTool(n.Tool) || len(n.Paths) == 0 {
		return nil
	}
	if enforcement() == EnforceOff {
		return nil
	}

	sess, err := OpenSession(n.CWD, true)
	if err != nil {
		return silent(err)
	}
	res, err := sess.Client.CheckFiles(sess.Project.ID, sess.AgentID, n.Paths)
	if err != nil {
		return silent(err)
	}

	unclaimed := []string{}
	for _, r := range res.Results {
		if r.Granted && r.Holder == "" {
			unclaimed = append(unclaimed, r.Path)
		}
	}
	if len(unclaimed) == 0 {
		return nil
	}

	sess.Client.ClaimFiles(sess.Project.ID, sess.AgentID, sess.AgentName, "",
		unclaimed, store.DefaultLeaseTTLSec)
	emitSystemMessage(fmt.Sprintf(
		"succubus: you edited %s without claiming it first. Claimed it for you as %s — "+
			"call succubus_claim_files before editing next time.",
		strings.Join(unclaimed, ", "), sess.AgentName))
	return nil
}

// hookTurnEnd fires when an agent finishes a turn but its session is still
// alive — the last moment anything can still make it act.
//
// If someone addressed it by name and it never replied, this asks the harness
// to keep the turn going. That is the only automatic nudge available: no tool
// exposes an event for "a message arrived", so the closest thing to reacting
// immediately is refusing to go quiet while a question is outstanding.
func hookTurnEnd(n normalized) error {
	// Guard against a loop: a harness that already restarted us once has done
	// its part, and blocking again would trap the agent.
	if n.StopActive || enforcement() == EnforceOff {
		return nil
	}

	sess, err := OpenSession(n.CWD, false)
	if err != nil || sess.AgentID == "" {
		return silent(err)
	}
	ctx, err := sess.Client.Context(sess.Project.ID, sess.AgentID)
	if err != nil {
		return silent(err)
	}

	// Only a direct mention counts. Broadcasts and general traffic are not
	// worth holding a turn open for.
	var pending []string
	for _, m := range ctx.RoomMentions {
		if m.DirectMention {
			pending = append(pending, fmt.Sprintf("%s: %s (reply_to %s)",
				m.AuthorName, trimLine(m.BodyMD, 140), m.ID))
		}
	}
	if len(pending) == 0 {
		return nil
	}

	reason := fmt.Sprintf(
		"succubus: %d message(s) in the agent room address you by name and you have "+
			"not replied. Answer with succubus_say (reply_to set) before finishing:\n  - %s",
		len(pending), strings.Join(pending, "\n  - "))

	emitStopBlock(reason)
	return nil
}

// emitStopBlock asks the harness to continue the turn instead of ending it.
func emitStopBlock(reason string) {
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"decision": "block",
		"reason":   reason,
	})
}

func trimLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// hookSessionEnd releases everything the agent held so a finished session never
// blocks the next one.
func hookSessionEnd(n normalized) error {
	sess, err := OpenSession(n.CWD, false)
	if err != nil || sess.AgentID == "" {
		return silent(err)
	}
	sess.Client.ReleaseFiles(sess.Project.ID, sess.AgentID, nil, true)
	return nil
}

// ---- output ----------------------------------------------------------------

// emitContext writes the injection payload in Claude Code's hook output shape,
// which Factory Droid and Codex also accept.
func emitContext(text, eventName string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	wrapped := "<succubus-coordination>\n" + text + "</succubus-coordination>"
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     eventName,
			"additionalContext": wrapped,
		},
		"suppressOutput": true,
	})
}

func emitSystemMessage(msg string) {
	json.NewEncoder(os.Stdout).Encode(map[string]any{"systemMessage": msg})
}

func conflictPath(res *client.CheckResponse) string {
	for _, r := range res.Results {
		if !r.Granted {
			return r.Path
		}
	}
	return "a file"
}

// silent swallows the errors a hook must never surface. succubus being down, or
// the session simply not being in a project, must not interrupt the agent's
// work — a session opened in a home directory or a scratch folder is a normal
// thing to do, not a fault to report on every turn.
func silent(err error) error {
	if err == nil {
		return nil
	}
	var down *client.ErrDaemonDown
	if errors.As(err, &down) {
		return nil
	}
	if strings.Contains(err.Error(), "is not a project") {
		return nil
	}
	fmt.Fprintf(os.Stderr, "succubus hook: %v\n", err)
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
