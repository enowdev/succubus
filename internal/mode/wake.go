package mode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/store"
)

// Waking an agent from outside its session.
//
// An interactive session cannot be pushed to: it has no endpoint, and nothing
// runs between its turns. But every one of these tools also has a headless
// mode — `claude -p`, `codex exec`, `opencode run` — which starts a fresh,
// short-lived turn from the command line.
//
// So a message addressed to an agent does not have to wait. succubus can spawn
// a headless turn on that agent's behalf, in that agent's project, carrying the
// message. The reply lands back in the room.
//
// The limitation this does *not* remove: the headless turn is a new process
// with no memory of the interactive session's conversation. It knows what
// succubus tells it — the plan, the tasks, the room — which for answering a
// question is usually enough.

// wakeRunner describes how to start a headless turn for one tool.
type wakeRunner struct {
	// bin is the executable to look for on PATH.
	bin string
	// args builds the command line for a prompt.
	args func(prompt string) []string
}

// succubusTools are the MCP tools a woken turn needs. A headless run has no one
// to answer a permission prompt, so anything not pre-approved is simply denied
// and the turn ends without replying.
var succubusTools = []string{
	"mcp__succubus__succubus_context",
	"mcp__succubus__succubus_room",
	"mcp__succubus__succubus_say",
	"mcp__succubus__succubus_resolve",
	"mcp__succubus__succubus_task_create",
	"mcp__succubus__succubus_task_update",
	"mcp__succubus__succubus_whoami",
}

// runners covers the tools that expose a non-interactive mode. Anything absent
// here cannot be woken, and succubus says so rather than pretending.
var runners = map[string]wakeRunner{
	"claude-code": {
		bin: "claude",
		args: func(p string) []string {
			return []string{
				"-p", p,
				"--output-format", "text",
				// A headless run does not load the user's MCP servers, so the
				// succubus tools have to be handed to it directly.
				"--mcp-config", mcpConfigPath(),
				// And without an explicit allow-list every call is denied,
				// since there is no human to approve one.
				"--allowedTools", strings.Join(succubusTools, ","),
			}
		},
	},
	"codex": {
		bin:  "codex",
		args: func(p string) []string { return []string{"exec", p} },
	},
	"opencode": {
		bin:  "opencode",
		args: func(p string) []string { return []string{"run", p} },
	},
	"droid": {
		bin:  "droid",
		args: func(p string) []string { return []string{"exec", p} },
	},
}

// mcpConfigPath is a minimal MCP config pointing at this binary, written on
// demand. A headless run loads none of the user's configured servers, so the
// succubus tools have to travel with the invocation.
func mcpConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".succubus", "mcp.json")

	self, err := os.Executable()
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"succubus": map[string]any{
				"command": self,
				"args":    []string{"mcp"},
			},
		},
	})
	if err != nil {
		return path
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, body, 0o644)
	return path
}

// WakeableTools lists the tools this machine can actually wake.
func WakeableTools() []string {
	var out []string
	for tool, r := range runners {
		if _, err := exec.LookPath(r.bin); err == nil {
			out = append(out, tool)
		}
	}
	return out
}

// CanWake reports whether an agent's tool supports headless invocation here.
func CanWake(tool string) bool {
	r, ok := runners[tool]
	if !ok {
		return false
	}
	_, err := exec.LookPath(r.bin)
	return err == nil
}

// wakePrompt is what the headless turn is asked to do. It is deliberately
// narrow: answer the message, then stop. A woken agent that starts refactoring
// would be worse than one that never woke.
func wakePrompt(agentName string, msgs []store.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s. You have been woken by succubus because "+
		"you were addressed by name in the agent room.\n\n", agentName)

	b.WriteString("Messages waiting for you:\n")
	for _, m := range msgs {
		fmt.Fprintf(&b, "  - %s: %s\n    (reply_to %s)\n", m.AuthorName, m.BodyMD, m.ID)
	}

	b.WriteString("\nDo exactly this:\n")
	b.WriteString("1. Call succubus_context to see the current plan, tasks, and locks.\n")
	b.WriteString("2. Answer each message with succubus_say, setting reply_to.\n")
	b.WriteString("3. Stop.\n\n")
	b.WriteString("If a message asks you to do work, do not start it in this turn — " +
		"reply saying what you will do, and record it with succubus_task_create so " +
		"it is on the board. This is a short turn for answering, not for building.\n")
	return b.String()
}

// Wake handles `succubus wake`, both by hand and from the daemon.
func Wake(args []string) error {
	fs := flag.NewFlagSet("wake", flag.ExitOnError)
	agentName := fs.String("agent", "", "agent to wake (defaults to every agent with unread mentions)")
	dry := fs.Bool("dry-run", false, "show what would run, without running it")
	timeout := fs.Duration("timeout", 3*time.Minute, "how long to let a woken turn run")
	fs.Parse(args)

	sess, err := OpenSession("", false)
	if err != nil {
		var down *client.ErrDaemonDown
		if errors.As(err, &down) {
			return errors.New("daemon not running — start it with: succubus service install")
		}
		return err
	}
	c, pid := sess.Client, sess.Project.ID

	agents, err := c.ListAgents(pid)
	if err != nil {
		return err
	}

	woke := 0
	for _, a := range agents {
		if *agentName != "" && !strings.EqualFold(a.Name, *agentName) {
			continue
		}
		if a.Status == store.AgentDead {
			continue
		}

		ctx, err := c.Context(pid, a.ID)
		if err != nil {
			continue
		}
		var direct []store.Message
		for _, m := range ctx.RoomMentions {
			if m.DirectMention {
				direct = append(direct, m)
			}
		}
		if len(direct) == 0 {
			if *agentName != "" {
				fmt.Printf("%s has nothing waiting.\n", a.Name)
			}
			continue
		}

		if !CanWake(a.Tool) {
			fmt.Printf("  – %s (%s): %d message(s) waiting, but %s has no headless mode "+
				"on this machine — it will read them on its next turn.\n",
				a.Name, a.Tool, len(direct), a.Tool)
			continue
		}

		if *dry {
			r := runners[a.Tool]
			fmt.Printf("  would run: %s %s\n    in %s\n",
				r.bin, strings.Join(r.args("<prompt>"), " "), sess.Project.RootPath)
			continue
		}

		fmt.Printf("  waking %s (%s)…\n", a.Name, a.Tool)
		if err := runWake(a, sess.Project.RootPath, direct, *timeout); err != nil {
			fmt.Printf("    failed: %v\n", err)
			continue
		}
		woke++
	}

	if woke == 0 && *agentName == "" && !*dry {
		fmt.Println("Nothing to wake — no agent has an unanswered mention.")
	}
	return nil
}

// runWake starts one headless turn and waits for it.
func runWake(a store.Agent, root string, msgs []store.Message, timeout time.Duration) error {
	r, ok := runners[a.Tool]
	if !ok {
		return fmt.Errorf("no headless runner for %s", a.Tool)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.bin, r.args(wakePrompt(a.Name, msgs))...)
	cmd.Dir = root
	// The woken turn speaks *as* the agent it is standing in for, so its reply
	// lands under the right name. It shares the session key too, which keeps
	// the identity single rather than spawning a second agent — registration is
	// idempotent on that key.
	cmd.Env = append(os.Environ(),
		"SUCCUBUS_SESSION="+a.SessionKey,
		"SUCCUBUS_TOOL="+a.Tool,
		"SUCCUBUS_WOKEN=1",
	)

	// Headless runs wait on stdin otherwise, costing a few seconds per wake.
	cmd.Stdin = nil

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timed out after %s", timeout)
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(trimLine(out.String(), 200)))
	}
	return nil
}
