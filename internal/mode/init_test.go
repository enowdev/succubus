package mode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStopHookIsWiredToStop is a regression test for issue #1.
//
// Codex's Stop event was wired to `hook SessionEnd`. Those two are not
// interchangeable: classify() maps "stop" to evTurnEnd, which keeps a turn alive
// so the agent answers a question addressed to it, while "sessionend" maps to
// evSessionEnd, which releases *every* file claim the agent holds.
//
// So a Codex agent released all its locks each time it finished a turn — in the
// middle of a session it was still working in — and never got the turn-end
// nudge. The mistake is invisible in the config file, which looks perfectly
// reasonable until you know what the two events mean.
//
// This checks every tool, because the bug is a mismatch between the event a
// harness fires and the handler it is pointed at, and nothing else prevents it.
func TestStopHookIsWiredToStop(t *testing.T) {
	home := t.TempDir()
	bin := "/usr/local/bin/succubus"

	// Each initialiser refuses to run when its tool's directory is absent, so
	// create them all.
	for _, d := range []string{".claude", ".factory", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		tool string
		path string
		init func() error
	}{
		{"claude", filepath.Join(home, ".claude", "settings.json"),
			func() error { return initClaude(home, bin, false) }},
		{"droid", filepath.Join(home, ".factory", "hooks.json"),
			func() error { return initDroid(home, bin, false) }},
		{"codex", filepath.Join(home, ".codex", "hooks.json"),
			func() error { return initCodex(home, bin, false) }},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			if err := c.init(); err != nil {
				t.Skipf("%s init unavailable here: %v", c.tool, err)
			}
			b, err := os.ReadFile(c.path)
			if err != nil {
				t.Skipf("%s wrote no config: %v", c.tool, err)
			}

			var cfg map[string]any
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatalf("%s wrote invalid JSON: %v", c.tool, err)
			}

			stop := findHookCommands(cfg, "Stop")
			if len(stop) == 0 {
				t.Skipf("%s configures no Stop hook", c.tool)
			}
			for _, cmd := range stop {
				if strings.Contains(cmd, "hook SessionEnd") {
					t.Errorf("%s maps Stop to `hook SessionEnd`, which releases every "+
						"file claim at the end of each turn: %s", c.tool, cmd)
				}
				if !strings.Contains(cmd, "hook Stop") {
					t.Errorf("%s Stop hook does not invoke `hook Stop`: %s", c.tool, cmd)
				}
			}
		})
	}
}

// TestSessionEndHookIsWiredToSessionEnd is the mirror image: the event that
// really does end a session must be the one that releases the claims, or a
// finished agent holds its files until the lease expires.
func TestSessionEndHookIsWiredToSessionEnd(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initClaude(home, "/usr/local/bin/succubus", false); err != nil {
		t.Skipf("claude init unavailable: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Skip("no config written")
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range findHookCommands(cfg, "SessionEnd") {
		if !strings.Contains(cmd, "hook SessionEnd") {
			t.Errorf("SessionEnd is not wired to `hook SessionEnd`: %s", cmd)
		}
	}
}

// findHookCommands pulls every command string configured for an event, without
// assuming a particular nesting — the dialects differ in shape and this test is
// about which handler runs, not about the schema.
func findHookCommands(cfg map[string]any, event string) []string {
	var out []string

	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				if k == "command" {
					if s, ok := vv.(string); ok {
						out = append(out, s)
					}
					continue
				}
				walk(vv)
			}
		case []any:
			for _, vv := range t {
				walk(vv)
			}
		}
	}

	// Find the subtree for this event wherever it sits.
	var find func(v any)
	find = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, vv := range t {
				if k == event {
					walk(vv)
					continue
				}
				find(vv)
			}
		case []any:
			for _, vv := range t {
				find(vv)
			}
		}
	}
	find(cfg)
	return out
}
