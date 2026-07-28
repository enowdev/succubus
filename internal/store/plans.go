package store

import (
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) CreatePlan(projectID, title, bodyMD, status, createdBy string) (*Plan, error) {
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("title required")
	}
	if status == "" {
		status = "active"
	}
	ts := now()
	p := &Plan{
		ID: NewID(), ProjectID: projectID, Title: title, BodyMD: bodyMD,
		Status: status, CreatedBy: createdBy, CreatedAt: ts, UpdatedAt: ts,
	}
	if _, err := s.writeDB.Exec(`
		INSERT INTO plans(id, project_id, title, body_md, status, created_by, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		p.ID, p.ProjectID, p.Title, p.BodyMD, p.Status, nz(p.CreatedBy), ts, ts); err != nil {
		return nil, err
	}
	s.Emit(Event{ProjectID: projectID, Type: EvPlanCreated, SubjectID: p.ID,
		Payload: map[string]any{"title": title}})
	return p, nil
}

func (s *Store) GetPlan(id string) (*Plan, error) {
	var p Plan
	var by sql.NullString
	err := s.readDB.QueryRow(`
		SELECT id, project_id, title, body_md, status, created_by, created_at, updated_at
		FROM plans WHERE id=?`, id).
		Scan(&p.ID, &p.ProjectID, &p.Title, &p.BodyMD, &p.Status, &by, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedBy = ns(by)
	return &p, nil
}

func (s *Store) ListPlans(projectID string) ([]Plan, error) {
	rows, err := s.readDB.Query(`
		SELECT id, project_id, title, body_md, status, created_by, created_at, updated_at
		FROM plans WHERE project_id=? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Plan{}
	for rows.Next() {
		var p Plan
		var by sql.NullString
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Title, &p.BodyMD, &p.Status,
			&by, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.CreatedBy = ns(by)
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlanPatch carries partial updates; nil fields are left unchanged.
type PlanPatch struct {
	Title  *string `json:"title"`
	BodyMD *string `json:"body_md"`
	Status *string `json:"status"`
}

func (s *Store) UpdatePlan(id string, patch PlanPatch) (*Plan, error) {
	p, err := s.GetPlan(id)
	if err != nil {
		return nil, err
	}
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	if patch.BodyMD != nil {
		p.BodyMD = *patch.BodyMD
	}
	if patch.Status != nil {
		p.Status = *patch.Status
	}
	p.UpdatedAt = now()

	if _, err := s.writeDB.Exec(
		`UPDATE plans SET title=?, body_md=?, status=?, updated_at=? WHERE id=?`,
		p.Title, p.BodyMD, p.Status, p.UpdatedAt, id); err != nil {
		return nil, err
	}
	s.Emit(Event{ProjectID: p.ProjectID, Type: EvPlanUpdated, SubjectID: p.ID,
		Payload: map[string]any{"title": p.Title, "status": p.Status}})
	return p, nil
}

func (s *Store) DeletePlan(id string) error {
	p, err := s.GetPlan(id)
	if err != nil {
		return err
	}
	if _, err := s.writeDB.Exec(`DELETE FROM plans WHERE id=?`, id); err != nil {
		return err
	}
	s.Emit(Event{ProjectID: p.ProjectID, Type: EvPlanDeleted, SubjectID: id})
	return nil
}
