package mode

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Tool describes one agent runtime succubus can wire itself into.
type Tool struct {
	ID    string // stable key used by --tools
	Name  string // human name
	Notes string // caveat worth showing before writing anything

	// Detected is filled in by DetectTools.
	Installed bool
	// Evidence is why we think it is installed — a binary path or config dir.
	Evidence string

	// Capabilities, so the summary can be honest about what you get.
	HasHooks bool
	HasMCP   bool
	// NeedsPlugin marks tools where a file has to be copied into the project.
	NeedsPlugin bool
	// Manual marks tools succubus cannot configure for you.
	Manual bool
}

// knownTools is the full roster, in the order the summary should list them.
var knownTools = []Tool{
	{
		ID: "claude", Name: "Claude Code",
		HasHooks: true, HasMCP: true,
	},
	{
		ID: "droid", Name: "Factory Droid",
		HasHooks: true, HasMCP: true,
		Notes: "MCP is registered separately with: droid mcp add",
	},
	{
		ID: "codex", Name: "Codex CLI",
		HasHooks: true, HasMCP: true,
		Notes: "hooks are experimental and need [features] hooks = true; not on Windows",
	},
	{
		ID: "gemini", Name: "Gemini CLI",
		HasHooks: true, HasMCP: true,
		Notes: "project-local config, written to .gemini/settings.json",
	},
	{
		ID: "opencode", Name: "OpenCode",
		HasMCP: true, NeedsPlugin: true,
		Notes: "no shell hooks; a plugin is copied into .opencode/plugin/",
	},
	{
		ID: "cursor", Name: "Cursor CLI",
		HasMCP: true,
		Notes:  "the CLI fires almost no hook events, so this is MCP only",
	},
	{
		ID: "copilot", Name: "GitHub Copilot CLI",
		HasMCP: true,
		Notes:  "no hook system; MCP and the skill only",
	},
	{
		ID: "aider", Name: "Aider",
		Manual: true,
		Notes:  "no MCP and no hooks — add `read: [AGENTS.md]` to .aider.conf.yml yourself",
	},
}

// detectors maps a tool id to the binaries and config directories that prove it
// is installed. A config directory counts on its own: several of these tools
// install their binary somewhere not on the shell PATH succubus inherits.
var detectors = map[string]struct {
	bins []string
	dirs []string
}{
	"claude":   {bins: []string{"claude"}, dirs: []string{".claude"}},
	"droid":    {bins: []string{"droid"}, dirs: []string{".factory"}},
	"codex":    {bins: []string{"codex"}, dirs: []string{".codex"}},
	"gemini":   {bins: []string{"gemini", "gemini-cli"}, dirs: []string{".gemini"}},
	"opencode": {bins: []string{"opencode"}, dirs: []string{".config/opencode", ".opencode"}},
	"cursor":   {bins: []string{"cursor-agent", "cursor"}, dirs: []string{".cursor"}},
	"copilot":  {bins: []string{"copilot"}, dirs: []string{".copilot"}},
	"aider":    {bins: []string{"aider"}, dirs: []string{".aider.conf.yml"}},
}

// DetectTools reports which agent runtimes are present on this machine.
func DetectTools() []Tool {
	home, _ := os.UserHomeDir()

	out := make([]Tool, 0, len(knownTools))
	for _, t := range knownTools {
		d := detectors[t.ID]

		for _, b := range d.bins {
			if p, err := exec.LookPath(b); err == nil {
				t.Installed, t.Evidence = true, p
				break
			}
		}
		if !t.Installed && home != "" {
			for _, dir := range d.dirs {
				// The detector table uses forward slashes; convert so Windows
				// stats a real path.
				full := filepath.Join(home, filepath.FromSlash(dir))
				if _, err := os.Stat(full); err == nil {
					t.Installed, t.Evidence = true, filepath.Join("~", filepath.FromSlash(dir))
					break
				}
			}
		}

		// Codex hooks do not exist on Windows; say so up front.
		if t.ID == "codex" && runtime.GOOS == "windows" {
			t.HasHooks = false
			t.Notes = "hooks are unavailable on Windows; MCP only"
		}
		out = append(out, t)
	}
	return out
}

// InstalledIDs returns the ids of tools succubus can configure automatically.
func InstalledIDs(tools []Tool) []string {
	var ids []string
	for _, t := range tools {
		if t.Installed && !t.Manual {
			ids = append(ids, t.ID)
		}
	}
	return ids
}
