---
name: succubus
description: Coordinate with other AI agents working on this same project. Use at the start of every session, and before editing any file, to adopt your identity, read the shared plan and task board, and lease the files you are about to change so two agents never edit the same file at once.
---

# succubus — working alongside other agents

Several AI agents may be working in this repository **right now**. succubus is
the shared record of who is here, what the plan is, which tasks are taken, and
which files are locked.

## Do this first

Call **`succubus_register`**. It returns the name you are known by here — for
example `ORION`. That name is your identity in this project: use it when
referring to yourself, and when other agents refer to you.

The response also contains the current state: the active plan, your tasks, the
other agents present, and the files they hold. Read it before you plan anything.

If you have forgotten your name — after a long session, or a context
compaction — call **`succubus_whoami`**.

## Before you edit any file

Call **`succubus_claim_files`** with the paths you are about to change.

- **Granted** — go ahead. Call `succubus_release_files` when you are done.
- **Denied** — another agent holds that file. **Do not edit it.** Work on
  something else, or leave the holder a note with `succubus_handoff`.

Claims expire on their own, so a crashed agent never blocks a file forever. You
do not need to worry about cleaning up after someone else.

To check availability without taking a lock, use `succubus_check_files`.

## Plan before you build

**If there is no active plan and the work is not trivial, write one first** with
`succubus_plan_create` — what you are building and the approach you intend to
take. Every agent reads the active plan on every turn, so this is the cheapest
way to keep four sessions pointed the same direction.

Keep it current with `succubus_plan_update` as the approach changes. A stale
plan is worse than none, because agents act on it.

## Working on tasks

The board is only useful if it reflects what is happening *now*, so write to it
as you work rather than afterwards.

- `succubus_task_create` — record work **as you identify it, before doing it**.
  An unrecorded task is one another agent may start in parallel.
- `succubus_task_list` — check the board before inventing new work; the task
  may already exist.
- `succubus_task_claim` — take ownership so no one else starts the same thing.
- `succubus_task_update` — `in_progress` when you begin, `review` or `done` when
  you finish. Never leave a task in `in_progress` that you have stopped working
  on: it tells every other agent a lie.

## Leaving a trail

- `succubus_report` — record progress on a task, so the human and the other
  agents can follow along without reading your whole transcript.
- `succubus_decisions` — record *why* you chose an approach. Future agents
  inherit your reasoning instead of relitigating it.
- `succubus_handoff` — address a note to a specific agent by name. They see it
  in their next context.

## Etiquette

- Claim before editing, release when done. Holding a file you are not editing
  blocks real work.
- Do not steal a task another live agent owns. If you think it is stuck, send a
  handoff and ask.
- Do not edit a file that is locked by someone else, even if the change looks
  trivial. That is exactly the case succubus exists to prevent.
- Keep the board honest. A task left in `in_progress` after you stop working on
  it tells everyone a lie.

## If succubus is unavailable

The tools will say the daemon is not running. **Continue working normally.**
Coordination is best-effort and must never block you — just be aware that other
agents cannot see what you are doing, so be conservative about wide-reaching
edits.
