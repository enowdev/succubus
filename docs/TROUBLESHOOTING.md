# Troubleshooting

## `succubus: command not found` right after installing

The binary is installed, but its directory is not on your `PATH`. The installer
prints the exact line to add when it detects this — it is one line in your shell
profile:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && exec zsh
```

On Windows the installer edits your user `PATH`, but **already-open terminals
keep the old one**. Open a new terminal.

## The installer says `checksum mismatch`

It refused to install, which is the correct outcome. The downloaded file does
not match the checksum published with the release, meaning it was corrupted in
transit or tampered with somewhere between GitHub and you.

Retrying is reasonable once, in case of a truncated download. If it persists, do
not work around it by skipping verification — check whether a proxy or a network
appliance is rewriting the download.

## The installer says `could not determine the latest release`

It could not reach the GitHub API, or no release is published yet. Pin a version
explicitly:

```bash
SUCCUBUS_VERSION=v0.1.0 sh install.sh
```

Or build from source, which needs no network beyond the clone:

```bash
git clone https://github.com/enowdev/succubus && cd succubus && make install
```

## `the installed binary did not run`

The download completed but the binary will not execute on this machine — almost
always the wrong architecture. The installer removes it rather than leaving
something broken on your `PATH`.

Check what you actually have:

```bash
uname -m          # arm64 or x86_64
```

On Windows, a 32-bit PowerShell on a 64-bit machine used to be a source of this;
the installer reads `PROCESSOR_ARCHITEW6432` to see through that. If it still
happens, report it with the output of `echo $env:PROCESSOR_ARCHITECTURE`.

## My agent does not appear in `succubus agents`

Registration normally happens in the `SessionStart` hook, so the agent does not
have to do anything. Check, in order:

```bash
succubus daemon                  # is it running at all?
curl 127.0.0.1:7801/api/health   # {"ok":true,...}
```

Then confirm the hook is wired up and produces output:

```bash
echo '{"session_id":"test","cwd":"'$(pwd)'","hook_event_name":"SessionStart"}' \
  | succubus hook SessionStart
```

You should get JSON containing `additionalContext` with a line like
`You are **ORION** in this project`. If you get nothing, the daemon is
unreachable — hooks stay silent by design rather than interrupting your session.

If the hook works by hand but not in the tool, the tool is not running it. Check
its config path from [SETUP.md](SETUP.md), and remember that some tools need a
restart after a config change.

**Tools with no hooks at all** — Cursor CLI, Copilot CLI, Aider — never
auto-register. Those agents must call `succubus_register` themselves.

## Agents keep disappearing

An agent is marked `idle` after 90 seconds without a heartbeat, and `dead` after
five minutes. Heartbeats come from the `UserPromptSubmit` hook, so a session
sitting idle with no prompts will eventually be swept.

This is intentional: it is how a crashed session's file claims get released. The
session is not broken — the next prompt re-registers it under the same name.

## A file is locked but nobody is editing it

Claims expire on their own. A claim also stops counting the moment its holding
agent is marked dead, so a crashed session cannot hold a file for the remainder
of its lease.

To see the truth:

```bash
succubus claims
```

Anything listed is held by a live agent with an unexpired lease. To break one:

```bash
succubus release <path>          # if you own it
```

or use the **Claims** page in the dashboard, which can force-release any lock.

## An agent is blocked and should not be

Check who actually holds the path:

```bash
succubus check path/to/file.go
```

If the answer is wrong, the likely cause is **path normalization**. succubus
stores paths repo-relative with forward slashes, lowercased on macOS and Windows
(both have case-insensitive filesystems). An agent passing an absolute path from
a different worktree will not match.

To disable blocking entirely while you investigate:

```bash
SUCCUBUS_ENFORCEMENT=nag succubus daemon
```

## The dashboard shows nothing

Confirm the daemon has data:

```bash
curl 127.0.0.1:7801/api/overview
```

If that returns `[]`, no project has registered yet. Run any succubus command
inside your repository — `succubus status` is enough — and reload.

If the page loads but never updates, the SSE stream is not connecting. The dot
in the top bar goes amber while reconnecting and red when the stream is closed.
Check for a proxy between the browser and the daemon that buffers
`text/event-stream`.

## Two clones of one repo show up as separate projects

They should not — project identity is derived from the git remote first, exactly
so clones coordinate as one project. If they diverge, one clone probably has no
remote configured.

Pin the identity explicitly in each clone:

```json
// .succubus/project.json
{ "id": "my-project", "display_name": "My Project" }
```

## `another succubus daemon is running`

A lockfile at `~/.succubus/daemon.lock` prevents two daemons from sharing one
database. If the previous daemon crashed, the lock is stale and succubus removes
it automatically once it sees the PID is gone. If it does not:

```bash
rm ~/.succubus/daemon.lock
```

## MCP tools do not appear in my agent

Test the server directly:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | succubus mcp
```

You should get two JSON lines, the second listing 18 tools. If that works but
the agent still sees nothing, the MCP config points at the wrong binary — use an
absolute path, not `succubus`, since agent tools often do not inherit your
shell's `PATH`.

## Claims are denied for files I already hold

Re-claiming your own path is a renewal and should always succeed. If it does
not, you are probably running as a *different session* than you think — each
session gets its own identity.

```bash
succubus whoami
```

Session keys come from the tool's own session id where available, falling back
to the terminal session, then the parent PID. Set `SUCCUBUS_SESSION` explicitly
if your tool exposes no stable id.

## Everything is slow

The daemon serializes writes through a single connection on purpose, so
concurrent claims cannot deadlock. That is not usually the bottleneck at this
scale — hundreds to thousands of records is well within what SQLite handles
without noticing.

If hooks feel slow, check that the daemon is on loopback and not behind a proxy.
Hooks time out after roughly three seconds and then give up silently.

## Starting over

```bash
pkill -f 'succubus daemon'
rm ~/.succubus/succubus.db*
rm -rf .succubus/            # per-project identity cache
succubus daemon
```

Config written by `succubus init` is not touched by this. Each file it edited
has a `.succubus-bak` copy from before the first change.
