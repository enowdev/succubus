package mode

import (
	"encoding/json"
	"testing"
)

// TestClassifyEveryDialect pins the event vocabulary of every supported tool to
// the five actions the handler implements. A tool renaming an event silently
// turns off enforcement for that tool, so this is the guard.
func TestClassifyEveryDialect(t *testing.T) {
	cases := map[string]evKind{
		// Claude Code, Factory Droid, Codex CLI
		"SessionStart":     evSessionStart,
		"UserPromptSubmit": evPrompt,
		"PreToolUse":       evPreTool,
		"PostToolUse":      evPostTool,
		"SessionEnd":       evSessionEnd,
		// Stop ends a turn, not the session — the agent is still alive, which
		// is what lets the handler hold the turn open for an unanswered
		// mention. Conflating it with SessionEnd would lose that.
		"Stop": evTurnEnd,

		// Gemini CLI
		"BeforeAgent":         evPrompt,
		"BeforeTool":          evPreTool,
		"BeforeToolSelection": evPreTool,
		"AfterTool":           evPostTool,
		"AfterAgent":          evTurnEnd,

		// Cursor
		"beforeShellExecution": evPreTool,
		"afterFileEdit":        evPostTool,
		"beforeSubmitPrompt":   evPrompt,

		// OpenCode plugin
		"tool.execute.before": evPreTool,
		"tool.execute.after":  evPostTool,
		"session.idle":        evSessionEnd,

		// Case must not matter: dialects disagree on it.
		"sessionstart": evSessionStart,
		"PRETOOLUSE":   evPreTool,

		"SomethingElse": evUnknown,
		"":              evUnknown,
	}

	for event, want := range cases {
		if got := classify(event); got != want {
			t.Errorf("classify(%q) = %v, want %v", event, got, want)
		}
	}
}

func TestIsMutatingTool(t *testing.T) {
	mutating := []string{
		"Edit", "Write", "MultiEdit", "NotebookEdit", "edit", "write",
		"str_replace_editor", "apply_patch", "create_file", "edit_file",
	}
	for _, name := range mutating {
		if !isMutatingTool(name) {
			t.Errorf("%q should count as a file-modifying tool", name)
		}
	}
	// Read-only tools must never trigger a claim check; nagging on Read would
	// make the hooks unbearable.
	for _, name := range []string{"Read", "Bash", "Grep", "Glob", "WebFetch", ""} {
		if isMutatingTool(name) {
			t.Errorf("%q must not be treated as file-modifying", name)
		}
	}
}

// TestExtractPathsAcrossDialects: every tool spells its file argument
// differently, and a missed spelling silently disables enforcement.
func TestExtractPathsAcrossDialects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"claude edit", `{"tool_input":{"file_path":"a.go"}}`, []string{"a.go"}},
		{"generic path", `{"tool_input":{"path":"b.go"}}`, []string{"b.go"}},
		{"camelCase", `{"tool_input":{"filePath":"c.go"}}`, []string{"c.go"}},
		{"notebook", `{"tool_input":{"notebook_path":"d.ipynb"}}`, []string{"d.ipynb"}},
		{"gemini args", `{"tool_args":{"file_path":"e.go"}}`, []string{"e.go"}},
		{"multiedit", `{"tool_input":{"edits":[{"file_path":"f.go"},{"file_path":"g.go"}]}}`,
			[]string{"f.go", "g.go"}},
		{"file list", `{"tool_input":{"files":["h.go","i.go"]}}`, []string{"h.go", "i.go"}},
		{"nothing", `{"tool_input":{"pattern":"*.go"}}`, nil},
		{"malformed", `{"tool_input":"not an object"}`, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p hookPayload
			if err := json.Unmarshal([]byte(c.raw), &p); err != nil {
				// A payload we cannot parse must still not crash the handler.
				t.Logf("unmarshal: %v (acceptable)", err)
			}
			got := extractPaths(&p)
			if len(got) != len(c.want) {
				t.Fatalf("extractPaths = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("extractPaths = %v, want %v", got, c.want)
				}
			}
		})
	}
}

// TestExtractPathsDeduplicates: the same file named twice is one claim.
func TestExtractPathsDeduplicates(t *testing.T) {
	var p hookPayload
	json.Unmarshal([]byte(`{"tool_input":{"file_path":"a.go","path":"a.go"}}`), &p)
	if got := extractPaths(&p); len(got) != 1 {
		t.Fatalf("expected 1 deduplicated path, got %v", got)
	}
}

func TestDetectDialect(t *testing.T) {
	cases := map[string]string{
		`{"hook_event_name":"PreToolUse"}`: "claude",
		`{"event":"BeforeTool"}`:           "gemini",
		`{"command":"ls"}`:                 "cursor",
		`{}`:                               "claude", // safest default
	}
	for raw, want := range cases {
		var p hookPayload
		json.Unmarshal([]byte(raw), &p)
		if got := detectDialect(&p); got != want {
			t.Errorf("detectDialect(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestEnforcementDefaultsToNag(t *testing.T) {
	t.Setenv("SUCCUBUS_ENFORCEMENT", "")
	if got := enforcement(); got != EnforceNag {
		t.Fatalf("default enforcement should be %q, got %q", EnforceNag, got)
	}
	// Blocking must stay opt-in: a false positive stalls a working agent.
	t.Setenv("SUCCUBUS_ENFORCEMENT", "block")
	if got := enforcement(); got != EnforceBlock {
		t.Fatalf("expected block, got %q", got)
	}
}

func TestSessionKeyIsStable(t *testing.T) {
	t.Setenv("SUCCUBUS_SESSION", "fixed-id")
	a := SessionKey("claude-code")
	if a != SessionKey("claude-code") {
		t.Fatal("session key must be stable across calls")
	}

	// SUCCUBUS_SESSION is a verbatim override. The hook handler sets it to
	// "<dialect>:<session id>" and then resolves a session; if SessionKey
	// prefixed the tool again, that round trip would yield a second key — and
	// the same session would end up with two identities.
	if a != "fixed-id" {
		t.Fatalf("SUCCUBUS_SESSION must be used verbatim, got %q", a)
	}
	if SessionKey("opencode") != a {
		t.Fatal("an explicit session override must not vary by tool")
	}
}

// TestSessionKeyNamespacesToolSpecificIDs: without an explicit override, two
// tools reporting the same session id must still be two agents.
func TestSessionKeyNamespacesToolSpecificIDs(t *testing.T) {
	t.Setenv("SUCCUBUS_SESSION", "")
	t.Setenv("CLAUDE_SESSION_ID", "abc")

	claude := SessionKey("claude-code")
	if claude != "claude-code:abc" {
		t.Fatalf("expected the tool to namespace the id, got %q", claude)
	}
	if SessionKey("opencode") == claude {
		t.Fatal("the same id under two tools must not collide")
	}
}
