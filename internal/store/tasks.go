package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// TaskFilter narrows a board query. Zero values mean "no filter".
type TaskFilter struct {
	Status   string
	Assignee string
	PlanID   string
}

func (s *Store) CreateTask(projectID, planID, title, bodyMD, status string, priority int, assigneeAgentID, assigneeName string, dependsOn []string) (*Task, error) {
	if strings.TrimSpace(title) == "" {
		return nil, errors.New("title required")
	}
	if status == "" {
		status = StatusTodo
	}
	if !ValidTaskStatus(status) {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	if priority == 0 {
		priority = 2
	}

	// New tasks land at the bottom of their column.
	var maxSort sql.NullFloat64
	s.readDB.QueryRow(`SELECT MAX(sort_key) FROM tasks WHERE project_id=? AND status=?`,
		projectID, status).Scan(&maxSort)
	sortKey := 1000.0
	if maxSort.Valid {
		sortKey = maxSort.Float64 + 1000
	}

	ts := now()
	t := &Task{
		ID: NewID(), ProjectID: projectID, PlanID: planID, Title: title, BodyMD: bodyMD,
		Status: status, Priority: priority, SortKey: sortKey,
		AssigneeAgentID: assigneeAgentID, AssigneeName: assigneeName,
		CreatedAt: ts, UpdatedAt: ts, DependsOn: []string{},
	}

	tx, err := s.writeDB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO tasks(id, project_id, plan_id, title, body_md, status, priority, sort_key,
		                  assignee_agent_id, assignee_name, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.ProjectID, nz(t.PlanID), t.Title, t.BodyMD, t.Status, t.Priority, t.SortKey,
		nz(t.AssigneeAgentID), nz(t.AssigneeName), ts, ts); err != nil {
		return nil, err
	}
	for _, dep := range dependsOn {
		if dep == "" || dep == t.ID {
			continue
		}
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO task_deps(task_id, depends_on_id) VALUES(?,?)`, t.ID, dep); err != nil {
			return nil, err
		}
		t.DependsOn = append(t.DependsOn, dep)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	s.Emit(Event{ProjectID: projectID, Type: EvTaskCreated, SubjectID: t.ID, AgentName: assigneeName,
		Payload: map[string]any{"title": title, "status": status}})
	return s.GetTask(t.ID)
}

func (s *Store) GetTask(id string) (*Task, error) {
	var t Task
	var planID, aid, aname sql.NullString
	var doneAt sql.NullInt64
	err := s.readDB.QueryRow(`
		SELECT id, project_id, plan_id, title, body_md, status, priority, sort_key,
		       assignee_agent_id, assignee_name, created_at, updated_at, done_at
		FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.ProjectID, &planID, &t.Title, &t.BodyMD, &t.Status, &t.Priority, &t.SortKey,
			&aid, &aname, &t.CreatedAt, &t.UpdatedAt, &doneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.PlanID, t.AssigneeAgentID, t.AssigneeName = ns(planID), ns(aid), ns(aname)
	t.DoneAt = ni(doneAt)

	deps, err := s.deps(t.ID)
	if err != nil {
		return nil, err
	}
	t.DependsOn = deps
	t.Blocked, _ = s.isBlocked(t.ID)
	return &t, nil
}

func (s *Store) deps(taskID string) ([]string, error) {
	rows, err := s.readDB.Query(`SELECT depends_on_id FROM task_deps WHERE task_id=?`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// isBlocked reports whether any dependency is still unfinished.
func (s *Store) isBlocked(taskID string) (bool, error) {
	var n int
	err := s.readDB.QueryRow(`
		SELECT COUNT(*) FROM task_deps d
		JOIN tasks t ON t.id = d.depends_on_id
		WHERE d.task_id=? AND t.status NOT IN ('done','cancelled')`, taskID).Scan(&n)
	return n > 0, err
}

func (s *Store) ListTasks(projectID string, f TaskFilter) ([]Task, error) {
	q := `SELECT id, project_id, plan_id, title, body_md, status, priority, sort_key,
	             assignee_agent_id, assignee_name, created_at, updated_at, done_at
	      FROM tasks WHERE project_id=?`
	args := []any{projectID}
	if f.Status != "" {
		q += ` AND status=?`
		args = append(args, f.Status)
	}
	if f.Assignee != "" {
		q += ` AND assignee_name=?`
		args = append(args, strings.ToUpper(f.Assignee))
	}
	if f.PlanID != "" {
		q += ` AND plan_id=?`
		args = append(args, f.PlanID)
	}
	q += ` ORDER BY sort_key ASC, created_at ASC`

	rows, err := s.readDB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Task{}
	ids := []string{}
	for rows.Next() {
		var t Task
		var planID, aid, aname sql.NullString
		var doneAt sql.NullInt64
		if err := rows.Scan(&t.ID, &t.ProjectID, &planID, &t.Title, &t.BodyMD, &t.Status,
			&t.Priority, &t.SortKey, &aid, &aname, &t.CreatedAt, &t.UpdatedAt, &doneAt); err != nil {
			return nil, err
		}
		t.PlanID, t.AssigneeAgentID, t.AssigneeName = ns(planID), ns(aid), ns(aname)
		t.DoneAt = ni(doneAt)
		t.DependsOn = []string{}
		out = append(out, t)
		ids = append(ids, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	// Load all deps in one pass rather than N queries.
	depMap, blockedSet, err := s.depsBulk(projectID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if d, ok := depMap[out[i].ID]; ok {
			out[i].DependsOn = d
		}
		out[i].Blocked = blockedSet[out[i].ID]
	}
	return out, nil
}

// depsBulk returns dependency lists and the blocked set for a whole project.
func (s *Store) depsBulk(projectID string) (map[string][]string, map[string]bool, error) {
	rows, err := s.readDB.Query(`
		SELECT d.task_id, d.depends_on_id, COALESCE(dep.status,'')
		FROM task_deps d
		JOIN tasks t   ON t.id = d.task_id
		LEFT JOIN tasks dep ON dep.id = d.depends_on_id
		WHERE t.project_id=?`, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	deps := map[string][]string{}
	blocked := map[string]bool{}
	for rows.Next() {
		var taskID, depID, depStatus string
		if err := rows.Scan(&taskID, &depID, &depStatus); err != nil {
			return nil, nil, err
		}
		deps[taskID] = append(deps[taskID], depID)
		if depStatus != "" && depStatus != StatusDone && depStatus != StatusCancelled {
			blocked[taskID] = true
		}
	}
	return deps, blocked, rows.Err()
}

// TaskPatch carries partial updates; nil fields are left unchanged.
type TaskPatch struct {
	Title           *string  `json:"title"`
	BodyMD          *string  `json:"body_md"`
	Status          *string  `json:"status"`
	Priority        *int     `json:"priority"`
	SortKey         *float64 `json:"sort_key"`
	PlanID          *string  `json:"plan_id"`
	AssigneeAgentID *string  `json:"assignee_agent_id"`
	AssigneeName    *string  `json:"assignee_name"`
}

func (s *Store) UpdateTask(id string, patch TaskPatch) (*Task, error) {
	t, err := s.GetTask(id)
	if err != nil {
		return nil, err
	}
	prevStatus := t.Status

	if patch.Title != nil {
		t.Title = *patch.Title
	}
	if patch.BodyMD != nil {
		t.BodyMD = *patch.BodyMD
	}
	if patch.Status != nil {
		if !ValidTaskStatus(*patch.Status) {
			return nil, fmt.Errorf("invalid status %q", *patch.Status)
		}
		t.Status = *patch.Status
	}
	if patch.Priority != nil {
		t.Priority = *patch.Priority
	}
	if patch.SortKey != nil {
		t.SortKey = *patch.SortKey
	}
	if patch.PlanID != nil {
		t.PlanID = *patch.PlanID
	}
	if patch.AssigneeAgentID != nil {
		t.AssigneeAgentID = *patch.AssigneeAgentID
	}
	if patch.AssigneeName != nil {
		t.AssigneeName = strings.ToUpper(*patch.AssigneeName)
	}

	t.UpdatedAt = now()
	if t.Status == StatusDone && prevStatus != StatusDone {
		t.DoneAt = t.UpdatedAt
	} else if t.Status != StatusDone {
		t.DoneAt = 0
	}

	var doneAt any
	if t.DoneAt > 0 {
		doneAt = t.DoneAt
	}
	if _, err := s.writeDB.Exec(`
		UPDATE tasks SET title=?, body_md=?, status=?, priority=?, sort_key=?, plan_id=?,
		                 assignee_agent_id=?, assignee_name=?, updated_at=?, done_at=?
		WHERE id=?`,
		t.Title, t.BodyMD, t.Status, t.Priority, t.SortKey, nz(t.PlanID),
		nz(t.AssigneeAgentID), nz(t.AssigneeName), t.UpdatedAt, doneAt, id); err != nil {
		return nil, err
	}

	// Finishing a task frees the files claimed for it. Without this an agent
	// that marks work done and moves on keeps holding files it will never
	// touch again, until the lease happens to expire.
	if (t.Status == StatusDone || t.Status == StatusCancelled) && prevStatus != t.Status {
		if n, err := s.ReleaseClaimsForTask(t.ProjectID, t.ID); err == nil && n > 0 {
			s.Emit(Event{ProjectID: t.ProjectID, Type: EvClaimReleased, AgentName: t.AssigneeName,
				SubjectID: t.ID, Payload: map[string]any{"reason": "task " + t.Status, "count": n}})
		}
	}

	evType := EvTaskUpdated
	if prevStatus != t.Status {
		evType = EvTaskMoved
	}
	s.Emit(Event{ProjectID: t.ProjectID, Type: evType, SubjectID: t.ID, AgentName: t.AssigneeName,
		Payload: map[string]any{"title": t.Title, "status": t.Status, "from": prevStatus}})
	return s.GetTask(id)
}

func (s *Store) DeleteTask(id string) error {
	t, err := s.GetTask(id)
	if err != nil {
		return err
	}
	if _, err := s.writeDB.Exec(`DELETE FROM tasks WHERE id=?`, id); err != nil {
		return err
	}
	s.Emit(Event{ProjectID: t.ProjectID, Type: EvTaskDeleted, SubjectID: id,
		Payload: map[string]any{"title": t.Title}})
	return nil
}

// AddDep links task -> dependsOn, refusing edges that would create a cycle.
func (s *Store) AddDep(taskID, dependsOn string) error {
	if taskID == dependsOn {
		return errors.New("a task cannot depend on itself")
	}
	if _, err := s.GetTask(dependsOn); err != nil {
		return fmt.Errorf("dependency %s: %w", dependsOn, err)
	}
	cyclic, err := s.reaches(dependsOn, taskID)
	if err != nil {
		return err
	}
	if cyclic {
		return errors.New("dependency would create a cycle")
	}
	if _, err := s.writeDB.Exec(
		`INSERT OR IGNORE INTO task_deps(task_id, depends_on_id) VALUES(?,?)`, taskID, dependsOn); err != nil {
		return err
	}
	t, err := s.GetTask(taskID)
	if err == nil {
		s.Emit(Event{ProjectID: t.ProjectID, Type: EvTaskUpdated, SubjectID: taskID,
			Payload: map[string]any{"dep_added": dependsOn}})
	}
	return nil
}

// reaches reports whether `from` can reach `target` by following dependencies.
func (s *Store) reaches(from, target string) (bool, error) {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true, nil
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		next, err := s.deps(cur)
		if err != nil {
			return false, err
		}
		stack = append(stack, next...)
	}
	return false, nil
}

func (s *Store) RemoveDep(taskID, dependsOn string) error {
	_, err := s.writeDB.Exec(
		`DELETE FROM task_deps WHERE task_id=? AND depends_on_id=?`, taskID, dependsOn)
	if err == nil {
		if t, e := s.GetTask(taskID); e == nil {
			s.Emit(Event{ProjectID: t.ProjectID, Type: EvTaskUpdated, SubjectID: taskID,
				Payload: map[string]any{"dep_removed": dependsOn}})
		}
	}
	return err
}

// ReorderTask moves a task to a column at a position, recomputing its sort key
// as the midpoint between neighbours. Fractional keys avoid renumbering the
// whole column on every drag.
func (s *Store) ReorderTask(projectID, taskID, status string, index int) (*Task, error) {
	if !ValidTaskStatus(status) {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	rows, err := s.readDB.Query(
		`SELECT id, sort_key FROM tasks WHERE project_id=? AND status=? AND id!=? ORDER BY sort_key ASC`,
		projectID, status, taskID)
	if err != nil {
		return nil, err
	}
	var keys []float64
	for rows.Next() {
		var id string
		var k float64
		if err := rows.Scan(&id, &k); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()

	if index < 0 {
		index = 0
	}
	if index > len(keys) {
		index = len(keys)
	}

	var newKey float64
	switch {
	case len(keys) == 0:
		newKey = 1000
	case index == 0:
		newKey = keys[0] - 1000
	case index == len(keys):
		newKey = keys[len(keys)-1] + 1000
	default:
		newKey = (keys[index-1] + keys[index]) / 2
	}

	return s.UpdateTask(taskID, TaskPatch{Status: &status, SortKey: &newKey})
}

// ClaimTask assigns a task to an agent, refusing to steal one that another live
// agent is already working on.
func (s *Store) ClaimTask(taskID, agentID, agentName string, force bool) (*Task, error) {
	t, err := s.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if !force && t.AssigneeAgentID != "" && t.AssigneeAgentID != agentID {
		if a, err := s.GetAgent(t.AssigneeAgentID); err == nil && a.Status != AgentDead {
			return nil, fmt.Errorf("task already assigned to %s", a.Name)
		}
	}
	inProgress := StatusInProgress
	patch := TaskPatch{AssigneeAgentID: &agentID, AssigneeName: &agentName}
	if t.Status == StatusTodo {
		patch.Status = &inProgress
	}
	return s.UpdateTask(taskID, patch)
}
