package mode

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/succubus/assets"
	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/identity"
)

// Setup is the one-command path: detect the agent tools on this machine, show
// what will change, ask once, then write everything and verify it.
//
// Nothing here needs elevated privileges. Every file lives under the user's
// home directory or inside the project — if something asks for sudo, it is
// pointed at the wrong place.
func Setup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	dry := fs.Bool("dry-run", false, "show what would change, write nothing")
	only := fs.String("tools", "", "restrict to these tools (comma-separated)")
	noStart := fs.Bool("no-start", false, "do not offer to start the daemon")
	fs.Parse(args)

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}

	cwd, _ := os.Getwd()
	proj := identity.ResolveProject(cwd)

	// Fail before printing a plan we are not going to be able to carry out.
	if err := checkProjectRoot(proj.RootPath, *dry); err != nil {
		return err
	}

	tools := DetectTools()

	// A --tools filter narrows what we touch, but detection still runs so the
	// summary can say what was skipped and why.
	var restrict map[string]bool
	if *only != "" {
		restrict = map[string]bool{}
		for _, t := range strings.Split(*only, ",") {
			restrict[strings.TrimSpace(t)] = true
		}
	}

	fmt.Printf("\n  succubus setup\n")
	fmt.Printf("  %s\n\n", strings.Repeat("─", 56))
	fmt.Printf("  project   %s\n", proj.DisplayName)
	fmt.Printf("  path      %s", proj.RootPath)
	if proj.RootPath != cwd {
		// Worth stating: project files follow the repository root, not the
		// subdirectory you happen to be standing in.
		fmt.Printf("  %s", dim("(repository root, not your cwd)"))
	}
	fmt.Printf("\n  binary    %s\n\n", bin)

	var willConfigure []Tool
	var manual []Tool
	var missing []Tool

	for _, t := range tools {
		switch {
		case !t.Installed:
			missing = append(missing, t)
		case t.Manual:
			manual = append(manual, t)
		case restrict != nil && !restrict[t.ID]:
			// Explicitly excluded by --tools; not worth listing as missing.
		default:
			willConfigure = append(willConfigure, t)
		}
	}

	if len(willConfigure) == 0 {
		fmt.Println("  No supported agent tools were found on this machine.")
		fmt.Println()
		fmt.Println("  succubus looks for: Claude Code, Factory Droid, Codex CLI,")
		fmt.Println("  Gemini CLI, OpenCode, Cursor CLI, and GitHub Copilot CLI.")
		fmt.Println()
		fmt.Println("  You can still use the CLI and the dashboard:")
		fmt.Println("      succubus daemon")
		return nil
	}

	fmt.Printf("  Found %d tool%s:\n\n", len(willConfigure), plural(len(willConfigure)))
	for _, t := range willConfigure {
		fmt.Printf("    %-20s %s\n", t.Name, capabilities(t))
		fmt.Printf("    %-20s %s\n", "", dim(t.Evidence))
		if t.Notes != "" {
			fmt.Printf("    %-20s %s\n", "", dim("note: "+t.Notes))
		}
		fmt.Println()
	}

	fmt.Println("  Also written, for tools with no hook support:")
	fmt.Printf("    %-20s %s\n", "AGENTS.md", dim("contract block, merged into any existing file"))
	fmt.Printf("    %-20s %s\n\n", ".agents/skills/", dim("the succubus Agent Skill"))

	if len(manual) > 0 {
		fmt.Println("  Installed, but cannot be configured automatically:")
		for _, t := range manual {
			fmt.Printf("    %-20s %s\n", t.Name, dim(t.Notes))
		}
		fmt.Println()
	}

	if len(missing) > 0 && restrict == nil {
		names := make([]string, 0, len(missing))
		for _, t := range missing {
			names = append(names, t.Name)
		}
		fmt.Printf("  Not installed: %s\n\n", dim(strings.Join(names, ", ")))
	}

	fmt.Println("  Existing config is merged, not replaced. The first time succubus")
	fmt.Println("  edits a file it leaves a .succubus-bak copy beside it.")
	fmt.Println()

	if *dry {
		fmt.Println("  Dry run — nothing was written.")
		fmt.Println("  Run without --dry-run to apply, or `succubus init --dry-run` to")
		fmt.Println("  see the exact file contents.")
		return nil
	}

	if !*yes && !confirmTTY("  Configure these tools now?") {
		fmt.Println("\n  Cancelled. Nothing was written.")
		return nil
	}
	fmt.Println()

	// Reuse init, which owns the actual merge logic for each tool.
	ids := make([]string, 0, len(willConfigure))
	for _, t := range willConfigure {
		ids = append(ids, t.ID)
	}
	// init owns every file write, including the plugin and the skill, so that
	// `succubus init` on its own produces a complete installation too.
	if err := Init([]string{"--tools", strings.Join(ids, ",")}); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("  %s\n\n", strings.Repeat("─", 56))

	// Finish by telling them whether the thing is actually running.
	c := client.New(DefaultAddr(), 2*time.Second)
	running := c.Health() == nil

	if running {
		fmt.Println("  The daemon is already running.")
		fmt.Printf("  Dashboard: http://%s\n\n", DefaultAddr())
		fmt.Println("  Restart your agent sessions so they pick up the new config.")
		return nil
	}

	if *noStart {
		fmt.Println("  Next: start the daemon and leave it running.")
		fmt.Println("      succubus daemon")
		return nil
	}

	// Coordination only works while the daemon is up, so offer to make that
	// automatic rather than leaving it as a thing to remember.
	fmt.Println("  The daemon is not running yet. It needs to stay running for")
	fmt.Println("  agents to coordinate at all.")
	fmt.Println()

	if *yes || confirmTTY("  Start it now, and every time you log in?") {
		fmt.Println()
		if err := serviceInstall(bin, "", ""); err != nil {
			fmt.Printf("  – could not install autostart: %v\n", err)
			fmt.Println("\n  Start it by hand instead:")
			fmt.Println("      succubus daemon")
			return nil
		}
		fmt.Print("\n  Waiting for the daemon…")
		for i := range 20 {
			if c.Health() == nil {
				fmt.Printf("\r  ✓ daemon running at http://%s\n\n", DefaultAddr())
				fmt.Println("  Restart your agent sessions so they pick up the new config.")
				return nil
			}
			if i == 19 {
				fmt.Printf("\r  ! not answering yet — check %s\n", serviceLogPath())
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil
	}

	fmt.Println()
	fmt.Println("  Start it yourself with:")
	fmt.Println("      succubus daemon              # foreground")
	fmt.Println("      succubus service install     # background, every login")
	fmt.Println()
	fmt.Printf("  Then open http://%s and restart your agent sessions.\n", DefaultAddr())
	return nil
}

// installOpenCodePlugin copies the bundled plugin into the project, since
// OpenCode has no hook system to configure.
func installOpenCodePlugin(root string) error {
	dir := filepath.Join(root, ".opencode", "plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "succubus.ts"), []byte(assets.OpenCodePlugin()), 0o644)
}

// installSkill writes the Agent Skill to the vendor-neutral location, which
// Claude Code, Copilot CLI, OpenCode, Codex, Gemini, Cursor, and Factory read.
func installSkill(root string) error {
	dir := filepath.Join(root, ".agents", "skills", "succubus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(assets.Skill()), 0o644)
}

// confirmTTY asks a yes/no question. It answers itself with "no" when there is
// no terminal, so a scripted run never hangs waiting on stdin.
func confirmTTY(prompt string) bool {
	info, err := os.Stdin.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		fmt.Println(prompt + " [y/N] (no terminal — assuming no; pass --yes to proceed)")
		return false
	}
	fmt.Printf("%s [Y/n] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

func capabilities(t Tool) string {
	var parts []string
	if t.HasHooks {
		parts = append(parts, "hooks")
	}
	if t.HasMCP {
		parts = append(parts, "mcp")
	}
	if t.NeedsPlugin {
		parts = append(parts, "plugin")
	}
	if len(parts) == 0 {
		return "manual"
	}
	return strings.Join(parts, " + ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// dim wraps text in ANSI dim when stdout is a terminal, and leaves it alone
// when output is being piped or captured.
func dim(s string) string {
	if s == "" {
		return ""
	}
	if info, err := os.Stdout.Stat(); err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		return s
	}
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// Doctor checks a live installation and reports what is wrong.
func Doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fs.Parse(args)

	cwd, _ := os.Getwd()
	proj := identity.ResolveProject(cwd)
	var problems int

	fmt.Printf("\n  succubus doctor\n")
	fmt.Printf("  %s\n\n", strings.Repeat("─", 56))

	// 1. Daemon.
	c := client.New(DefaultAddr(), 2*time.Second)
	if err := c.Health(); err != nil {
		problems++
		fmt.Printf("  ✗ daemon      not reachable at %s\n", DefaultAddr())
		fmt.Printf("                start it with: succubus service install\n")
	} else {
		fmt.Printf("  ✓ daemon      running at %s\n", DefaultAddr())
		// A daemon running only because someone started it by hand will be
		// gone after the next reboot, which is worth flagging.
		if !autostartInstalled() {
			fmt.Printf("  ! autostart   not installed — it will not come back after a reboot\n")
			fmt.Printf("                succubus service install\n")
		} else {
			fmt.Printf("  ✓ autostart   installed\n")
		}
	}

	// 2. Project.
	fmt.Printf("  ✓ project     %s (%s)\n", proj.DisplayName, proj.ID)

	// 3. Tools, and whether each one's config mentions succubus.
	home, _ := os.UserHomeDir()
	configs := map[string]string{
		"claude":   filepath.Join(home, ".claude", "settings.json"),
		"droid":    filepath.Join(home, ".factory", "hooks.json"),
		"codex":    filepath.Join(home, ".codex", "hooks.json"),
		"gemini":   filepath.Join(proj.RootPath, ".gemini", "settings.json"),
		"cursor":   filepath.Join(proj.RootPath, ".cursor", "mcp.json"),
		"opencode": filepath.Join(proj.RootPath, "opencode.json"),
		"copilot":  filepath.Join(home, ".copilot", "mcp-config.json"),
	}

	fmt.Println()
	for _, t := range DetectTools() {
		if !t.Installed {
			continue
		}
		if t.Manual {
			fmt.Printf("  – %-11s installed; configure by hand (%s)\n", t.ID, t.Notes)
			continue
		}
		path, ok := configs[t.ID]
		if !ok {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			problems++
			fmt.Printf("  ✗ %-11s not configured — run: succubus setup\n", t.ID)
			continue
		}
		if !strings.Contains(string(b), "succubus") {
			problems++
			fmt.Printf("  ✗ %-11s config exists but has no succubus entry\n", t.ID)
			continue
		}

		// MCP alone is not enough for OpenCode: without the plugin it has no
		// lifecycle events at all, so registration and blocking never happen.
		if t.NeedsPlugin {
			p := filepath.Join(proj.RootPath, ".opencode", "plugin", "succubus.ts")
			if _, err := os.Stat(p); err != nil {
				problems++
				fmt.Printf("  ✗ %-11s mcp configured, but the plugin is missing — run: succubus setup\n", t.ID)
				continue
			}
			fmt.Printf("  ✓ %-11s configured (mcp + plugin)\n", t.ID)
			continue
		}
		fmt.Printf("  ✓ %-11s configured\n", t.ID)
	}

	// 4. Portable surfaces.
	fmt.Println()
	if b, err := os.ReadFile(filepath.Join(proj.RootPath, "AGENTS.md")); err == nil &&
		strings.Contains(string(b), "succubus:begin") {
		fmt.Println("  ✓ AGENTS.md   contract block present")
	} else {
		problems++
		fmt.Println("  ✗ AGENTS.md   missing the succubus contract block")
	}
	if _, err := os.Stat(filepath.Join(proj.RootPath, ".agents", "skills", "succubus", "SKILL.md")); err == nil {
		fmt.Println("  ✓ skill       installed")
	} else {
		problems++
		fmt.Println("  ✗ skill       not installed")
	}

	// 5. Is the binary reachable by name? Agent tools rarely inherit PATH,
	//    which is the single most common reason MCP silently does nothing.
	fmt.Println()
	if _, err := exec.LookPath("succubus"); err != nil {
		fmt.Println("  ! succubus is not on PATH. That is fine — the config written by")
		fmt.Println("    `succubus setup` uses an absolute path — but installing it makes")
		fmt.Println("    the CLI easier to use:  make install")
	} else {
		fmt.Println("  ✓ binary      on PATH")
	}

	fmt.Printf("\n  %s\n", strings.Repeat("─", 56))
	if problems == 0 {
		fmt.Println("  Everything checks out.")
		return nil
	}
	fmt.Printf("  %d problem%s found. Most are fixed by: succubus setup\n", problems, plural(problems))
	return errors.New("configuration incomplete")
}
