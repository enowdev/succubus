package mode

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/succubus/assets"
	"github.com/enowdev/succubus/internal/identity"
)

// Init wires succubus into the agent tools installed on this machine.
//
// It writes hook configs and MCP entries, merging into existing settings rather
// than overwriting them — these files belong to the user, not to us.
func Init(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	tools := fs.String("tools", "all", "comma-separated: claude,droid,codex,gemini,cursor,opencode,all")
	dry := fs.Bool("dry-run", false, "show what would change without writing")
	fs.Parse(args)

	binPath, err := os.Executable()
	if err != nil {
		return err
	}
	binPath, _ = filepath.EvalSymlinks(binPath)

	cwd, _ := os.Getwd()
	proj := identity.ResolveProject(cwd)

	if err := checkProjectRoot(proj.RootPath, *dry); err != nil {
		return err
	}

	fmt.Printf("succubus init\n  project: %s (%s)\n  binary:  %s\n\n",
		proj.DisplayName, proj.ID, binPath)
	if proj.RootPath != cwd {
		// Project-scoped files follow the repository root, not the directory
		// you happen to be standing in.
		fmt.Printf("  writing project files to the repository root: %s\n\n", proj.RootPath)
	}

	want := map[string]bool{}
	for _, t := range strings.Split(*tools, ",") {
		want[strings.TrimSpace(t)] = true
	}
	enabled := func(name string) bool { return want["all"] || want[name] }

	home, _ := os.UserHomeDir()
	var done, skipped []string

	if enabled("claude") {
		if err := initClaude(home, binPath, *dry); err != nil {
			skipped = append(skipped, "claude: "+err.Error())
		} else {
			done = append(done, "claude-code (hooks + mcp)")
		}
		// Needed for channels: the flag that enables them resolves server names
		// against a project .mcp.json, not the user-level config.
		if err := initClaudeProjectMCP(proj.RootPath, binPath, *dry); err != nil {
			skipped = append(skipped, "claude .mcp.json: "+err.Error())
		} else {
			done = append(done, ".mcp.json (enables live channel delivery)")
		}
	}
	if enabled("droid") {
		if err := initDroid(home, binPath, *dry); err != nil {
			skipped = append(skipped, "droid: "+err.Error())
		} else {
			done = append(done, "factory droid (hooks)")
		}
	}
	if enabled("codex") {
		if err := initCodex(home, binPath, *dry); err != nil {
			skipped = append(skipped, "codex: "+err.Error())
		} else {
			done = append(done, "codex cli (hooks)")
		}
	}
	if enabled("gemini") {
		if err := initGemini(proj.RootPath, binPath, *dry); err != nil {
			skipped = append(skipped, "gemini: "+err.Error())
		} else {
			done = append(done, "gemini cli (hooks)")
		}
	}
	if enabled("cursor") {
		if err := initCursor(proj.RootPath, binPath, *dry); err != nil {
			skipped = append(skipped, "cursor: "+err.Error())
		} else {
			done = append(done, "cursor (mcp)")
		}
	}
	if enabled("opencode") {
		if err := initOpenCode(proj.RootPath, binPath, *dry); err != nil {
			skipped = append(skipped, "opencode: "+err.Error())
		} else {
			done = append(done, "opencode (mcp)")
		}
		// OpenCode has no shell hooks, so without the plugin it gets no
		// registration, no nagging, and no blocking — MCP alone.
		if *dry {
			fmt.Printf("--- %s ---\n(plugin source, %d bytes)\n\n",
				filepath.Join(proj.RootPath, ".opencode", "plugin", "succubus.ts"),
				len(assets.OpenCodePlugin()))
			done = append(done, "opencode plugin")
		} else if err := installOpenCodePlugin(proj.RootPath); err != nil {
			skipped = append(skipped, "opencode plugin: "+err.Error())
		} else {
			done = append(done, "opencode plugin (.opencode/plugin/succubus.ts)")
		}
	}
	if enabled("copilot") {
		if err := initCopilot(home, binPath, *dry); err != nil {
			skipped = append(skipped, "copilot: "+err.Error())
		} else {
			done = append(done, "copilot (mcp)")
		}
	}

	// These two are not per-tool: they reach every agent, including the ones
	// with no hook or MCP support at all, so they are always written.
	if err := initAgentsMD(proj.RootPath, *dry); err != nil {
		skipped = append(skipped, "AGENTS.md: "+err.Error())
	} else {
		done = append(done, "AGENTS.md contract block")
	}
	if *dry {
		fmt.Printf("--- %s ---\n(skill, %d bytes)\n\n",
			filepath.Join(proj.RootPath, ".agents", "skills", "succubus", "SKILL.md"),
			len(assets.Skill()))
		done = append(done, "Agent Skill")
	} else if err := installSkill(proj.RootPath); err != nil {
		skipped = append(skipped, "Agent Skill: "+err.Error())
	} else {
		done = append(done, "Agent Skill (.agents/skills/succubus/SKILL.md)")
	}

	fmt.Println("configured:")
	for _, d := range done {
		fmt.Printf("  ✓ %s\n", d)
	}
	if len(skipped) > 0 {
		fmt.Println("\nskipped:")
		for _, s := range skipped {
			fmt.Printf("  – %s\n", s)
		}
	}
	if *dry {
		fmt.Println("\n(dry run — nothing was written)")
	} else {
		fmt.Println("\nStart the daemon with:  succubus daemon")
	}
	return nil
}

// mergeJSONFile reads a JSON file, applies mutate, and writes it back
// atomically. Missing files start as an empty object.
func mergeJSONFile(path string, dry bool, mutate func(m map[string]any)) error {
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	mutate(m)

	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if dry {
		fmt.Printf("--- %s ---\n%s\n\n", path, b)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Back up before the first rewrite so a bad merge is recoverable.
	if _, err := os.Stat(path); err == nil {
		if _, err := os.Stat(path + ".succubus-bak"); os.IsNotExist(err) {
			if orig, err := os.ReadFile(path); err == nil {
				os.WriteFile(path+".succubus-bak", orig, 0o644)
			}
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hookEntry builds one Claude/Droid-shaped hook matcher block.
func hookEntry(bin, event, matcher string, timeout int) map[string]any {
	h := map[string]any{
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": fmt.Sprintf("%q hook %s", bin, event),
			"timeout": timeout,
		}},
	}
	if matcher != "" {
		h["matcher"] = matcher
	}
	return h
}

// appendHook adds our hook to an event without disturbing hooks already there.
func appendHook(m map[string]any, event string, entry map[string]any, bin string) {
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		m["hooks"] = hooks
	}
	existing, _ := hooks[event].([]any)

	// Drop any previous succubus entry so re-running init is idempotent.
	kept := []any{}
	for _, e := range existing {
		if !mentionsBinary(e, bin) {
			kept = append(kept, e)
		}
	}
	hooks[event] = append(kept, entry)
}

func mentionsBinary(entry any, bin string) bool {
	b, err := json.Marshal(entry)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, bin) || strings.Contains(s, "succubus")
}

// initClaudeProjectMCP writes a project-level .mcp.json.
//
// This is not redundant with the user-level MCP entry. Channels — the mechanism
// that pushes a room message into a *live* session — are enabled with
// `--dangerously-load-development-channels server:succubus`, and that flag only
// resolves server names against a project .mcp.json or ~/.claude.json. A server
// supplied via --mcp-config is not found.
func initClaudeProjectMCP(root, bin string, dry bool) error {
	return mergeJSONFile(filepath.Join(root, ".mcp.json"), dry, func(m map[string]any) {
		servers, _ := m["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			m["mcpServers"] = servers
		}
		servers["succubus"] = map[string]any{
			"command": bin,
			"args":    []any{"mcp", "--tool", "claude-code"},
		}
	})
}

func initClaude(home, bin string, dry bool) error {
	path := filepath.Join(home, ".claude", "settings.json")
	return mergeJSONFile(path, dry, func(m map[string]any) {
		appendHook(m, "SessionStart", hookEntry(bin, "SessionStart", "startup|resume", 10), bin)
		appendHook(m, "UserPromptSubmit", hookEntry(bin, "UserPromptSubmit", "", 10), bin)
		appendHook(m, "PreToolUse", hookEntry(bin, "PreToolUse", "Edit|Write|MultiEdit|NotebookEdit", 5), bin)
		appendHook(m, "PostToolUse", hookEntry(bin, "PostToolUse", "Edit|Write|MultiEdit|NotebookEdit", 5), bin)
		// Stop is the last moment an agent can still act: it holds the turn
		// open when a message addressed to it has gone unanswered.
		appendHook(m, "Stop", hookEntry(bin, "Stop", "", 5), bin)
		appendHook(m, "SessionEnd", hookEntry(bin, "SessionEnd", "", 5), bin)

		servers, _ := m["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			m["mcpServers"] = servers
		}
		servers["succubus"] = map[string]any{
			"command": bin,
			"args":    []any{"mcp", "--tool", "claude-code"},
		}
	})
}

func initDroid(home, bin string, dry bool) error {
	dir := filepath.Join(home, ".factory")
	if _, err := os.Stat(dir); os.IsNotExist(err) && !dry {
		return fmt.Errorf("~/.factory not found — is Factory Droid installed?")
	}
	return mergeJSONFile(filepath.Join(dir, "hooks.json"), dry, func(m map[string]any) {
		appendHook(m, "SessionStart", hookEntry(bin, "SessionStart", "", 10), bin)
		appendHook(m, "UserPromptSubmit", hookEntry(bin, "UserPromptSubmit", "", 10), bin)
		appendHook(m, "PreToolUse", hookEntry(bin, "PreToolUse", "Edit|Write", 5), bin)
		appendHook(m, "PostToolUse", hookEntry(bin, "PostToolUse", "Edit|Write", 5), bin)
		appendHook(m, "Stop", hookEntry(bin, "Stop", "", 5), bin)
		appendHook(m, "SessionEnd", hookEntry(bin, "SessionEnd", "", 5), bin)
	})
}

func initCodex(home, bin string, dry bool) error {
	dir := filepath.Join(home, ".codex")
	if _, err := os.Stat(dir); os.IsNotExist(err) && !dry {
		return fmt.Errorf("~/.codex not found — is Codex CLI installed?")
	}
	return mergeJSONFile(filepath.Join(dir, "hooks.json"), dry, func(m map[string]any) {
		appendHook(m, "SessionStart", hookEntry(bin, "SessionStart", "", 10), bin)
		appendHook(m, "UserPromptSubmit", hookEntry(bin, "UserPromptSubmit", "", 10), bin)
		appendHook(m, "PreToolUse", hookEntry(bin, "PreToolUse", "", 5), bin)
		appendHook(m, "PostToolUse", hookEntry(bin, "PostToolUse", "", 5), bin)
		// Stop is the end of a *turn*, not the end of the session. Wiring it to
		// SessionEnd released every file claim the agent held each time it
		// stopped talking, mid-session — so a Codex agent kept losing the locks
		// it was still relying on, and never got the turn-end nudge to answer a
		// question addressed to it.
		appendHook(m, "Stop", hookEntry(bin, "Stop", "", 5), bin)
	})
}

// initGemini writes project-local settings: Gemini's event names differ enough
// that they get their own dialect flag.
func initGemini(root, bin string, dry bool) error {
	path := filepath.Join(root, ".gemini", "settings.json")
	cmd := func(event string) map[string]any {
		return map[string]any{"hooks": []any{map[string]any{
			"type":    "command",
			"command": fmt.Sprintf("%q hook %s --dialect gemini", bin, event),
			"timeout": 10,
		}}}
	}
	return mergeJSONFile(path, dry, func(m map[string]any) {
		appendHook(m, "SessionStart", cmd("SessionStart"), bin)
		appendHook(m, "BeforeAgent", cmd("BeforeAgent"), bin)
		appendHook(m, "BeforeTool", cmd("BeforeTool"), bin)
		appendHook(m, "AfterTool", cmd("AfterTool"), bin)
		appendHook(m, "SessionEnd", cmd("SessionEnd"), bin)
	})
}

// checkProjectRoot refuses to treat a directory that is obviously not a project
// as one.
//
// succubus writes AGENTS.md and .agents/ into the project root, so running it
// in a home directory or at the filesystem root would scatter files somewhere
// nobody expects — and register that location as a "project" in the daemon.
func checkProjectRoot(root string, dry bool) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	abs = filepath.Clean(abs)

	// The same rule the daemon applies when a hook registers a project, so a
	// directory that cannot be registered also cannot be configured.
	ok, why := identity.IsRegisterable(abs)
	if ok {
		return nil
	}

	switch why {
	case "filesystem root":
		return fmt.Errorf("refusing to run at the filesystem root (%s) — cd into your project first", abs)
	case "home directory":
		return fmt.Errorf(
			"refusing to run in your home directory (%s).\n"+
				"succubus writes AGENTS.md and .agents/ into the project root — cd into a project first",
			abs)
	}

	// A dry run is allowed to preview what would happen anywhere.
	if dry {
		return nil
	}
	return fmt.Errorf(
		"%s does not look like a project: no .git, and no go.mod, package.json,\n"+
			"Cargo.toml, pyproject.toml, or similar.\n"+
			"cd into your project, or run `succubus init --dry-run` to see what would be written",
		abs)
}

// initOpenCode registers the MCP server. The lifecycle side is a plugin file,
// copied into the project by `succubus setup` — OpenCode has no shell hooks.
func initOpenCode(root, bin string, dry bool) error {
	return mergeJSONFile(filepath.Join(root, "opencode.json"), dry, func(m map[string]any) {
		servers, _ := m["mcp"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			m["mcp"] = servers
		}
		servers["succubus"] = map[string]any{
			"type":    "local",
			"command": []any{bin, "mcp", "--tool", "opencode"},
			"enabled": true,
		}
	})
}

// initCopilot writes MCP config. Copilot CLI has no hooks, but it does read
// skills from .agents/skills, which setup installs.
func initCopilot(home, bin string, dry bool) error {
	path := filepath.Join(home, ".copilot", "mcp-config.json")
	return mergeJSONFile(path, dry, func(m map[string]any) {
		servers, _ := m["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			m["mcpServers"] = servers
		}
		servers["succubus"] = map[string]any{
			"command": bin, "args": []any{"mcp", "--tool", "copilot"},
		}
	})
}

// initCursor only wires MCP: the Cursor CLI fires almost none of the hook
// events the IDE documents, so hooks there would be a false promise.
func initCursor(root, bin string, dry bool) error {
	return mergeJSONFile(filepath.Join(root, ".cursor", "mcp.json"), dry, func(m map[string]any) {
		servers, _ := m["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
			m["mcpServers"] = servers
		}
		servers["succubus"] = map[string]any{
			"command": bin, "args": []any{"mcp", "--tool", "cursor"},
		}
	})
}

// initAgentsMD appends the contract to AGENTS.md, replacing a previous block.
// AGENTS.md is the widest-reach surface: tools with no hook support still read it.
func initAgentsMD(root string, dry bool) error {
	path := filepath.Join(root, "AGENTS.md")
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	}

	// Cut out a previous block so re-running replaces it instead of stacking
	// copies. Only a newline immediately after the end marker is consumed —
	// blindly dropping one character would eat whatever the user wrote there,
	// and would run past the end of a file that finishes on the marker.
	const beginMark, endMark = "<!-- succubus:begin -->", "<!-- succubus:end -->"
	if i := strings.Index(existing, beginMark); i >= 0 {
		if j := strings.Index(existing, endMark); j > i {
			rest := existing[j+len(endMark):]
			rest = strings.TrimPrefix(rest, "\r\n")
			rest = strings.TrimPrefix(rest, "\n")
			existing = existing[:i] + rest
		}
	}
	existing = strings.TrimRight(existing, "\n")
	block := assets.AgentsBlock()
	out := block
	if existing != "" {
		out = existing + "\n\n" + block
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}

	if dry {
		fmt.Printf("--- %s ---\n%s\n", path, out)
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644)
}
