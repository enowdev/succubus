package mode

import (
	"os"
	"strconv"
	"strings"

	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/identity"
)

// Session ties a running process to its adopted succubus identity.
type Session struct {
	Client    *client.Client
	Project   identity.Project
	AgentID   string
	AgentName string
	Tool      string
	Key       string
	CWD       string
}

// DetectTool guesses which agent runtime we are embedded in. Each tool exports
// its own marker variables; falling back to "unknown" is fine because identity
// is keyed on the session, not the tool.
func DetectTool() string {
	if v := os.Getenv("SUCCUBUS_TOOL"); v != "" {
		return v
	}
	for env, name := range map[string]string{
		"CLAUDECODE":          "claude-code",
		"CLAUDE_CODE_SESSION": "claude-code",
		"OPENCODE":            "opencode",
		"OPENCODE_SESSION":    "opencode",
		"CODEX_SESSION":       "codex",
		"FACTORY_SESSION":     "droid",
		"DROID_SESSION":       "droid",
		"GEMINI_CLI":          "gemini",
		"CURSOR_AGENT":        "cursor",
		"COPILOT_CLI":         "copilot",
	} {
		if os.Getenv(env) != "" {
			return name
		}
	}
	if strings.Contains(os.Getenv("TERM_PROGRAM"), "vscode") {
		return "vscode"
	}
	return "unknown"
}

// SessionKey identifies one agent session. Stability matters more than
// uniqueness of form: the same key must come back after a resume, so a resumed
// session keeps its name.
func SessionKey(tool string) string {
	// SUCCUBUS_SESSION is a complete override, used verbatim. The hook handler
	// sets it to "<dialect>:<session id>" before resolving a session, so
	// prefixing the tool again here would produce a second, different key for
	// the same session — and therefore a second identity.
	if v := os.Getenv("SUCCUBUS_SESSION"); v != "" {
		return v
	}
	for _, env := range []string{
		"CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION",
		"OPENCODE_SESSION", "CODEX_SESSION_ID", "FACTORY_SESSION_ID", "GEMINI_SESSION_ID",
	} {
		if v := os.Getenv(env); v != "" {
			return tool + ":" + v
		}
	}
	// No session id available: fall back to the terminal, which is stable for
	// the life of the shell, then to the parent pid.
	if v := os.Getenv("TERM_SESSION_ID"); v != "" {
		return tool + ":term:" + v
	}
	return tool + ":ppid:" + strconv.Itoa(os.Getppid())
}

// OpenSession resolves the project, registers (or recovers) this session's
// identity, and caches it on disk so the name survives restarts.
//
// register=false skips creating an identity, for read-only commands.
func OpenSession(cwd string, register bool) (*Session, error) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	tool := DetectTool()
	key := SessionKey(tool)
	proj := identity.ResolveProject(cwd)

	c := client.New(DefaultAddr(), 0)
	s := &Session{Client: c, Project: proj, Tool: tool, Key: key, CWD: cwd}

	if _, err := c.ResolveProject(cwd); err != nil {
		return s, err
	}

	// Prefer the cached identity so a restarted process reclaims its own name.
	if cached, err := identity.LoadCached(proj.RootPath, key); err == nil && cached.ProjectID == proj.ID {
		s.AgentID, s.AgentName = cached.AgentID, cached.Name
	}
	if !register {
		return s, nil
	}

	// An explicitly requested name only matters when this session has no
	// identity yet; the server keeps the existing one either way.
	want := s.AgentName
	if want == "" {
		want = os.Getenv("SUCCUBUS_PREFERRED_NAME")
	}

	res, err := c.Register(proj.ID, want, tool, key, cwd, os.Getpid())
	if err != nil {
		return s, err
	}
	s.AgentID, s.AgentName = res.Agent.ID, res.Agent.Name
	identity.SaveCached(proj.RootPath, identity.Cached{
		AgentID: s.AgentID, Name: s.AgentName, ProjectID: proj.ID,
		SessionKey: key, Tool: tool,
	})
	return s, nil
}
