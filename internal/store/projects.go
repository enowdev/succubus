package store

import (
	"database/sql"
	"errors"
)

func (s *Store) UpsertProject(id, displayName, rootPath, gitRemote string) (*Project, error) {
	ts := now()
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
