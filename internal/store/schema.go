package store

// schemaSQL is the full DDL for succubus. It is executed on every startup and
// is written to be idempotent, so it doubles as the v1 migration.
//
// The shape of file_claims is load-bearing: exactly one row per
// (project_id, path). An append-log with a partial unique index on
// released_at IS NULL deadlocks — an expired-but-unreleased claim can never be
// superseded, so a crashed agent would hold a path forever. See ClaimFiles.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS projects (
  id            TEXT PRIMARY KEY,
  display_name  TEXT NOT NULL,
  root_path     TEXT NOT NULL,
  git_remote    TEXT,
  created_at    INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS agents (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  tool              TEXT NOT NULL,
  session_key       TEXT NOT NULL,
  pid               INTEGER,
  cwd               TEXT,
  status            TEXT NOT NULL DEFAULT 'active',
  registered_at     INTEGER NOT NULL,
  last_heartbeat_at INTEGER NOT NULL,
  UNIQUE(project_id, name),
  UNIQUE(project_id, session_key)
);
CREATE INDEX IF NOT EXISTS ix_agents_live
  ON agents(project_id, status, last_heartbeat_at);

CREATE TABLE IF NOT EXISTS plans (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  title       TEXT NOT NULL,
  body_md     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'active',
  created_by  TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_plans_project ON plans(project_id, status);

CREATE TABLE IF NOT EXISTS tasks (
  id                TEXT PRIMARY KEY,
  project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  plan_id           TEXT REFERENCES plans(id) ON DELETE SET NULL,
  title             TEXT NOT NULL,
  body_md           TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'todo',
  priority          INTEGER NOT NULL DEFAULT 2,
  sort_key          REAL NOT NULL DEFAULT 1000,
  assignee_agent_id TEXT,
  assignee_name     TEXT,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  done_at           INTEGER
);
CREATE INDEX IF NOT EXISTS ix_tasks_board ON tasks(project_id, status, sort_key);
CREATE INDEX IF NOT EXISTS ix_tasks_plan  ON tasks(plan_id);

CREATE TABLE IF NOT EXISTS task_deps (
  task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY(task_id, depends_on_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS ix_deps_reverse ON task_deps(depends_on_id);

CREATE TABLE IF NOT EXISTS file_claims (
  project_id  TEXT NOT NULL,
  path        TEXT NOT NULL,
  agent_id    TEXT NOT NULL,
  agent_name  TEXT NOT NULL,
  task_id     TEXT,
  mode        TEXT NOT NULL DEFAULT 'write',
  claimed_at  INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  released_at INTEGER,
  PRIMARY KEY (project_id, path)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS ix_claims_agent ON file_claims(agent_id);

CREATE TABLE IF NOT EXISTS events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id   TEXT NOT NULL,
  type         TEXT NOT NULL,
  agent_id     TEXT,
  agent_name   TEXT,
  subject_id   TEXT,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_events_stream ON events(project_id, id);

-- The agent room: a single shared conversation per project, where an agent
-- that is unsure can ask instead of guessing, and the human can answer.
--
-- Threading is one level deep on purpose. Agents reply to a specific question;
-- nested sub-threads would be a structure nobody reads back.
CREATE TABLE IF NOT EXISTS messages (
  id           TEXT PRIMARY KEY,
  project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  parent_id    TEXT REFERENCES messages(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL DEFAULT 'message',  -- message|question|answer|announce
  author_id    TEXT,                             -- NULL when the human speaks
  author_name  TEXT NOT NULL,                    -- agent name, or HUMAN
  mentions     TEXT NOT NULL DEFAULT '',         -- comma-wrapped: ,ORION,VESPER,
  body_md      TEXT NOT NULL,
  resolved_at  INTEGER,                          -- questions only
  resolved_by  TEXT,
  created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_messages_room   ON messages(project_id, created_at);
CREATE INDEX IF NOT EXISTS ix_messages_thread ON messages(parent_id, created_at);
CREATE INDEX IF NOT EXISTS ix_messages_open   ON messages(project_id, kind, resolved_at);

-- Tracks how much of the room each agent has already been shown, so injection
-- can say "3 new messages" instead of repeating the whole conversation.
--
-- The watermark is a message id, not a timestamp. Two messages written in the
-- same millisecond share a created_at, so a time-based mark either replays one
-- forever or swallows it — ULIDs are monotonic and never tie.
CREATE TABLE IF NOT EXISTS room_reads (
  project_id  TEXT NOT NULL,
  agent_name  TEXT NOT NULL,
  last_seen   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (project_id, agent_name)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS decisions (
  id               TEXT PRIMARY KEY,
  project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL DEFAULT 'decision',
  title            TEXT NOT NULL,
  body_md          TEXT NOT NULL DEFAULT '',
  author_agent_id  TEXT,
  author_name      TEXT,
  target_agent_name TEXT,
  created_at       INTEGER NOT NULL,
  ack_at           INTEGER
);
CREATE INDEX IF NOT EXISTS ix_decisions_project ON decisions(project_id, created_at);
CREATE INDEX IF NOT EXISTS ix_decisions_target  ON decisions(project_id, target_agent_name, ack_at);
`
