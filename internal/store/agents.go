package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// NamePool is the curated set of agent identities. Names are short, distinct,
// and easy to say out loud — an agent has to adopt one as its own name, and
// humans have to be able to refer to it in conversation.
var NamePool = []string{
	"ORION", "VESPER", "KESTREL", "LYRA", "ATLAS", "NOVA", "RAVEN", "ZEPHYR",
	"ONYX", "SABLE", "CIPHER", "HALCYON", "MERIDIAN", "QUILL", "TALON", "EMBER",
	"SOLACE", "VERTEX", "WREN", "AZURE", "COBALT", "DUSK", "FLINT", "GROVE",
	"HAVEN", "INDIGO", "JUNIPER", "KAIROS", "LUMEN", "MIRAGE", "NIMBUS", "OCTAVE",
}

// Heartbeat thresholds.
//
// What these actually measure is worth being precise about: an agent has no
// process of its own between turns, so heartbeats only arrive when the human
// sends it a prompt. "active" therefore means *recently given work*, not
// *currently thinking*, and an idle agent cannot act on anything — it will read
// the room and reply only on its next turn.
const (
	HeartbeatIntervalMS int64 = 30_000
	ActiveToIdleMS      int64 = 90_000
	IdleToDeadMS        int64 = 300_000
)

// RegisterAgent adopts an identity for a session.
//
// Registration is idempotent on (project_id, session_key): a resumed session
// gets back the *same* agent with the same name, while three concurrent
// sessions of the same tool each get a distinct name. This is what lets an
// agent keep believing it is ORION across compaction and restarts.
func (s *Store) RegisterAgent(projectID, preferredName, tool, sessionKey, cwd string, pid int) (*Agent, bool, error) {
	if sessionKey == "" {
		return nil, false, errors.New("session_key required")
	}
	ts := now()

	tx, err := s.writeDB.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	// Existing session? Return the same identity.
	var a Agent
	var pidNull sql.NullInt64
	var cwdNull sql.NullString
	err = tx.QueryRow(`
		SELECT id, project_id, name, tool, session_key, pid, cwd, status, registered_at, last_heartbeat_at
		FROM agents WHERE project_id=? AND session_key=?`, projectID, sessionKey).
		Scan(&a.ID, &a.ProjectID, &a.Name, &a.Tool, &a.SessionKey, &pidNull, &cwdNull,
			&a.Status, &a.RegisteredAt, &a.LastHeartbeatAt)
	if err == nil {
		if _, err := tx.Exec(
			`UPDATE agents SET status='active', last_heartbeat_at=?, pid=?, cwd=? WHERE id=?`,
			ts, pid, nz(cwd), a.ID); err != nil {
			return nil, false, err
		}
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		a.Status = AgentActive
		a.LastHeartbeatAt = ts
		a.PID = pid
		a.CWD = cwd
		return &a, true, nil // reused
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}

	name, err := pickName(tx, projectID, preferredName)
	if err != nil {
		return nil, false, err
	}

	a = Agent{
		ID: NewID(), ProjectID: projectID, Name: name, Tool: tool,
		SessionKey: sessionKey, PID: pid, CWD: cwd, Status: AgentActive,
		RegisteredAt: ts, LastHeartbeatAt: ts,
	}
	if _, err := tx.Exec(`
		INSERT INTO agents(id, project_id, name, tool, session_key, pid, cwd, status, registered_at, last_heartbeat_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.Name, a.Tool, a.SessionKey, pid, nz(cwd), a.Status, ts, ts); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}

	s.Emit(Event{ProjectID: projectID, Type: EvAgentRegistered, AgentID: a.ID, AgentName: a.Name,
		Payload: map[string]any{"tool": tool, "cwd": cwd}})
	return &a, false, nil
}

// pickName finds a free identity, preferring the requested one. Uniqueness is
// enforced by UNIQUE(project_id, name); this scan just avoids the collision.
func pickName(tx *sql.Tx, projectID, preferred string) (string, error) {
	taken := map[string]bool{}
	rows, err := tx.Query(`SELECT name FROM agents WHERE project_id=?`, projectID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		taken[strings.ToUpper(n)] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	if p := strings.ToUpper(strings.TrimSpace(preferred)); p != "" && !taken[p] {
		return p, nil
	}
	for _, n := range NamePool {
		if !taken[n] {
			return n, nil
		}
	}
	// Pool exhausted: suffix until free.
	for i := 2; ; i++ {
		for _, n := range NamePool {
			cand := fmt.Sprintf("%s-%d", n, i)
			if !taken[cand] {
				return cand, nil
			}
		}
	}
}

func (s *Store) GetAgent(id string) (*Agent, error) {
	var a Agent
	var pid sql.NullInt64
	var cwd sql.NullString
	err := s.readDB.QueryRow(`
		SELECT id, project_id, name, tool, session_key, pid, cwd, status, registered_at, last_heartbeat_at
		FROM agents WHERE id=?`, id).
		Scan(&a.ID, &a.ProjectID, &a.Name, &a.Tool, &a.SessionKey, &pid, &cwd,
			&a.Status, &a.RegisteredAt, &a.LastHeartbeatAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.PID = int(ni(pid))
	a.CWD = ns(cwd)
	return &a, nil
}

func (s *Store) ListAgents(projectID string, includeDead bool) ([]Agent, error) {
	q := `SELECT id, project_id, name, tool, session_key, pid, cwd, status, registered_at, last_heartbeat_at
	      FROM agents WHERE project_id=?`
	if !includeDead {
		q += ` AND status!='dead'`
	}
	q += ` ORDER BY registered_at ASC`

	rows, err := s.readDB.Query(q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Agent{}
	for rows.Next() {
		var a Agent
		var pid sql.NullInt64
		var cwd sql.NullString
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Tool, &a.SessionKey, &pid, &cwd,
			&a.Status, &a.RegisteredAt, &a.LastHeartbeatAt); err != nil {
			return nil, err
		}
		a.PID = int(ni(pid))
		a.CWD = ns(cwd)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Heartbeat(agentID string) error {
	_, err := s.writeDB.Exec(
		`UPDATE agents SET last_heartbeat_at=?, status='active' WHERE id=?`, now(), agentID)
	return err
}

func (s *Store) RenameAgent(agentID, newName string) (*Agent, error) {
	a, err := s.GetAgent(agentID)
	if err != nil {
		return nil, err
	}
	name := strings.ToUpper(strings.TrimSpace(newName))
	if name == "" {
		return nil, errors.New("name required")
	}
	if _, err := s.writeDB.Exec(`UPDATE agents SET name=? WHERE id=?`, name, agentID); err != nil {
		return nil, fmt.Errorf("rename (name may be taken): %w", err)
	}
	// Keep task attribution readable after a rename.
	s.writeDB.Exec(`UPDATE tasks SET assignee_name=? WHERE assignee_agent_id=?`, name, agentID)
	s.writeDB.Exec(`UPDATE file_claims SET agent_name=? WHERE agent_id=?`, name, agentID)

	old := a.Name
	a.Name = name
	s.Emit(Event{ProjectID: a.ProjectID, Type: EvAgentRegistered, AgentID: a.ID, AgentName: name,
		Payload: map[string]any{"renamed_from": old}})
	return a, nil
}

// UnregisterAgent marks an agent gone and frees everything it held.
func (s *Store) UnregisterAgent(agentID string) error {
	a, err := s.GetAgent(agentID)
	if err != nil {
		return err
	}
	if _, err := s.writeDB.Exec(`UPDATE agents SET status='dead' WHERE id=?`, agentID); err != nil {
		return err
	}
	s.ReleaseAllForAgent(a.ProjectID, agentID)
	s.Emit(Event{ProjectID: a.ProjectID, Type: EvAgentLeft, AgentID: agentID, AgentName: a.Name})
	return nil
}

// SweepAgents demotes silent agents and releases the claims of dead ones.
// Returns how many transitioned to dead.
func (s *Store) SweepAgents() (int, error) {
	ts := now()

	if _, err := s.writeDB.Exec(
		`UPDATE agents SET status='idle' WHERE status='active' AND last_heartbeat_at < ?`,
		ts-ActiveToIdleMS); err != nil {
		return 0, err
	}

	rows, err := s.writeDB.Query(
		`SELECT id, project_id, name FROM agents WHERE status IN ('active','idle') AND last_heartbeat_at < ?`,
		ts-IdleToDeadMS)
	if err != nil {
		return 0, err
	}
	type dead struct{ id, project, name string }
	var list []dead
	for rows.Next() {
		var d dead
		if err := rows.Scan(&d.id, &d.project, &d.name); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()

	for _, d := range list {
		s.writeDB.Exec(`UPDATE agents SET status='dead' WHERE id=?`, d.id)
		s.ReleaseAllForAgent(d.project, d.id)
		s.Emit(Event{ProjectID: d.project, Type: EvAgentLeft, AgentID: d.id, AgentName: d.name,
			Payload: map[string]any{"reason": "heartbeat timeout"}})
	}
	return len(list), nil
}
