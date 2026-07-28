# Architecture

## One binary, four modes

`succubus` dispatches on `argv[1]`:

| Mode | What it is |
|---|---|
| `daemon` | HTTP API, SSE hub, background janitor, embedded dashboard |
| `mcp` | MCP server over stdio; forwards to the daemon over HTTP |
| `hook` | Lifecycle hook handler for every supported tool dialect |
| everything else | CLI |

Only the daemon touches the database. The MCP bridge, hooks, and CLI are all
HTTP clients — which is what makes concurrent access safe without any
cross-process locking scheme.

```
agent tool ──┬── MCP stdio ──┐
             └── hook (exec) ─┼──► HTTP ──► daemon ──► SQLite
                              │              │
browser ────── SSE ───────────┘              └──► event bus ──► SSE subscribers
```

## Storage

SQLite via `modernc.org/sqlite` — pure Go, no cgo, so the binary
cross-compiles and ships alone. It is the project's only non-stdlib dependency.

Writes go through a connection pool capped at one, so they serialize in-process
instead of bouncing off `SQLITE_BUSY`. Reads use a separate pooled connection.
WAL mode plus `busy_timeout=5000` covers the cross-process case.

Database lives at `~/.succubus/succubus.db`, partitioned by `project_id` — one
daemon serves every project on the machine.

### Project identity

Resolved in priority order:

1. `.succubus/project.json` — explicit override
2. Git remote URL, normalized (ssh and https spellings collapse to the same
   key) → `sha256[:12]`
3. Git root path
4. Working directory

Remote-first means two clones of one repo on the same machine coordinate as one
project, which is almost always what you want.

## Named identity

Every session adopts a name from a curated pool — ORION, VESPER, KESTREL. Two
uniqueness constraints do the real work:

```sql
UNIQUE(project_id, name)         -- no two agents share a name
UNIQUE(project_id, session_key)  -- one identity per session
```

The second is what makes resume work. Registration keyed on `session_key` is
idempotent: three concurrent Claude Code sessions get three distinct names,
while a session that restarts gets *its own* name back rather than a new one.

Identity is also cached at `.succubus/agent-<session>.json` and re-injected on
every `SessionStart` and `UserPromptSubmit`, so a context compaction cannot make
an agent forget who it is. `succubus_whoami` is the manual recovery path.

**Registration is done by the hook, not the agent.** This is the central design
decision: an agent that never calls `succubus_register` is still registered,
still named, and still has coordination state injected. MCP alone would be
opt-in, and agents opt out.

## File claims

This is the part that had to be gotten right, and the obvious design is broken.

**One row per `(project_id, path)`.** Not an append log. An append log with a
partial unique index on `released_at IS NULL` deadlocks: a claim that expires
without being released can never be superseded — the expiry check says "you may
take it", the unique index says "a row already exists" — so a crashed agent
holds a file forever.

Claims are taken with a single conditional UPSERT:

```sql
INSERT INTO file_claims (...) VALUES (...)
ON CONFLICT(project_id, path) DO UPDATE SET ...
WHERE file_claims.released_at IS NOT NULL          -- freed
   OR file_claims.expires_at <= excluded.claimed_at -- lease lapsed
   OR file_claims.agent_id   = excluded.agent_id    -- renewal by the holder
   OR NOT EXISTS (                                  -- holder is dead or unknown
        SELECT 1 FROM agents a
        WHERE a.id = file_claims.agent_id AND a.status != 'dead')
```

Grant is decided by **`RowsAffected() == 1`**, not by the absence of an error — a
losing insert updates zero rows and commits perfectly happily.

The dead-holder clause matters as much as the expiry one. `SweepAgents` releases
claims at the moment it marks an agent dead, but a claim taken shortly *before*
that sweep outlives it. Without this clause a crashed agent blocks live agents
for the remainder of its lease.

Multi-path claims sort paths and run in one transaction, all-or-nothing — sorting
means two agents racing for overlapping sets cannot deadlock by acquiring in
opposite orders.

Expiry is lazy: `expires_at` is evaluated inside the claim statement itself. The
janitor that sweeps lapsed leases exists only so the dashboard can show them
ending; correctness never depends on it running.

### Tests worth keeping

`internal/store/claims_test.go` covers the failure modes that actually bit:

- 32 goroutines racing one path → exactly one grant
- An expired-but-unreleased claim is stealable
- A dead agent's *unexpired* claim is stealable, and invisible to
  `ActiveClaims` / `CheckFiles`
- Overlapping batches claimed in opposite orders do not deadlock

## Enforcement

Three tiers, set with `SUCCUBUS_ENFORCEMENT`:

| Level | Behaviour |
|---|---|
| `off` | Register and inject only |
| `nag` *(default)* | Warn on an unclaimed edit, and claim the file retroactively |
| `block` | Also deny `PreToolUse` when another live agent holds the path |

Blocking is opt-in because a false positive stalls a working agent, which is
worse than a missed conflict. Start at `nag`.

Every hook exits 0 when the daemon is unreachable. succubus degrades to nothing
rather than breaking a session — that is a hard rule, not a nicety.

## Hook dialects

One handler, five vocabularies. `classify()` maps each tool's event names onto
five actions:

| Action | Claude / Droid / Codex | Gemini | OpenCode |
|---|---|---|---|
| register + inject | `SessionStart` | `SessionStart` | `session.created` |
| inject + heartbeat | `UserPromptSubmit` | `BeforeAgent` | — |
| block on conflict | `PreToolUse` | `BeforeTool` | `tool.execute.before` |
| nag if unclaimed | `PostToolUse` | `AfterTool` | `tool.execute.after` |
| release everything | `Stop`, `SessionEnd` | `SessionEnd` | `session.idle` |

Payload shapes differ too, so `extractPaths` accepts every spelling of a file
path it has seen (`file_path`, `path`, `filePath`, `edits[].file_path`, …).

Blocking is expressed per dialect: Claude and Droid take a
`permissionDecision: "deny"` object, Gemini takes exit code 2, Cursor takes
`permission: "deny"`.

OpenCode has no shell hooks at all, so it gets a TypeScript plugin
(`assets/opencode/succubus.ts`) that shells out to the same handler.

## HTTP API

Go 1.22 `ServeMux` patterns — `POST /api/tasks/{id}` with `r.PathValue("id")`.
No router dependency.

SSE lives at `GET /api/projects/{pid}/stream`. Two details make it work:

- `ResponseController.SetWriteDeadline(time.Time{})` — without it, the server's
  write timeout kills long-lived streams.
- Every frame carries its database row id, so a reconnecting browser replays
  from `Last-Event-ID` instead of silently missing events.

`GET /api/projects/{pid}/context` returns the injection payload used by hooks,
the MCP `succubus_context` tool, and the CLI alike — so all three tell an agent
exactly the same story.

## Dashboard

React 19 + Vite, no component library and no Tailwind — design tokens plus
hand-written CSS, in the flat true-black style borrowed from enowx-rag. Icons
are `lucide-react`. That is the entire dependency list.

Built output is embedded with `//go:embed all:dist`, so `make build` produces a
single binary that serves its own UI. A placeholder `dist/index.html` is
committed so `go build` works on a fresh clone; the daemon detects the
placeholder and points you at the dev server instead of serving a stub.

State is loaded once per project and kept current from the event stream, with
event bursts coalesced into one refetch per slice.

## What is deliberately not here

- **No authentication.** The daemon binds loopback only. Exposing it beyond
  localhost would need a token first.
- **No cross-machine sync.** One daemon, one machine. Agents on different
  machines do not coordinate.
- **No mandatory locking.** Claims are advisory leases, as in every comparable
  system. Enforcement comes from hooks refusing tool calls, not from the
  filesystem.
