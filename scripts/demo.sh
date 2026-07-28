#!/usr/bin/env bash
# Fill a running daemon with a believable multi-agent session, for screenshots
# and for trying the dashboard without wiring up real agents.
#
# Unlike seed.sh this paints a fuller picture: four agents on different tools,
# a plan with real progress, a board across every column, contended files, and
# a room conversation with an unanswered question.
set -euo pipefail

API="http://${SUCCUBUS_ADDR:-127.0.0.1:7801}/api"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

jqf() { python3 -c "import sys,json;print(json.load(sys.stdin)$1)"; }
post() { curl -sf -X POST "$1" -H 'Content-Type: application/json' -d "$2"; }

PID=$(post "$API/projects/resolve" "{\"cwd\":\"$ROOT\"}" | jqf "['id']")
echo "project $PID"

echo "clearing previous demo data…"
for kind in tasks plans decisions; do
  curl -sf "$API/projects/$PID/$kind" \
    | python3 -c "import sys,json;[print(x['id']) for x in json.load(sys.stdin)]" \
    | while read -r id; do
        case $kind in
          tasks) curl -sf -X DELETE "$API/tasks/$id" >/dev/null || true ;;
          plans) curl -sf -X DELETE "$API/plans/$id" >/dev/null || true ;;
          decisions) curl -sf -X DELETE "$API/decisions/$id" >/dev/null || true ;;
        esac
      done
done
curl -sf "$API/projects/$PID/room" \
  | python3 -c "import sys,json;[print(m['id']) for m in json.load(sys.stdin)['messages']]" \
  | while read -r id; do curl -sf -X DELETE "$API/room/$id" >/dev/null || true; done

# --- agents, one per tool ----------------------------------------------------
register() {
  post "$API/projects/$PID/agents/register" \
    "{\"tool\":\"$1\",\"session_key\":\"demo:$2\",\"cwd\":\"$ROOT\",\"pid\":$$}"
}
A=$(register claude-code a); AID=$(echo "$A" | jqf "['agent']['id']"); AN=$(echo "$A" | jqf "['agent']['name']")
B=$(register opencode   b); BID=$(echo "$B" | jqf "['agent']['id']"); BN=$(echo "$B" | jqf "['agent']['name']")
C=$(register codex      c); CID=$(echo "$C" | jqf "['agent']['id']"); CN=$(echo "$C" | jqf "['agent']['name']")
D=$(register droid      d); DID=$(echo "$D" | jqf "['agent']['id']"); DN=$(echo "$D" | jqf "['agent']['name']")
echo "agents: $AN $BN $CN $DN"

for id in "$AID" "$BID" "$CID"; do curl -sf -X POST "$API/agents/$id/heartbeat" >/dev/null; done

# --- plan --------------------------------------------------------------------
PLAN=$(post "$API/projects/$PID/plans" '{
  "title": "Ship succubus v1",
  "body_md": "One daemon, one database, every agent on the same page.\n\nThe hard part is not storage — it is making agents that cannot see each other behave as if they can. Hooks inject state so no agent can claim ignorance; MCP gives them the tools to write back.\n\nRemaining before release: finish the hook dialects, prove the OpenCode plugin end to end, and embed the dashboard in the binary.",
  "status": "active"}' | jqf "['id']")

mktask() { post "$API/projects/$PID/tasks" "$1" | jqf "['id']"; }

T1=$(mktask "{\"title\":\"SQLite store with conditional-UPSERT file claims\",\"plan_id\":\"$PLAN\",\"status\":\"done\",\"assignee_name\":\"$AN\",\"priority\":1}")
T2=$(mktask "{\"title\":\"HTTP API and the SSE event stream\",\"plan_id\":\"$PLAN\",\"status\":\"done\",\"assignee_name\":\"$AN\"}")
T3=$(mktask "{\"title\":\"Named agent identity that survives compaction\",\"plan_id\":\"$PLAN\",\"status\":\"done\",\"assignee_name\":\"$BN\"}")
T4=$(mktask "{\"title\":\"React dashboard, live over SSE\",\"plan_id\":\"$PLAN\",\"status\":\"in_progress\",\"assignee_name\":\"$AN\",\"priority\":1}")
T5=$(mktask "{\"title\":\"MCP stdio bridge — JSON-RPC by hand\",\"plan_id\":\"$PLAN\",\"status\":\"in_progress\",\"assignee_name\":\"$BN\"}")
T6=$(mktask "{\"title\":\"Hook dialects: Droid, Codex, Gemini\",\"plan_id\":\"$PLAN\",\"status\":\"in_progress\",\"assignee_name\":\"$CN\"}")
T7=$(mktask "{\"title\":\"Embed the SPA into the single binary\",\"plan_id\":\"$PLAN\",\"status\":\"review\",\"assignee_name\":\"$BN\"}")
T8=$(mktask "{\"title\":\"OpenCode plugin — it has no shell hooks\",\"plan_id\":\"$PLAN\",\"status\":\"todo\"}")
T9=$(mktask "{\"title\":\"Cross-platform release binaries\",\"plan_id\":\"$PLAN\",\"status\":\"todo\",\"priority\":3}")
T10=$(mktask "{\"title\":\"Windows: tasklist instead of ps for liveness\",\"plan_id\":\"$PLAN\",\"status\":\"todo\"}")

# T7 cannot land before the dashboard exists — shows the blocked state.
post "$API/tasks/$T7/deps" "{\"depends_on\":\"$T4\"}" >/dev/null

# --- contended files ---------------------------------------------------------
claim() {
  post "$API/projects/$PID/claims" \
    "{\"agent_id\":\"$1\",\"agent_name\":\"$2\",\"task_id\":\"$3\",\"ttl_sec\":2700,\"paths\":$4}" >/dev/null
}
claim "$AID" "$AN" "$T4" '["web/src/pages/Board.tsx","web/src/components/TaskModal.tsx","web/src/index.css"]'
claim "$BID" "$BN" "$T5" '["internal/mode/mcp.go","internal/client/client.go"]'
claim "$CID" "$CN" "$T6" '["internal/mode/hook.go"]'

# --- decisions ---------------------------------------------------------------
post "$API/projects/$PID/decisions" "{
  \"kind\":\"decision\",
  \"title\":\"One row per (project, path) for file claims\",
  \"body_md\":\"An append-log with a partial unique index deadlocks: a claim that expires without being released can never be superseded, so a crashed agent would hold a file forever. A conditional UPSERT self-heals, and also releases when the holder is dead.\",
  \"author_name\":\"$AN\"}" >/dev/null

post "$API/projects/$PID/decisions" "{
  \"kind\":\"decision\",
  \"title\":\"Hooks register agents, agents do not register themselves\",
  \"body_md\":\"MCP alone is opt-in, and an agent that never calls succubus_register would be invisible. Registering from SessionStart means even an uncooperative agent is named and gets state injected.\",
  \"author_name\":\"$BN\"}" >/dev/null

post "$API/projects/$PID/decisions" "{
  \"kind\":\"handoff\",
  \"title\":\"Cursor CLI is effectively MCP-only\",
  \"body_md\":\"It documents around twenty hook events, but the CLI fires almost none of them. Do not promise enforcement there — wire up MCP and say so plainly in the docs.\",
  \"author_name\":\"$AN\",\"target_agent_name\":\"$CN\"}" >/dev/null

# --- room --------------------------------------------------------------------
say() { post "$API/projects/$PID/room" "$1" >/dev/null; }

Q1=$(post "$API/projects/$PID/room" "{
  \"kind\":\"question\",\"author_id\":\"$AID\",\"author_name\":\"$AN\",
  \"body_md\":\"Should the dashboard bundle Inter and JetBrains Mono, or keep loading them from Google Fonts? @$BN you touched the build config last.\"}" | jqf "['id']")

say "{\"parent_id\":\"$Q1\",\"author_id\":\"$BID\",\"author_name\":\"$BN\",
  \"body_md\":\"Bundle them. A daemon you run offline that silently loses its typeface is worse than a slightly bigger binary.\"}"

say "{\"author_id\":\"$CID\",\"author_name\":\"$CN\",\"kind\":\"announce\",
  \"body_md\":\"Starting on the Gemini hook dialect now. Its event names differ enough that it needs its own --dialect flag — I have internal/mode/hook.go claimed.\"}"

say "{\"author_name\":\"HUMAN\",
  \"body_md\":\"@ALL heads up: I am renaming the events table tonight. Do not start anything that touches it.\"}"

post "$API/projects/$PID/room" "{
  \"kind\":\"question\",\"author_id\":\"$CID\",\"author_name\":\"$CN\",
  \"body_md\":\"Do we support Windows for the Codex hooks? The config path differs and I would rather ask than guess.\"}" >/dev/null

curl -sf -X POST "$API/room/$Q1/resolve" -H 'Content-Type: application/json' \
  -d "{\"by\":\"$AN\"}" >/dev/null

# Let the fourth agent go quiet, so the UI shows a non-active state too.
curl -sf -X POST "$API/agents/$DID/heartbeat" >/dev/null || true

echo
echo "demo data ready — http://localhost:5273/"
