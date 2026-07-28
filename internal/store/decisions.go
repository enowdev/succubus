package store

import (
	"database/sql"
	"errors"
	"strings"
)

// CreateDecision records a decision, a note, or a handoff addressed to another
// agent. Handoffs are how an agent leaves instructions for whoever picks up
// next — the durable version of "tell the next session what I learned".
func (s *Store) CreateDecision(projectID, kind, title, bodyMD, authorID, authorName, target string) (*Decision, error) {
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("title required")
	}
	if kind == "" {
		kind = "decision"
	}
	d := &Decision{
		ID: NewID(), ProjectID: projectID, Kind: kind, Title: title, BodyMD: bodyMD,
		AuthorAgentID: authorID, AuthorName: authorName,
		TargetAgentName: strings.ToUpper(strings.TrimSpace(target)), CreatedAt: now(),
	}
	if _, err := s.writeDB.Exec(`
		INSERT INTO decisions(id, project_id, kind, title, body_md, author_agent_id, author_name, target_agent_name, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		d.ID, d.ProjectID, d.Kind, d.Title, d.BodyMD, nz(d.AuthorAgentID),
		nz(d.AuthorName), nz(d.TargetAgentName), d.CreatedAt); err != nil {
		return nil, err
	}

	evType := EvDecisionCreated
	if kind == "handoff" {
		evType = EvHandoff
	}
	s.Emit(Event{ProjectID: projectID, Type: evType, AgentID: authorID, AgentName: authorName,
		SubjectID: d.ID, Payload: map[string]any{"title": title, "target": d.TargetAgentName, "kind": kind}})
	return d, nil
}

func (s *Store) ListDecisions(projectID, target string, unackedOnly bool, since int64, limit int) ([]Decision, error) {
	q := `SELECT id, project_id, kind, title, body_md, author_agent_id, author_name,
	             target_agent_name, created_at, ack_at
	      FROM decisions WHERE project_id=?`
	args := []any{projectID}
	if target != "" {
		q += ` AND target_agent_name=?`
		args = append(args, strings.ToUpper(target))
	}
	if unackedOnly {
		q += ` AND ack_at IS NULL`
	}
	if since > 0 {
		q += ` AND created_at>?`
		args = append(args, since)
	}
	q += ` ORDER BY created_at DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.readDB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Decision{}
	for rows.Next() {
		var d Decision
		var aid, aname, tgt sql.NullString
		var ack sql.NullInt64
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Kind, &d.Title, &d.BodyMD,
			&aid, &aname, &tgt, &d.CreatedAt, &ack); err != nil {
			return nil, err
		}
		d.AuthorAgentID, d.AuthorName, d.TargetAgentName = ns(aid), ns(aname), ns(tgt)
		d.AckAt = ni(ack)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AckDecision(id string) error {
	res, err := s.writeDB.Exec(`UPDATE decisions SET ack_at=? WHERE id=? AND ack_at IS NULL`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteDecision(id string) error {
	res, err := s.writeDB.Exec(`DELETE FROM decisions WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEvents returns the activity log after a cursor, oldest first, so SSE
// clients can replay from Last-Event-ID.
func (s *Store) ListEvents(projectID string, after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.readDB.Query(`
		SELECT id, project_id, type, agent_id, agent_name, subject_id, payload_json, created_at
		FROM events WHERE project_id=? AND id>? ORDER BY id ASC LIMIT ?`,
		projectID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var e Event
		var aid, aname, sid sql.NullString
		var payload string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Type, &aid, &aname, &sid, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.AgentID, e.AgentName, e.SubjectID = ns(aid), ns(aname), ns(sid)
		e.Payload = rawJSON(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecentEvents returns the newest events first, for the activity feed.
func (s *Store) RecentEvents(projectID string, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.readDB.Query(`
		SELECT id, project_id, type, agent_id, agent_name, subject_id, payload_json, created_at
		FROM events WHERE project_id=? ORDER BY id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Event{}
	for rows.Next() {
		var e Event
		var aid, aname, sid sql.NullString
		var payload string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Type, &aid, &aname, &sid, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.AgentID, e.AgentName, e.SubjectID = ns(aid), ns(aname), ns(sid)
		e.Payload = rawJSON(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// rawJSON lets a stored payload pass through to the client without a
// decode/re-encode round trip.
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if r == "" {
		return []byte("{}"), nil
	}
	return []byte(r), nil
}
