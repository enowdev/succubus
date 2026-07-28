# Setting up succubus in your coding tools

succubus reaches an agent through up to four surfaces. Which ones are available
depends on the tool:

| Surface | What it does | Can an agent ignore it? |
|---|---|---|
| **MCP server** | Gives the agent tools it can call | Yes — it has to choose to call them |
| **Hooks** | Fire on session start, prompts, and tool use | **No** — the harness runs them |
| **AGENTS.md** | A contract block the agent reads as instructions | Yes, but it is always in context |
| **Agent Skill** | `SKILL.md` the agent loads on demand | Yes |

MCP alone is not enough — that is the whole reason hooks exist here. An agent
that never calls `succubus_register` is still registered *by the hook*, still
gets a name, and still has the coordination state injected into its context.

---

## Installing succubus

One binary with the dashboard embedded. No runtime dependencies, and no `sudo`
or Administrator required — it installs per user.

**macOS and Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/succubus/main/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://raw.githubusercontent.com/enowdev/succubus/main/install.ps1 | iex
```

Both verify the download against the release's published `checksums.txt` and
refuse to install if it does not match. Set `SUCCUBUS_VERSION` to pin a release,
or `SUCCUBUS_INSTALL_DIR` to install elsewhere.

If you would rather not pipe a script into a shell, download it, read it, then
run it — or build from source with `make install`.

---

## Quick start

```bash
cd /path/to/your/project
succubus init            # configures every tool it finds installed
succubus daemon          # leave running; open http://127.0.0.1:7801
```

`succubus init` merges into existing config rather than replacing it, backs up
each file once as `<file>.succubus-bak`, and is safe to re-run — it replaces its
own previous entries instead of stacking duplicates.

Useful flags:

```bash
succubus init --dry-run                 # print every change, write nothing
succubus init --tools claude,droid      # only these
```

Valid names: `claude`, `droid`, `codex`, `gemini`, `cursor`, `all`.

---

## Claude Code

**Full support: MCP, all five hook events, skill, AGENTS.md.**

`succubus init --tools claude` writes to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart":     [{ "matcher": "startup|resume",
      "hooks": [{ "type": "command", "command": "\"/path/to/succubus\" hook SessionStart", "timeout": 10 }] }],
    "UserPromptSubmit": [{
      "hooks": [{ "type": "command", "command": "\"/path/to/succubus\" hook UserPromptSubmit", "timeout": 10 }] }],
    "PreToolUse":       [{ "matcher": "Edit|Write|MultiEdit|NotebookEdit",
      "hooks": [{ "type": "command", "command": "\"/path/to/succubus\" hook PreToolUse", "timeout": 5 }] }],
    "PostToolUse":      [{ "matcher": "Edit|Write|MultiEdit|NotebookEdit",
      "hooks": [{ "type": "command", "command": "\"/path/to/succubus\" hook PostToolUse", "timeout": 5 }] }],
    "SessionEnd":       [{
      "hooks": [{ "type": "command", "command": "\"/path/to/succubus\" hook SessionEnd", "timeout": 5 }] }]
  },
  "mcpServers": {
    "succubus": { "command": "/path/to/succubus", "args": ["mcp", "--tool", "claude-code"] }
  }
}
```

What each event does:

| Event | Action |
|---|---|
| `SessionStart` | Registers the agent, assigns a name, injects the full state |
| `UserPromptSubmit` | Re-injects identity and state, heartbeats, renews leases |
| `PreToolUse` | Blocks an edit only if another live agent holds that file |
| `PostToolUse` | Warns if a file was edited without a claim, and claims it |
| `SessionEnd` | Releases everything the agent held |

Verify: start a session and ask *"what's my succubus identity?"* — it should
name itself without calling any tool, because `SessionStart` already told it.

---

## Factory Droid

**Full support.** Droid's hook schema is near-identical to Claude Code's, so the
same handler serves both.

`succubus init --tools droid` writes `~/.factory/hooks.json` with the same five
events. MCP is registered separately:

```bash
droid mcp add succubus -- /path/to/succubus mcp --tool droid
```

---

## Codex CLI

**Hooks are experimental**, gated behind a feature flag, and **not available on
Windows**.

Enable them in `~/.codex/config.toml`:

```toml
[features]
hooks = true

[mcp_servers.succubus]
command = "/path/to/succubus"
args = ["mcp", "--tool", "codex"]
```

Then `succubus init --tools codex` writes `~/.codex/hooks.json`. If hooks are not
enabled, MCP and AGENTS.md still work — you lose automatic registration and
blocking, not coordination itself.

---

## Gemini CLI

Gemini uses different event names, so its hooks get their own dialect flag.
`succubus init --tools gemini` writes `.gemini/settings.json` in the project:

```json
{
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command",
      "command": "\"/path/to/succubus\" hook SessionStart --dialect gemini", "timeout": 10 }] }],
    "BeforeAgent":  [{ "hooks": [{ "type": "command",
      "command": "\"/path/to/succubus\" hook BeforeAgent --dialect gemini", "timeout": 10 }] }],
    "BeforeTool":   [{ "hooks": [{ "type": "command",
      "command": "\"/path/to/succubus\" hook BeforeTool --dialect gemini", "timeout": 10 }] }],
    "AfterTool":    [{ "hooks": [{ "type": "command",
      "command": "\"/path/to/succubus\" hook AfterTool --dialect gemini", "timeout": 10 }] }],
    "SessionEnd":   [{ "hooks": [{ "type": "command",
      "command": "\"/path/to/succubus\" hook SessionEnd --dialect gemini", "timeout": 10 }] }]
  },
  "mcpServers": {
    "succubus": { "command": "/path/to/succubus", "args": ["mcp", "--tool", "gemini"] }
  }
}
```

Gemini treats **exit code 2 as a hard block**, which is what the handler emits on
a real conflict.

---

## Cursor CLI

**MCP only.** Cursor documents around twenty hook events, but `cursor-agent`
reportedly fires almost none of them — only shell-execution hooks. Promising
enforcement here would be a lie, so `succubus init --tools cursor` writes just
`.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "succubus": { "command": "/path/to/succubus", "args": ["mcp", "--tool", "cursor"] }
  }
}
```

Because there is no `SessionStart` hook to register the agent for you, Cursor
sessions must call `succubus_register` themselves. The AGENTS.md contract tells
them to.

---

## OpenCode

**OpenCode has no shell hooks at all** — its lifecycle events are only reachable
from a JavaScript plugin. succubus ships one, and `succubus setup` copies it to
`.opencode/plugin/succubus.ts` for you.

If you are wiring it up by hand, the plugin source lives at
`assets/opencode/succubus.ts` in the repository. Register the MCP server in
`opencode.json`:

```json
{
  "mcp": {
    "succubus": {
      "type": "local",
      "command": ["/path/to/succubus", "mcp", "--tool", "opencode"],
      "enabled": true
    }
  }
}
```

The plugin subscribes to `session.created`, `tool.execute.before`, and
`tool.execute.after`, and shells out to the same `succubus hook` handler — so
OpenCode gets the same registration, nagging, and blocking as Claude Code.

Set `SUCCUBUS_BIN` if the binary is not on `PATH`.

---

## GitHub Copilot CLI

**MCP and skills only** — no hook system.

`~/.copilot/mcp-config.json`:

```json
{
  "mcpServers": {
    "succubus": { "command": "/path/to/succubus", "args": ["mcp", "--tool", "copilot"] }
  }
}
```

Copilot CLI reads skills from `.github/skills`, `.claude/skills`, and
`.agents/skills` — so the bundled skill is found automatically. Coordination is
a soft contract here: the agent must choose to call the tools.

---

## Aider

**No MCP, no hooks.** Aider can only participate if you wire it in by hand:

```bash
aider --read AGENTS.md
```

Or in `.aider.conf.yml`:

```yaml
read:
  - AGENTS.md
```

The agent then knows the conventions, but cannot call succubus tools. In
practice, use the CLI alongside it:

```bash
succubus claim src/thing.go     # before you let Aider edit it
succubus release --all          # when done
```

---

## The portable surfaces

These reach every tool, including ones with no hook or MCP support.

### AGENTS.md

`succubus init` appends a contract block delimited by
`<!-- succubus:begin -->` / `<!-- succubus:end -->`, so re-running replaces it
without touching the rest of your file. AGENTS.md is read natively by Claude
Code, Codex, Cursor, Copilot, Gemini CLI, Factory, Zed, Windsurf, and others.

### Agent Skill

`.agents/skills/succubus/SKILL.md` is the vendor-neutral location, picked up by
Claude Code, Copilot CLI, OpenCode, Codex, Gemini CLI, Cursor, and Factory. Copy
it into your project:

```bash
mkdir -p .agents/skills
cp -r /path/to/succubus-repo/.agents/skills/succubus .agents/skills/
```

---

## Notifications

Webhooks run the other way from channels: agents telling *you* something. This
direction has no timing problem at all, because a person always has something
running.

```bash
succubus notify add https://hooks.slack.com/services/…
succubus notify add https://discord.com/api/webhooks/…   # format inferred
succubus notify list
```

By default only the events worth an interruption are sent: room messages,
handoffs, denied claims, and an agent leaving. Task and claim churn is constant
and would train you to ignore the channel.

This is usually more useful than trying to reach an agent from the dashboard. An
agent that gets stuck at 2am can tell you; you answer by prompting its session.

## Live delivery (channels)

By default a room message reaches an agent on its **next turn**. With channels
enabled, a message addressed to an agent that is **currently working** arrives in
under a second — Claude Code delivers it as a real user turn, mid-stream.

This is a Claude Code research-preview feature, so it needs a flag at launch:

```bash
succubus init     # writes the project .mcp.json that the flag resolves against
claude --dangerously-load-development-channels server:succubus
```

Claude Code shows a full-screen warning the first time; choose **I am using this
for local development**. A dim line below the banner confirms it is on:

```
▎Channels (experimental) messages from server:succubus inject directly in this
▎session · restart without --dangerously-load-development-channels to stop
```

### What it can and cannot do

| Recipient state | Delivery |
|---|---|
| Mid-turn (working) | **under a second** |
| Idle (waiting for input) | on its next turn |

The mechanism inserts a message into a turn that is already running. An idle
session has no turn to insert into, and no coding tool offers a way to start one
from outside — so an idle agent still reads the room when it is next given work.

This makes channels most useful **between agents**: an agent posting a question
is by definition mid-turn, and the agent it addresses is usually working too.

To make an idle agent answer now, start a short headless turn instead:

```bash
succubus wake --agent ORION
```

Or set `"auto_wake": true` in `~/.succubus/config.json` to do that automatically
whenever an agent is addressed by name. Each wake is a real, billable turn, and
it runs as a fresh process with no memory of the interactive session — enough to
answer a question, not to continue work in progress.

### Requirements and caveats

- **Claude Code only.** OpenCode's HTTP API could do the same; Codex has an
  experimental app-server; Gemini CLI has nothing yet.
- **Project `.mcp.json` is required.** `--dangerously-load-development-channels`
  resolves server names against the project `.mcp.json` or `~/.claude.json` —
  a server passed with `--mcp-config` is not found. `succubus init` writes it.
- **Without the flag, nothing breaks.** The notifications are dropped silently
  and the room behaves as it always did.
- **This is a prompt-injection surface.** Anything posted to the room arrives in
  front of Claude as a user turn. The daemon binds loopback only, but be aware of
  what that means before exposing it.

## Keeping the daemon running

Agents only coordinate while the daemon is up, so it is worth making that
automatic. `succubus setup` offers to do it; you can also manage it directly:

```bash
succubus service install     # start now, and at every login
succubus service status      # installed? answering?
succubus service restart     # reinstall and restart
succubus service uninstall   # stop and remove
```

Each platform uses its own native mechanism, and all three are **per user** —
no `sudo`, no system-wide daemon, nothing written outside your own config:

| Platform | Mechanism | Where |
|---|---|---|
| macOS | launchd user agent | `~/Library/LaunchAgents/com.enowdev.succubus.plist` |
| Linux | systemd user unit | `~/.config/systemd/user/succubus.service` |
| Windows | per-user Run key | `HKCU\...\CurrentVersion\Run` |

The daemon restarts automatically if it exits, and writes to
`~/.succubus/daemon.log`.

Two platform notes:

- **Linux:** a systemd user unit stops when your last session ends. On a
  headless machine you want it to survive that:
  `sudo loginctl enable-linger $USER`. `service install` tells you if this is
  needed.
- **Windows:** if group policy blocks the Run key, succubus falls back to a
  `.cmd` in your Startup folder automatically.

To customise the installed service, pass the flags at install time — they are
baked into the service definition:

```bash
succubus service install --addr 127.0.0.1:7801 --db ~/.succubus/succubus.db
```

## Platforms

succubus is pure Go with no cgo, so one binary per platform ships alone:

| | amd64 | arm64 |
|---|:-:|:-:|
| **macOS** | ✅ | ✅ |
| **Linux** | ✅ | ✅ |
| **Windows** | ✅ | ✅ |

Config paths follow the platform. On Windows `~` is `%USERPROFILE%`, so Claude
Code's settings live at `%USERPROFILE%\.claude\settings.json` — `succubus setup`
resolves this for you.

Two Windows differences worth knowing:

- **Codex CLI hooks are unavailable on Windows** (an upstream limitation), so
  Codex falls back to MCP there. Everything else behaves identically.
- Daemon liveness uses `tasklist` rather than `ps`, so a stale lockfile is
  cleaned up the same way.

Build every platform at once with `make release`.

## Configuration

Environment variables, all optional:

| Variable | Default | Meaning |
|---|---|---|
| `SUCCUBUS_ADDR` | `127.0.0.1:7801` | Daemon address |
| `SUCCUBUS_ENFORCEMENT` | `nag` | `off`, `nag`, or `block` |
| `SUCCUBUS_TOOL` | auto-detected | Override the reported tool name |
| `SUCCUBUS_SESSION` | auto-detected | Override the session key |
| `SUCCUBUS_BIN` | `succubus` | Binary path, for the OpenCode plugin |

### Enforcement levels

- **`off`** — hooks still register agents and inject state, but never warn or
  block.
- **`nag`** *(default)* — an edit without a claim produces a warning and the file
  is claimed for the agent retroactively.
- **`block`** — additionally, `PreToolUse` refuses an edit when another live
  agent holds that path.

Start at `nag`. Move to `block` once you trust that agents claim files
correctly — a false positive there stalls a working agent, which is worse than a
missed conflict.

```bash
SUCCUBUS_ENFORCEMENT=block succubus daemon
```

### Project identity

A project id is derived in this order:

1. `.succubus/project.json` with an explicit `{"id": "...", "display_name": "..."}`
2. The git remote URL, normalized — so two clones of one repo coordinate as one
   project
3. The git root path
4. The working directory

---

## Verifying it works

The real test is two agents seeing each other.

```bash
succubus daemon &
```

Open two agent sessions in the same repository, then:

```bash
succubus agents
```

Both should appear with **different names**. Now check the lease:

```bash
# In session ORION:
succubus claim src/main.go        # granted

# In session VESPER:
succubus claim src/main.go        # DENIED — held by ORION
```

And confirm the self-healing property — kill session ORION with `kill -9`, wait
for the lease TTL, then claim from VESPER. It should succeed. A crashed agent
must never hold a file forever.

If nothing appears, see [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
