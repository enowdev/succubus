package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Message kinds. A question stays open until someone resolves it, which is what
// makes the room useful: an agent can see what is still unanswered.
const (
	MsgMessage  = "message"
	MsgQuestion = "question"
	MsgAnswer   = "answer"
	MsgAnnounce = "announce"
)

// HumanAuthor is the name used when a person posts from the dashboard.
const HumanAuthor = "HUMAN"

// MentionAll addresses every agent in the project at once.
const MentionAll = "ALL"

type Message struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	ParentID   string    `json:"parent_id,omitempty"`
	Kind       string    `json:"kind"`
	AuthorID   string    `json:"author_id,omitempty"`
	AuthorName string    `json:"author_name"`
	Mentions   []string  `json:"mentions"`
	BodyMD     string    `json:"body_md"`
	ResolvedAt int64     `json:"resolved_at,omitempty"`
	ResolvedBy string    `json:"resolved_by,omitempty"`
	CreatedAt  int64     `json:"created_at"`
	Replies    []Message `json:"replies,omitempty"`
	// DirectMention is set per-reader by UnreadFor: true when this agent was
	// named specifically, as opposed to being caught by @ALL.
	DirectMention bool `json:"direct_mention,omitempty"`
}

// encodeMentions wraps names in commas so a LIKE '%,NAME,%' match cannot hit a
// partial name — ORION must not match ORIONA.
func encodeMentions(names []string) string {
	clean := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(n, "@")))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		clean = append(clean, n)
	}
	if len(clean) == 0 {
		return ""
	}
	return "," + strings.Join(clean, ",") + ","
}

func decodeMentions(s string) []string {
	s = strings.Trim(s, ",")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, ",")
}

// extractMentions pulls @NAME references out of the body, so an agent writing
// naturally does not also have to fill in a mentions array.
func extractMentions(body string) []string {
	var out []string
	for i := 0; i < len(body); i++ {
		if body[i] != '@' {
			continue
		}
		j := i + 1
		for j < len(body) && (isNameByte(body[j])) {
			j++
		}
		if j > i+1 {
			out = append(out, body[i+1:j])
		}
		i = j
	}
	return out
}

func isNameByte(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') || b == '-' || b == '_'
}

// PostMessage adds to the room. Passing parentID makes it a reply.
func (s *Store) PostMessage(projectID, parentID, kind, authorID, authorName, body string, mentions []string) (*Message, error) {
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("message body required")
	}
	if kind == "" {
		kind = MsgMessage
	}
	if authorName == "" {
		authorName = HumanAuthor
	}
	// A reply to a question is an answer unless it says otherwise.
	if parentID != "" && kind == MsgMessage {
		var parentKind string
		if err := s.readDB.QueryRow(`SELECT kind FROM messages WHERE id=?`, parentID).
			Scan(&parentKind); err == nil && parentKind == MsgQuestion {
			kind = MsgAnswer
		}
	}

	all := append(append([]string{}, mentions...), extractMentions(body)...)
	m := &Message{
		ID: NewID(), ProjectID: projectID, ParentID: parentID, Kind: kind,
		AuthorID: authorID, AuthorName: strings.ToUpper(authorName),
		Mentions: decodeMentions(encodeMentions(all)),
		BodyMD:   body, CreatedAt: now(),
	}

	if _, err := s.writeDB.Exec(`
		INSERT INTO messages(id, project_id, parent_id, kind, author_id, author_name, mentions, body_md, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ProjectID, nz(m.ParentID), m.Kind, nz(m.AuthorID), m.AuthorName,
		encodeMentions(all), m.BodyMD, m.CreatedAt); err != nil {
		return nil, err
	}

	s.Emit(Event{ProjectID: projectID, Type: EvRoomMessage, AgentID: authorID,
		AgentName: m.AuthorName, SubjectID: m.ID,
		Payload: map[string]any{
			"kind": kind, "mentions": m.Mentions, "parent_id": parentID,
			"preview": preview(body, 120),
		}})
	return m, nil
}

// ResolveQuestion closes an open question.
func (s *Store) ResolveQuestion(id, by string) error {
	res, err := s.writeDB.Exec(
		`UPDATE messages SET resolved_at=?, resolved_by=? WHERE id=? AND kind='question' AND resolved_at IS NULL`,
		now(), strings.ToUpper(by), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	var projectID string
	s.readDB.QueryRow(`SELECT project_id FROM messages WHERE id=?`, id).Scan(&projectID)
	if projectID != "" {
		s.Emit(Event{ProjectID: projectID, Type: EvRoomResolved, AgentName: strings.ToUpper(by),
			SubjectID: id})
	}
	return nil
}

func (s *Store) DeleteMessage(id string) error {
	res, err := s.writeDB.Exec(`DELETE FROM messages WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RoomMessages returns top-level messages newest-first with their replies
// attached oldest-first — the shape a conversation is actually read in.
func (s *Store) RoomMessages(projectID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}

	rows, err := s.readDB.Query(`
		SELECT id, project_id, COALESCE(parent_id,''), kind, COALESCE(author_id,''),
		       author_name, mentions, body_md, resolved_at, COALESCE(resolved_by,''), created_at
		FROM messages WHERE project_id=? AND parent_id IS NULL
		ORDER BY created_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	roots, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(roots) == 0 {
		return roots, nil
	}

	// One query for every reply in the page, rather than one per root.
	ids := make([]any, 0, len(roots))
	byID := map[string]int{}
	for i := range roots {
		ids = append(ids, roots[i].ID)
		byID[roots[i].ID] = i
		roots[i].Replies = []Message{}
	}
	q := `SELECT id, project_id, COALESCE(parent_id,''), kind, COALESCE(author_id,''),
	             author_name, mentions, body_md, resolved_at, COALESCE(resolved_by,''), created_at
	      FROM messages WHERE parent_id IN (?` + strings.Repeat(",?", len(ids)-1) + `)
	      ORDER BY created_at ASC`
	rows, err = s.readDB.Query(q, ids...)
	if err != nil {
		return nil, err
	}
	replies, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	for _, r := range replies {
		if i, ok := byID[r.ParentID]; ok {
			roots[i].Replies = append(roots[i].Replies, r)
		}
	}
	return roots, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var mentions string
		var resolvedAt sql.NullInt64
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.ParentID, &m.Kind, &m.AuthorID,
			&m.AuthorName, &mentions, &m.BodyMD, &resolvedAt, &m.ResolvedBy, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Mentions = decodeMentions(mentions)
		m.ResolvedAt = ni(resolvedAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// OpenQuestions lists unanswered questions, so context injection can surface
// them — an unanswered question nobody sees is the failure mode to avoid.
func (s *Store) OpenQuestions(projectID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.readDB.Query(`
		SELECT id, project_id, COALESCE(parent_id,''), kind, COALESCE(author_id,''),
		       author_name, mentions, body_md, resolved_at, COALESCE(resolved_by,''), created_at
		FROM messages
		WHERE project_id=? AND kind='question' AND resolved_at IS NULL
		ORDER BY created_at ASC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// UnreadFor returns messages this agent has not been shown yet, excluding its
// own. Mentions come back separately because they deserve louder treatment.
func (s *Store) UnreadFor(projectID, agentName string, limit int) (unread []Message, mentioned []Message, err error) {
	agentName = strings.ToUpper(agentName)
	if agentName == "" {
		return nil, nil, nil
	}

	// The watermark is the id of the last message this agent was shown. Read it
	// on the write connection: the read pool can hold an older WAL snapshot, and
	// a mark not yet visible there would replay messages the agent has seen.
	var seenID string
	s.writeDB.QueryRow(`SELECT last_seen FROM room_reads WHERE project_id=? AND agent_name=?`,
		projectID, agentName).Scan(&seenID)

	if limit <= 0 {
		limit = 20
	}
	rows, err := s.writeDB.Query(`
		SELECT id, project_id, COALESCE(parent_id,''), kind, COALESCE(author_id,''),
		       author_name, mentions, body_md, resolved_at, COALESCE(resolved_by,''), created_at
		FROM messages
		WHERE project_id=? AND id>? AND author_name!=?
		ORDER BY id ASC LIMIT ?`, projectID, seenID, agentName, limit)
	if err != nil {
		return nil, nil, err
	}
	all, err := scanMessages(rows)
	if err != nil {
		return nil, nil, err
	}

	needle := "," + agentName + ","
	for _, m := range all {
		joined := "," + strings.Join(m.Mentions, ",") + ","
		switch {
		case strings.Contains(joined, needle):
			// Addressed by name: the strongest signal there is.
			m.DirectMention = true
			mentioned = append(mentioned, m)
		case strings.Contains(joined, ","+MentionAll+","):
			// @ALL reaches everyone, but it is a broadcast — worth showing,
			// not worth treating as "someone is waiting on you".
			mentioned = append(mentioned, m)
		default:
			unread = append(unread, m)
		}
	}
	return unread, mentioned, nil
}

// MarkRoomRead records that this agent has been shown the room as it stands.
//
// The mark is the newest message id, not the wall clock: two messages written
// in the same millisecond share a created_at, and a time-based watermark would
// sweep up the later one and hide it forever.
func (s *Store) MarkRoomRead(projectID, agentName string) error {
	if agentName == "" {
		return nil
	}
	var newest string
	// COALESCE keeps an empty room from leaving the watermark NULL.
	if err := s.writeDB.QueryRow(
		`SELECT COALESCE(MAX(id), '') FROM messages WHERE project_id=?`,
		projectID).Scan(&newest); err != nil {
		return err
	}
	_, err := s.writeDB.Exec(`
		INSERT INTO room_reads(project_id, agent_name, last_seen) VALUES(?,?,?)
		ON CONFLICT(project_id, agent_name) DO UPDATE SET
		  last_seen = MAX(room_reads.last_seen, excluded.last_seen)`,
		projectID, strings.ToUpper(agentName), newest)
	return err
}

// RoomStats summarises the room for the dashboard header.
func (s *Store) RoomStats(projectID string) (total, open int, err error) {
	err = s.readDB.QueryRow(`SELECT COUNT(*) FROM messages WHERE project_id=?`, projectID).Scan(&total)
	if err != nil {
		return 0, 0, err
	}
	err = s.readDB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE project_id=? AND kind='question' AND resolved_at IS NULL`,
		projectID).Scan(&open)
	return total, open, err
}

func preview(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatMessage renders one message for injection into an agent's context.
func FormatMessage(m Message) string {
	tag := ""
	switch m.Kind {
	case MsgQuestion:
		tag = "[QUESTION] "
	case MsgAnswer:
		tag = "[answer] "
	case MsgAnnounce:
		tag = "[announce] "
	}
	return fmt.Sprintf("%s%s: %s", tag, m.AuthorName, preview(m.BodyMD, 300))
}
