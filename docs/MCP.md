# MCP tool reference

`succubus mcp` speaks JSON-RPC 2.0 over stdio, newline-delimited. It implements
`initialize`, `tools/list`, `tools/call`, `ping`, and
`notifications/initialized`. All logging goes to stderr — stdout carries
protocol frames only.

The MCP process is a thin bridge: it forwards to the daemon over HTTP and never
touches the database itself.

## Identity

### `succubus_register`

Adopt an identity. **Call this first, before any other work.** Returns the name
you are known by plus the full current state.

| Param | Type | Notes |
|---|---|---|
| `preferred_name` | string | Optional. One is assigned if omitted or taken |

Registration is idempotent on the session: calling it twice returns the same
identity, and a resumed session gets its own name back.

### `succubus_whoami`

Recover your adopted identity after a context compaction. No parameters.

### `succubus_agents`

List the other agents in this project and the files each holds. No parameters.

---

## Reading state

### `succubus_context`

The single most useful call. Returns the active plan, your tasks, unassigned
tasks, other agents, files they hold, and handoffs addressed to you — formatted
as text meant to be read, not parsed. No parameters.

---

## Plans

### `succubus_plan_list` · `succubus_plan_get`

List plans, or read one in full.

| Param | Type | Notes |
|---|---|---|
| `id` | string | Required by `_get` |

### `succubus_plan_create`

| Param | Type | Notes |
|---|---|---|
| `title` | string | **Required** |
| `body_md` | string | Markdown body |
| `status` | string | `draft`, `active`, `done`, `archived` — default `active` |

### `succubus_plan_update`

| Param | Type | Notes |
|---|---|---|
| `id` | string | **Required** |
| `title`, `body_md`, `status` | string | Only what you pass is changed |

---

## Tasks

### `succubus_task_list`

| Param | Type | Notes |
|---|---|---|
| `status` | string | `todo`, `in_progress`, `blocked`, `review`, `done`, `cancelled` |
| `assignee` | string | Agent name |
| `plan_id` | string | Tasks belonging to one plan |

Tasks whose dependencies are unfinished come back flagged `[BLOCKED]`.

### `succubus_task_create`

| Param | Type | Notes |
|---|---|---|
| `title` | string | **Required** |
| `body_md` | string | Details |
| `plan_id` | string | Owning plan |
| `status` | string | Default `todo` |
| `priority` | number | 1 high, 2 normal, 3 low |
| `depends_on` | string[] | Task ids that must finish first |

Dependency edges that would create a cycle are rejected.

### `succubus_task_update`

| Param | Type | Notes |
|---|---|---|
| `id` | string | **Required** |
| `title`, `body_md`, `status`, `assignee_name` | string | |
| `priority` | number | |

### `succubus_task_claim`

Take ownership so no other agent starts the same work. A task owned by another
**live** agent is refused; one owned by a dead agent can be taken.

| Param | Type | Notes |
|---|---|---|
| `id` | string | **Required** |
| `force` | boolean | Take it anyway |

---

## File claims

### `succubus_claim_files`

Lease paths **before** editing them.

| Param | Type | Notes |
|---|---|---|
| `paths` | string[] | **Required.** Repo-relative |
| `task_id` | string | Ties the lock to a task, shown on the board |
| `ttl_sec` | number | Lease seconds, default 900 |

All-or-nothing: if any path is held by another live agent, nothing is claimed
and every conflicting holder is named. Re-claiming a path you already hold is a
renewal, not a conflict.

### `succubus_release_files`

| Param | Type | Notes |
|---|---|---|
| `paths` | string[] | Paths to release |
| `all` | boolean | Release everything you hold |

### `succubus_check_files`

Ask whether paths are free without taking a lock.

| Param | Type | Notes |
|---|---|---|
| `paths` | string[] | **Required** |

---

## Communication

### `succubus_report`

Record progress so humans and other agents can follow along.

| Param | Type | Notes |
|---|---|---|
| `message` | string | **Required** |
| `detail` | string | Longer explanation |
| `task_id` | string | Task this concerns |
| `status` | string | Also move the task to this status |

### `succubus_handoff`

Address a note to another agent by name. It appears in their next context.

| Param | Type | Notes |
|---|---|---|
| `to_agent` | string | **Required.** e.g. `VESPER` |
| `title` | string | **Required** |
| `body_md` | string | The note |

### `succubus_decisions`

Pass a `title` to append to the decision log; omit it to read the log.

| Param | Type | Notes |
|---|---|---|
| `title` | string | Omit to read |
| `body_md` | string | Rationale |

---

## Behaviour under failure

No tool ever returns a hard error because succubus is unavailable. If the daemon
is not running, calls return advisory text telling the agent to carry on. This
is deliberate: succubus must degrade to nothing rather than break a session.

## Testing the server by hand

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  | succubus mcp
```
