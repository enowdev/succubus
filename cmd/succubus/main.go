// Command succubus is the cross-agent coordination layer: one binary that runs
// as the daemon, as an MCP stdio server, as a hook handler, and as a CLI.
package main

import (
	"fmt"
	"os"

	"github.com/enowdev/succubus/internal/mode"
	"github.com/enowdev/succubus/web"
)

// version comes from internal/mode so the CLI and the MCP handshake cannot
// disagree about what is running. The Makefile sets it at link time.
var version = mode.Version

const usage = `succubus — shared plan, tasks, and file claims for multiple AI coding agents

Getting started:
  succubus setup               Detect your coding tools and configure them all.
  succubus service install     Run the daemon in the background, every login.
  succubus doctor              Check an existing installation.

Usage:
  succubus setup [--yes] [--dry-run] [--tools LIST]
        Find every agent tool on this machine, show what will change, and wire
        them all up. No elevated privileges needed — everything lives in your
        home directory or this project.

  succubus daemon [--addr host:port] [--db path] [--dev]
        Run the coordination daemon in the foreground (HTTP API + SSE + dashboard).

  succubus service <install|status|restart|uninstall>
        Run the daemon in the background and start it at login. Uses launchd on
        macOS, a systemd user unit on Linux, and the per-user Run key on
        Windows. No administrator rights required.

  succubus mcp [--tool NAME] [--session KEY]
        Run as an MCP stdio server. Point your agent's MCP config at this.

  succubus hook <event> [--dialect NAME]
        Handle a lifecycle hook. Reads the tool's JSON payload on stdin.

  succubus init [--tools all] [--dry-run]
        The lower-level form of setup: writes config without the summary or
        the confirmation prompt.

  succubus status              Show project, agents, tasks, and claims.
  succubus agents              List agents registered in this project.
  succubus tasks [--status S]  List tasks.
  succubus plans               List plans.
  succubus claims              List active file claims.
  succubus claim <path>...     Claim files as this session's agent.
  succubus release <path>...   Release files (use --all for everything).
  succubus whoami              Show this session's adopted identity.
  succubus projects            List every project succubus knows about.
  succubus forget [id]         Delete a project's record. Files are untouched.
  succubus doctor              Diagnose a configured installation.

  succubus notify <add|list|remove|test> [url]
        Send room messages and handoffs to Slack, Discord, or any webhook.
        These notify *you* — agents read the room on their next turn either
        way, since they have no process running in between.

Environment:
  SUCCUBUS_ADDR      daemon address (default 127.0.0.1:7801)
  SUCCUBUS_SESSION   session key used to resolve identity
  SUCCUBUS_TOOL      agent tool name (claude-code, opencode, codex, …)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}

	// Hand the embedded dashboard to the daemon, if this build has one.
	if dist, ok := web.Dist(); ok {
		mode.SPAFS = dist
	}

	cmd, args := os.Args[1], os.Args[2:]
	var err error

	switch cmd {
	case "daemon", "serve":
		err = mode.Daemon(args)
	case "mcp":
		err = mode.MCP(args)
	case "hook":
		err = mode.Hook(args)
	case "setup":
		err = mode.Setup(args)
	case "service":
		err = mode.Service(args)
	case "wake":
		err = mode.Wake(args)
	case "notify":
		err = mode.Notify(args)
	case "doctor":
		err = mode.Doctor(args)
	case "init":
		err = mode.Init(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	case "version", "--version":
		fmt.Println("succubus " + version)
		return
	default:
		err = mode.CLI(cmd, args)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "succubus: %v\n", err)
		os.Exit(1)
	}
}
