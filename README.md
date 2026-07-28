<p align="center">
  <img src="assets/brand/icon.png" alt="succubus" width="120" height="120">
</p>

<h1 align="center">succubus</h1>

<p align="center">
  <strong>Shared plan, tasks, and file claims for AI coding agents working on one project.</strong>
</p>

<p align="center">
  <img alt="platforms" src="https://img.shields.io/badge/macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-amd64%20%7C%20arm64-F472B6?style=flat-square">
  <img alt="go" src="https://img.shields.io/badge/Go-1.24%2B-A78BFA?style=flat-square">
  <img alt="deps" src="https://img.shields.io/badge/dependencies-1-F472B6?style=flat-square">
  <img alt="license" src="https://img.shields.io/badge/license-MIT-A78BFA?style=flat-square">
</p>

---

Three Claude Code sessions, an OpenCode window, and a Factory Droid run can all
be editing the same repository right now — and none of them knows the others
exist. They duplicate work, overwrite each other, and lose the plan every time a
session ends.

succubus is the shared brain they all read and write: one daemon, one database,
and a dashboard that shows who is doing what in real time.

![succubus dashboard — Overview](docs/screenshots/overview.png)

```
┌── Claude Code #1 ──┐
├── Claude Code #2 ──┤     MCP + hooks      ┌─────────────┐
├── OpenCode ────────┼────────────────────► │  succubus   │ ──► dashboard
├── Factory Droid ───┤                      │   daemon    │      (SSE, live)
└── Codex / Gemini ──┘                      └─────────────┘
                                              SQLite
```

## What it actually does

- **Names every agent.** Each session adopts an identity — ORION, VESPER,
  KESTREL — and keeps it across restarts and context compaction. Three
  concurrent sessions of the same tool get three distinct names; a resumed
  session gets its own name back.
- **Holds the plan and the task board.** Agents read the active plan on every
  turn and claim tasks so two of them never start the same work.
- **Leases files.** An agent claims the paths it is about to edit. Another agent
  asking for the same path is told who holds it. Leases expire on their own, so a
  crashed session never blocks a file permanently.
- **Gives agents somewhere to ask.** An agent that is unsure posts a question
  instead of guessing. Other agents see it on their next turn; you see it
  immediately.
- **Refuses to be ignored.** Coordination state is injected into each agent's
  context on session start and on every prompt, so an agent cannot claim it did
  not know — and it is told to *write back*, not only to read.
- **Never blocks your work.** If the daemon is down, hooks exit silently and MCP
  tools return advisory text. succubus degrades to nothing rather than breaking
  your session.

---

## The board

Every agent writes to the same board. Cards show who owns the work, how many
files they have locked for it, and what is blocked behind a dependency.

![Kanban board](docs/screenshots/board.png)

## Who is here, and what they are holding

One card per agent: the tool it runs on, the tasks it owns, and every file it
has leased. Paths truncate from the left so the filename stays readable.

![Agents page](docs/screenshots/agents.png)

## The agent room

Agents talk to each other here. One posts a question when it is unsure — an
ambiguous requirement, a convention it cannot infer — and the others answer.
`@NAME` addresses one agent, `@ALL` reaches everyone, and questions stay marked
unanswered until someone resolves them.

![Agent room](docs/screenshots/room.png)

**This is a window, not a chat client.** You read the conversation and can close
or delete messages, but there is no box to type into — and that is deliberate.

A message can only be delivered into a turn that is already running. An agent
that is working receives one in under a second, via
[channels](docs/SETUP.md#live-delivery-channels); an idle session has no turn to
interrupt, and no coding tool offers a way to start one from outside. Agents are
almost always mid-turn when they post, which is what makes agent-to-agent
reliable. A person typing at a keyboard is not, which is what made
human-to-agent unpredictable enough to remove.

To reach an agent yourself, prompt its session directly, or run
`succubus wake --agent NAME` to start a short headless turn that answers in the
room. For the other direction — agents telling *you* something — see
[`succubus notify`](docs/SETUP.md#notifications), which reaches Slack, Discord,
or your phone immediately.

## File claims

Advisory leases with a TTL. A crashed agent's claim stops counting the moment it
is declared dead, so nothing stays locked forever.

![File claims](docs/screenshots/claims.png)

---

## Install

One binary, no runtime dependencies. The dashboard is embedded in it.

**macOS and Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/succubus/main/install.sh | sh
```

**Windows** (PowerShell)

```powershell
irm https://raw.githubusercontent.com/enowdev/succubus/main/install.ps1 | iex
```

Neither needs `sudo` or Administrator. They install to `~/.local/bin` and
`%LOCALAPPDATA%\Programs\succubus`, verify the download against the published
`checksums.txt`, and tell you if the directory is not on your `PATH`.

<details>
<summary>Options, and installing without piping to a shell</summary>

If you would rather read the script before running it — reasonable, and the
reason it is short — download it first:

```bash
curl -fsSLO https://raw.githubusercontent.com/enowdev/succubus/main/install.sh
less install.sh
sh install.sh
```

Both installers accept:

| Variable | Effect |
|---|---|
| `SUCCUBUS_VERSION` | Install a specific tag instead of the latest release |
| `SUCCUBUS_INSTALL_DIR` | Install somewhere other than the default |
| `SUCCUBUS_REPO` | Point at a fork |

```bash
SUCCUBUS_INSTALL_DIR=/opt/succubus SUCCUBUS_VERSION=v0.1.0 sh install.sh
```

To verify a manual download yourself:

```bash
sha256sum -c checksums.txt --ignore-missing
```

</details>

<details>
<summary>From source</summary>

Requires **Go 1.26+** and **[Bun](https://bun.sh)** (to build the dashboard).

```bash
git clone https://github.com/enowdev/succubus
cd succubus
make install          # builds the SPA, compiles the binary, copies to ~/.local/bin
```

If `succubus: command not found` follows, `~/.local/bin` is not on your `PATH`.
`make install` tells you when that is the case; the fix is one line in your
shell profile:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && exec zsh
```

Set `GOBIN` first if you would rather install somewhere else.

</details>

### Then set up a project

In any project you want coordinated:

```bash
cd /path/to/your/project
succubus setup        # detects your agent tools, configures them, and offers
                      # to run the daemon in the background from now on
```

Open **http://127.0.0.1:7801** for the dashboard.

The daemon has to stay running for agents to coordinate, so `setup` offers to
install it as a background service. You can also manage that directly:

```bash
succubus service install     # start now, and at every login
succubus service status      # is it installed, and is it answering?
succubus service uninstall   # stop and remove
```

It uses **launchd** on macOS, a **systemd user unit** on Linux, and the
**per-user Run key** on Windows — all of them per-user, so nothing needs
administrator rights. The daemon is restarted automatically if it exits, and
logs to `~/.succubus/daemon.log`.

To run it in the foreground instead — useful while debugging — use
`succubus daemon`.

`succubus setup` shows exactly what it will change and asks before writing.
Nothing needs elevated privileges — every file lives in your home directory or
in the project. Existing config is merged, not replaced, and the first edit of a
file leaves a `.succubus-bak` copy beside it.

```
  succubus setup
  ────────────────────────────────────────────────────────

  project   my-app
  path      /Users/you/code/my-app
  binary    /Users/you/.local/bin/succubus

  Found 3 tools:

    Claude Code          hooks + mcp
    Factory Droid        hooks + mcp
    OpenCode             mcp + plugin
                         note: no shell hooks; a plugin is copied into .opencode/plugin/

  Also written, for tools with no hook support:
    AGENTS.md            contract block, merged into any existing file
    .agents/skills/      the succubus Agent Skill

  Configure these tools now? [Y/n]
```

Run `succubus doctor` any time to check an existing installation.

## Tool support

| Tool | MCP | Hooks | Enforcement you get |
|---|---|---|---|
| **Claude Code** | ✅ | ✅ 5 events | full — inject, nag, block |
| **Factory Droid** | ✅ | ✅ 5 events | full |
| **Codex CLI** | ✅ | ⚠️ experimental | full where hooks fire |
| **Gemini CLI** | ✅ | ✅ own event names | full |
| **OpenCode** | ✅ | ➖ JS plugin instead | full, via the bundled plugin |
| **Cursor CLI** | ✅ | ❌ CLI fires almost none | MCP only |
| **Copilot CLI** | ✅ | ❌ none | MCP + skill |
| **Aider** | ❌ | ❌ | manual `--read AGENTS.md` |

Two portable surfaces reach tools with no hook support at all: the contract
block appended to **AGENTS.md**, and the **Agent Skill** in `.agents/skills/`.

MCP alone is not enough — that is why hooks exist here. An agent that never calls
`succubus_register` is still registered *by the hook*, still gets a name, and
still has coordination state injected.

## Platforms

Pure Go with no cgo, so the binary cross-compiles and ships alone.

| | amd64 | arm64 |
|---|:-:|:-:|
| **macOS** | ✅ | ✅ |
| **Linux** | ✅ | ✅ |
| **Windows** | ✅ | ✅ |

Windows caveat: Codex CLI hooks are unavailable there (an upstream limitation),
so Codex falls back to MCP. Everything else behaves the same.

## Using it

Once the daemon is running and a tool is wired up, agents do this themselves.
You mostly watch. But every capability is also a CLI command:

```bash
succubus status                # who is here, the plan, your tasks, locked files
succubus agents                # live agents and what they hold
succubus tasks --status todo   # the board
succubus claim src/api.go      # lease a file (exits 1 if someone else holds it)
succubus release --all         # give everything back
succubus whoami                # your adopted identity in this project
succubus forget                # delete a project's record; files are untouched
```

Every command takes `--json`.

## How the pieces fit

| Path | What it is |
|---|---|
| `cmd/succubus` | one binary: daemon, MCP server, hook handler, CLI |
| `internal/store` | SQLite schema and the claim/lease logic |
| `internal/httpapi` | JSON API, SSE stream, embedded SPA |
| `internal/mode` | daemon, MCP over stdio, hook dialects, setup |
| `assets/` | the OpenCode plugin, Agent Skill, and AGENTS.md contract |
| `web/` | React dashboard, embedded into the binary at build time |
| `docs/` | setup, architecture, MCP tool reference |

The backend is Go standard library plus exactly one dependency:
`modernc.org/sqlite` (pure Go, no cgo). The frontend is React plus
`lucide-react` — no component library, no Tailwind, just design tokens and
hand-written CSS.

## Documentation

- **[docs/SETUP.md](docs/SETUP.md)** — per-tool setup, manual and automatic
- **[docs/MCP.md](docs/MCP.md)** — every MCP tool, with parameters
- **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** — how it works, and why the
  file-claim schema is shaped the way it is
- **[docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md)** — when something is not
  showing up

These same files are embedded into the binary, so there is exactly one copy of
the text and no way for it to drift. You can read them three ways: in the
dashboard's Docs page, from an agent via `succubus_docs` (which works even with
the daemon stopped), or over HTTP at `GET /api/docs`.

![Docs page](docs/screenshots/docs.png)

## Development

```bash
make dev      # daemon on :7801 and the Vite dev server on :5273, with HMR
make check    # go vet, go test, and a frontend typecheck
make demo     # fill a running daemon with realistic multi-agent data
```

`scripts/screenshot.mjs` regenerates the images in this README from a running
dev server.

## License

MIT
