package store

import (
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) UpsertProject(id, displayName, rootPath, gitRemote string) (*Project, error) {
	ts := now()

	// Running `git remote add origin` changes how this project's id is derived:
	// before a remote exists it is hashed from the path, afterwards from the
	// remote. Without this, the moment a local repo is pushed to GitHub its plan,
	// tasks, decisions and history are silently orphaned and the dashboard shows
	// an empty project beside a full one it no longer links to.
	if err := s.adoptPreRemoteProject(id, rootPath, gitRemote); err != nil {
		return nil, err
	}

	_, err := s.writeDB.Exec(`
		INSERT INTO projects(id, display_name, root_path, git_remote, created_at, last_seen_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  display_name=excluded.display_name,
		  root_path=excluded.root_path,
		  git_remote=COALESCE(excluded.git_remote, projects.git_remote),
		  last_seen_at=excluded.last_seen_at`,
		id, displayName, rootPath, nz(gitRemote), ts, ts)
	if err != nil {
		return nil, err
	}
	return s.GetProject(id)
}

// projectChildTables are every table keyed by project_id. A migration that
// misses one leaves data pointing at a project that no longer exists.
var projectChildTables = []string{
	"agents", "plans", "tasks", "file_claims",
	"events", "messages", "room_reads", "decisions",
}

// adoptPreRemoteProject moves the history of a path-derived project onto its
// new remote-derived id.
//
// It fires only in the one situation where this is unambiguous: the incoming
// project has a remote, an older row exists for the *same root path* with no
// remote at all, and no row yet exists for the new id. Anything less specific
// risks merging two projects that were meant to stay apart, which is far worse
// than the orphaning it fixes — so when in doubt it does nothing.
func (s *Store) adoptPreRemoteProject(newID, rootPath, gitRemote string) error {
	if gitRemote == "" || rootPath == "" {
		return nil // nothing to adopt onto
	}

	var oldID string
	err := s.readDB.QueryRow(
		`SELECT id FROM projects
		  WHERE root_path = ? AND id != ? AND (git_remote IS NULL OR git_remote = '')
		  ORDER BY created_at LIMIT 1`, rootPath, newID).Scan(&oldID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	// Only adopt into a project that does not exist yet. If both ids already
	// carry data they are two histories, and silently merging them would lose
	// the distinction.
	var exists int
	if err := s.readDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = ?`, newID).
		Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	// There is no ordering that satisfies the foreign keys statement by statement:
	// moving the parent first orphans the children, and moving the children first
	// points them at a project that does not exist yet. Either way SQLite raises
	// FOREIGN KEY constraint failed (787) on the very first UPDATE.
	//
	// So defer the checks to commit time. The rows are consistent by the end of
	// the transaction, which is what the constraint actually cares about — and if
	// anything below is wrong the commit fails and nothing moves.
	if _, err := s.writeDB.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	defer s.writeDB.Exec(`PRAGMA defer_foreign_keys = OFF`)

	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE projects SET id = ?, git_remote = ? WHERE id = ?`,
		newID, gitRemote, oldID); err != nil {
		return err
	}
	for _, table := range projectChildTables {
		if _, err := tx.Exec(
			`UPDATE `+table+` SET project_id = ? WHERE project_id = ?`, newID, oldID); err != nil {
			return fmt.Errorf("re-keying %s: %w", table, err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetProject(id string) (*Project, error) {
	var p Project
	var remote sql.NullString
	err := s.readDB.QueryRow(
		`SELECT id, display_name, root_path, git_remote, created_at, last_seen_at FROM projects WHERE id=?`, id).
		Scan(&p.ID, &p.DisplayName, &p.RootPath, &remote, &p.CreatedAt, &p.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.GitRemote = ns(remote)
	return &p, nil
}

func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.readDB.Query(
		`SELECT id, display_name, root_path, git_remote, created_at, last_seen_at
		 FROM projects ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		var p Project
		var remote sql.NullString
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.RootPath, &remote, &p.CreatedAt, &p.LastSeenAt); err != nil {
			return nil, err
		}
		p.GitRemote = ns(remote)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProject removes a project and everything recorded under it.
//
// Agents, plans, tasks, decisions and room messages cascade from the foreign
// key. Claims and events carry a project_id but no constraint, so they are
// cleared explicitly — otherwise they would linger and a project re-registered
// at the same path would inherit stale locks.
func (s *Store) DeleteProject(id string) error {
	tx, err := s.writeDB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, q := range []string{
		`DELETE FROM file_claims WHERE project_id=?`,
		`DELETE FROM events      WHERE project_id=?`,
		`DELETE FROM room_reads  WHERE project_id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrNotFound is returned when a lookup by id yields nothing.
var ErrNotFound = errors.New("not found")
