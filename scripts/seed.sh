#!/usr/bin/env bash
# Populate a running daemon with a realistic project so the dashboard has
# something to show during development.
#
# Re-running is safe and idempotent: agents keep their names (same session
# keys), and the plans/tasks/notes this script created previously are deleted
# before being written again, so nothing accumulates duplicates.
set -euo pipefail

API="http://${SUCCUBUS_ADDR:-127.0.0.1:7801}/api"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

jqf() { python3 -c "import sys,json;print(json.load(sys.stdin)$1)"; }

echo "resolving project…"
PID=$(curl -sf -X POST "$API/projects/resolve" -H 'Content-Type: application/json' \
  -d "{\"cwd\":\"$ROOT\"}" | jqf "['id']")
echo "  project_id=$PID"

# Register two agents and capture both id and adopted name.
A_JSON=$(curl -sf -X POST "$API/projects/$PID/agents/register" -H 'Content-Type: application/json' \
  -d "{\"tool\":\"claude-code\",\"session_key\":\"demo:claude-1\",\"cwd\":\"$ROOT\",\"pid\":$$}")
B_JSON=$(curl -sf -X POST "$API/projects/$PID/agents/register" -H 'Content-Type: application/json' \
  -d "{\"tool\":\"opencode\",\"session_key\":\"demo:opencode-1\",\"cwd\":\"$ROOT\",\"pid\":$$}")

AID=$(echo "$A_JSON" | jqf "['agent']['id']")
ANAME=$(echo "$A_JSON" | jqf "['agent']['name']")
BID=$(echo "$B_JSON" | jqf "['agent']['id']")
BNAME=$(echo "$B_JSON" | jqf "['agent']['name']")
echo "  agents: $ANAME (claude-code), $BNAME (opencode)"

# Heartbeat so the sweeper keeps them alive while you look at the dashboard.
curl -sf -X POST "$API/agents/$AID/heartbeat" >/dev/null
curl -sf -X POST "$API/agents/$BID/heartbeat" >/dev/null

# Wipe what a previous run of this script left behind. Deleting the plan does
# not cascade to its tasks (plan_id is set null), so tasks go first.
echo "clearing previous seed data…"
curl -sf "$API/projects/$PID/tasks" \
  | python3 -c "import sys,json;[print(t['id']) for t in json.load(sys.stdin)]" \
  | while read -r id; do curl -sf -X DELETE "$API/tasks/$id" >/dev/null || true; done
curl -sf "$API/projects/$PID/plans" \
  | python3 -c "import sys,json;[print(p['id']) for p in json.load(sys.stdin)]" \
  | while read -r id; do curl -sf -X DELETE "$API/plans/$id" >/dev/null || true; done
curl -sf "$API/projects/$PID/decisions" \
  | python3 -c "import sys,json;[print(d['id']) for d in json.load(sys.stdin)]" \
  | while read -r id; do curl -sf -X DELETE "$API/decisions/$id" >/dev/null || true; done

PLAN=$(curl -sf -X POST "$API/projects/$PID/plans" -H 'Content-Type: application/json' -d '{
  "title": "succubus v1",
  "body_md": "Cross-agent coordination layer.\n\n- Go daemon + SQLite (one dependency: modernc.org/sqlite)\n- MCP stdio bridge, JSON-RPC by hand\n- Hook dialects for Claude Code, Droid, Codex, Gemini\n- React dashboard on Kumo + Phosphor, live over SSE",
  "status": "active"
}' | jqf "['id']")
echo "  plan=$PLAN"

mktask() {
  curl -sf -X POST "$API/projects/$PID/tasks" -H 'Content-Type: application/json' -d "$1" | jqf "['id']"
}

T1=$(mktask "{\"title\":\"SQLite store + conditional-UPSERT claims\",\"plan_id\":\"$PLAN\",\"status\":\"done\",\"assignee_name\":\"$ANAME\",\"priority\":1}")
T2=$(mktask "{\"title\":\"HTTP API + SSE event stream\",\"plan_id\":\"$PLAN\",\"status\":\"done\",\"assignee_name\":\"$ANAME\"}")
T3=$(mktask "{\"title\":\"React dashboard (Kumo + Phosphor)\",\"plan_id\":\"$PLAN\",\"status\":\"in_progress\",\"assignee_name\":\"$ANAME\",\"priority\":1}")
T4=$(mktask "{\"title\":\"MCP stdio bridge\",\"plan_id\":\"$PLAN\",\"status\":\"in_progress\",\"assignee_name\":\"$BNAME\"}")
T5=$(mktask "{\"title\":\"Hook dialects: droid, codex, gemini\",\"plan_id\":\"$PLAN\",\"status\":\"todo\"}")
T6=$(mktask "{\"title\":\"OpenCode plugin (it has no shell hooks)\",\"plan_id\":\"$PLAN\",\"status\":\"todo\",\"priority\":3}")
T7=$(mktask "{\"title\":\"Embed the SPA into the single binary\",\"plan_id\":\"$PLAN\",\"status\":\"review\",\"assignee_name\":\"$BNAME\"}")

# T7 cannot ship before the dashboard exists — shows the blocked state.
curl -sf -X POST "$API/tasks/$T7/deps" -H 'Content-Type: application/json' \
  -d "{\"depends_on\":\"$T3\"}" >/dev/null

curl -sf -X POST "$API/projects/$PID/claims" -H 'Content-Type: application/json' -d "{
  \"agent_id\":\"$AID\",\"agent_name\":\"$ANAME\",\"task_id\":\"$T3\",
  \"paths\":[\"web/src/pages/BoardPage.tsx\",\"web/src/components/TaskCard.tsx\"],\"ttl_sec\":3600}" >/dev/null
curl -sf -X POST "$API/projects/$PID/claims" -H 'Content-Type: application/json' -d "{
  \"agent_id\":\"$BID\",\"agent_name\":\"$BNAME\",\"task_id\":\"$T4\",
  \"paths\":[\"internal/mode/mcp.go\"],\"ttl_sec\":3600}" >/dev/null

curl -sf -X POST "$API/projects/$PID/decisions" -H 'Content-Type: application/json' -d "{
  \"kind\":\"decision\",
  \"title\":\"One row per (project, path) for file claims\",
  \"body_md\":\"An append-log with a partial unique index deadlocks: an expired-but-unreleased claim can never be superseded, so a crashed agent would hold a file forever. A conditional UPSERT self-heals, and also releases when the holder is dead.\",
  \"author_name\":\"$ANAME\"}" >/dev/null

curl -sf -X POST "$API/projects/$PID/decisions" -H 'Content-Type: application/json' -d "{
  \"kind\":\"handoff\",
  \"title\":\"Cursor CLI is effectively MCP-only\",
  \"body_md\":\"It documents ~20 hook events but the CLI only fires beforeShellExecution/afterShellExecution. Do not promise enforcement there.\",
  \"author_name\":\"$ANAME\",\"target_agent_name\":\"$BNAME\"}" >/dev/null

echo
echo "seeded. dashboard: http://localhost:5273/"
echo "  project '$(basename "$ROOT")' is selectable from the sidebar."
