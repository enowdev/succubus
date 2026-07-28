<!-- succubus:begin -->
## Agent coordination (succubus)

Several AI agents may work in this repository at the same time. succubus is the
shared record of who is here, what the plan is, and which files are taken.

**Before doing anything else**, call `succubus_register` to adopt your identity.
You will be given a name (for example ORION). That name is who you are here —
use it when referring to yourself and in every succubus tool call.

Then call `succubus_context` to read the active plan, your tasks, and the files
other agents are holding.

**Write to succubus as you work, not afterwards.** The board is only useful if it
reflects what is happening right now:

1. **Plan before building.** If there is no active plan and the work is not
   trivial, write one with `succubus_plan_create`. Keep it current with
   `succubus_plan_update` when the approach changes.
2. **Record tasks as you identify them**, with `succubus_task_create` — before
   doing them, not after. An unrecorded task is one another agent may start in
   parallel.
3. **Claim a task** with `succubus_task_claim` before starting it, and move it
   with `succubus_task_update`: `in_progress` when you begin, `review` or `done`
   when you finish. Never leave a task in `in_progress` you are no longer on.
4. **Claim files** with `succubus_claim_files` before editing, and release them
   with `succubus_release_files` when done. If a file is held by another agent,
   do not edit it.
5. **Record what would otherwise be lost**: a decision and its reasoning with
   `succubus_decisions`, progress with `succubus_report`, a note for a specific
   agent with `succubus_handoff`.
6. **Ask instead of guessing.** For an ambiguous requirement or a convention you
   cannot infer, use `succubus_ask`. Do not block waiting for an answer — post
   the question and carry on with something else.

If the succubus tools are unavailable, continue working normally — coordination
is best-effort and must never block you.
<!-- succubus:end -->
