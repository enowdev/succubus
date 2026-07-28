package store

import (
	"database/sql"
	"errors"
	"runtime"
	"sort"
	"strings"
)

// DefaultLeaseTTLSec is how long a file claim survives without renewal.
// Short enough that a crashed agent frees its files within a coffee break,
// long enough to survive a slow edit-test cycle.
const DefaultLeaseTTLSec = 900 // 15 minutes

// NormalizePath makes a claim path comparable across agents. Agents report
// paths in wildly different shapes (absolute, ./relative, backslashes), and a
// mismatch here silently defeats the whole locking scheme.
func NormalizePath(root, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")

	if root != "" && isAbsAnyOS(p) {
		// Compare in slash form rather than via filepath.Rel, whose behaviour is
		// OS-specific. The daemon may be running on a different OS from the
		// agent that reported the path — and a path is either inside the project
		// root or not, regardless of which OS is asking.
		r := strings.TrimSuffix(strings.ReplaceAll(root, "\\", "/"), "/")
		if rel, ok := trimRootPrefix(p, r); ok {
			p = rel
		}
	}

	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	// darwin and windows have case-insensitive filesystems, so two agents can
	// name the same file differently. Fold case there, but never on Linux.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// isAbsAnyOS reports whether p is absolute under *either* convention.
//
// filepath.IsAbs answers only for the OS it was compiled for, so on Windows it
// calls "/tmp/proj/src/a.go" relative and the root-stripping below is skipped —
// the claim is then stored under a different key than the same file claimed by
// an agent on Linux, and the two agents stop seeing each other's locks.
func isAbsAnyOS(p string) bool {
	if strings.HasPrefix(p, "/") { // unix, and windows paths already slash-converted
		return true
	}
	// Drive-letter form: C:/... or C:
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	return strings.HasPrefix(p, "//") // UNC share
}

// trimRootPrefix removes root from p when p is genuinely inside it, matching
// whole segments so that /tmp/project-two is not treated as living under
// /tmp/project.
func trimRootPrefix(p, root string) (string, bool) {
	if root == "" {
		return p, false
	}
	cmpP, cmpRoot := p, root
	// The filesystems where the case of a path does not distinguish two files
	// are the ones where an agent may report it differently.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		cmpP, cmpRoot = strings.ToLower(p), strings.ToLower(root)
	}
	if cmpP == cmpRoot {
		return "", true
	}
	if strings.HasPrefix(cmpP, cmpRoot+"/") {
		return p[len(root)+1:], true
	}
	return p, false
}

// claimUpsert is the heart of succubus.
//
// Exactly one row exists per (project_id, path). The WHERE clause on the
// conflict branch decides whether the incoming agent may take over:
//   - the previous holder released it, or
//   - the previous lease expired (self-healing: no reaper required), or
//   - the previous holder is dead or unknown, or
//   - the requester already holds it (renewal is idempotent).
//
// The dead-holder clause matters as much as the expiry one. Claims made shortly
// before an agent dies outlive the sweep that marked it dead — the sweep only
// releases claims at the moment of transition — so without this a crashed agent
// would block live agents for the rest of its lease.
//
// Grant is decided by RowsAffected()==1, NOT by the absence of an error: a
// conflicting insert updates zero rows and commits happily.
const claimUpsert = `
INSERT INTO file_claims
  (project_id, path, agent_id, agent_name, task_id, mode, claimed_at, expires_at, released_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
ON CONFLICT(project_id, path) DO UPDATE SET
  agent_id    = excluded.agent_id,
  agent_name  = excluded.agent_name,
  task_id     = excluded.task_id,
  mode        = excluded.mode,
  claimed_at  = excluded.claimed_at,
  expires_at  = excluded.expires_at,
  released_at = NULL
WHERE file_claims.released_at IS NOT NULL
   OR file_claims.expires_at <= excluded.claimed_at
   OR file_claims.agent_id   = excluded.agent_id
   OR NOT EXISTS (
        SELECT 1 FROM agents a
        WHERE a.id = file_claims.agent_id AND a.status != 'dead'
      )`

// ClaimFiles attempts to lease every path for the agent. Paths are sorted and
// claimed inside one transaction so that two agents racing for an overlapping
// set can never deadlock by grabbing them in opposite orders.
//
// Semantics are all-or-nothing: if any path is held by someone else, nothing is
// claimed and the conflicting holders are reported.
func (s *Store) ClaimFiles(projectID, root, agentID, agentName, taskID, mode string, paths []string, ttlSec int64) ([]ClaimResult, error) {
	if mode == "" {
		mode = "write"
	}
	if ttlSec <= 0 {
		ttlSec = DefaultLeaseTTLSec
	}

	norm := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		n := NormalizePath(root, p)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		norm = append(norm, n)
	}
	if len(norm) == 0 {
		return nil, errors.New("no valid paths")
	}
	sort.Strings(norm)

	ts := now()
	exp := ts + ttlSec*1000

	tx, err := s.writeDB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	results := make([]ClaimResult, 0, len(norm))
	conflict := false

	for _, p := range norm {
		res, err := tx.Exec(claimUpsert, projectID, p, agentID, agentName, nz(taskID), mode, ts, exp)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 1 {
			results = append(results, ClaimResult{Path: p, Granted: true, ExpiresAt: exp})
			continue
		}

		conflict = true
		var holder string
		var hexp int64
		err = tx.QueryRow(`SELECT agent_name, expires_at FROM file_claims WHERE project_id=? AND path=?`,
			projectID, p).Scan(&holder, &hexp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		results = append(results, ClaimResult{
			Path: p, Granted: false, Holder: holder, ExpiresAt: hexp,
			Reason: "held by " + holder,
		})
	}

	if conflict {
		// Roll back the partial grants so the caller never half-owns a set.
		tx.Rollback()
		for i := range results {
			if results[i].Granted {
				results[i].Granted = false
				results[i].Reason = "rolled back: another path in this batch is held"
			}
		}
		s.Emit(Event{ProjectID: projectID, Type: EvClaimDenied, AgentID: agentID,
			AgentName: agentName, Payload: map[string]any{"results": results}})
		return results, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.Emit(Event{ProjectID: projectID, Type: EvClaimGranted, AgentID: agentID,
		AgentName: agentName, Payload: map[string]any{"paths": norm, "expires_at": exp}})
	return results, nil
}

// ReleaseFiles frees paths held by this agent. Releasing a path held by someone
// else is a no-op, not an error.
func (s *Store) ReleaseFiles(projectID, root, agentID string, paths []string) (int64, error) {
	var total int64
	for _, p := range paths {
		n := NormalizePath(root, p)
		if n == "" {
			continue
		}
		res, err := s.writeDB.Exec(
			`UPDATE file_claims SET released_at=? WHERE project_id=? AND path=? AND agent_id=? AND released_at IS NULL`,
			now(), projectID, n, agentID)
		if err != nil {
			return total, err
		}
		c, _ := res.RowsAffected()
		total += c
	}
	if total > 0 {
		s.Emit(Event{ProjectID: projectID, Type: EvClaimReleased, AgentID: agentID,
			Payload: map[string]any{"paths": paths}})
	}
	return total, nil
}

// ReleaseAllForAgent frees every path an agent holds. Called on session end and
// when the janitor declares an agent dead.
func (s *Store) ReleaseAllForAgent(projectID, agentID string) (int64, error) {
	res, err := s.writeDB.Exec(
		`UPDATE file_claims SET released_at=? WHERE project_id=? AND agent_id=? AND released_at IS NULL`,
		now(), projectID, agentID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.Emit(Event{ProjectID: projectID, Type: EvClaimReleased, AgentID: agentID,
			Payload: map[string]any{"all": true, "count": n}})
	}
	return n, nil
}

// ReleaseClaimsForTask frees every file claimed in service of one task. Called
// when a task reaches done or cancelled: the work is over, so holding its files
// only blocks other agents.
func (s *Store) ReleaseClaimsForTask(projectID, taskID string) (int64, error) {
	if taskID == "" {
		return 0, nil
	}
	res, err := s.writeDB.Exec(
		`UPDATE file_claims SET released_at=? WHERE project_id=? AND task_id=? AND released_at IS NULL`,
		now(), projectID, taskID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RenewClaims extends the lease on everything the agent currently holds.
func (s *Store) RenewClaims(projectID, agentID string, ttlSec int64) (int64, error) {
	if ttlSec <= 0 {
		ttlSec = DefaultLeaseTTLSec
	}
	ts := now()
	res, err := s.writeDB.Exec(
		`UPDATE file_claims SET expires_at=? WHERE project_id=? AND agent_id=? AND released_at IS NULL AND expires_at>?`,
		ts+ttlSec*1000, projectID, agentID, ts)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// liveClaimPredicate matches claims that should still be honoured: unreleased,
// unexpired, and held by an agent that is not dead. A dead holder's claim is
// invisible everywhere, so the UI shows no phantom locks and hooks do not block
// on a session that has gone away.
const liveClaimPredicate = `released_at IS NULL AND expires_at > ?
  AND EXISTS (SELECT 1 FROM agents a WHERE a.id = file_claims.agent_id AND a.status != 'dead')`

// ActiveClaims lists live (unreleased, unexpired, live-holder) claims.
func (s *Store) ActiveClaims(projectID string) ([]Claim, error) {
	rows, err := s.readDB.Query(`
		SELECT project_id, path, agent_id, agent_name, COALESCE(task_id,''), mode, claimed_at, expires_at
		FROM file_claims
		WHERE project_id=? AND `+liveClaimPredicate+`
		ORDER BY claimed_at DESC`, projectID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Claim{}
	for rows.Next() {
		var c Claim
		if err := rows.Scan(&c.ProjectID, &c.Path, &c.AgentID, &c.AgentName,
			&c.TaskID, &c.Mode, &c.ClaimedAt, &c.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CheckFiles is the hook fast-path: does any path conflict with a live claim
// held by a *different* agent? excludeAgentID may be empty.
func (s *Store) CheckFiles(projectID, root, excludeAgentID string, paths []string) ([]ClaimResult, error) {
	ts := now()
	out := make([]ClaimResult, 0, len(paths))
	for _, p := range paths {
		n := NormalizePath(root, p)
		if n == "" {
			continue
		}
		var holderID, holder string
		var exp int64
		err := s.readDB.QueryRow(`
			SELECT agent_id, agent_name, expires_at FROM file_claims
			WHERE project_id=? AND path=? AND `+liveClaimPredicate,
			projectID, n, ts).Scan(&holderID, &holder, &exp)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			out = append(out, ClaimResult{Path: n, Granted: true})
		case err != nil:
			return nil, err
		case holderID == excludeAgentID:
			out = append(out, ClaimResult{Path: n, Granted: true, Holder: holder, ExpiresAt: exp,
				Reason: "held by you"})
		default:
			out = append(out, ClaimResult{Path: n, Granted: false, Holder: holder, ExpiresAt: exp,
				Reason: "held by " + holder})
		}
	}
	return out, nil
}

// ExpireClaims marks lapsed leases released so the UI can show them ending.
// Correctness does not depend on this running — claimUpsert already steals
// expired leases — it exists purely for observability.
func (s *Store) ExpireClaims() (int64, error) {
	ts := now()
	rows, err := s.writeDB.Query(
		`SELECT project_id, path, agent_name FROM file_claims WHERE released_at IS NULL AND expires_at<=?`, ts)
	if err != nil {
		return 0, err
	}
	type exp struct{ project, path, agent string }
	var lapsed []exp
	for rows.Next() {
		var e exp
		if err := rows.Scan(&e.project, &e.path, &e.agent); err != nil {
			rows.Close()
			return 0, err
		}
		lapsed = append(lapsed, e)
	}
	rows.Close()
	if len(lapsed) == 0 {
		return 0, nil
	}
	if _, err := s.writeDB.Exec(
		`UPDATE file_claims SET released_at=? WHERE released_at IS NULL AND expires_at<=?`, ts, ts); err != nil {
		return 0, err
	}
	for _, e := range lapsed {
		s.Emit(Event{ProjectID: e.project, Type: EvClaimExpired, AgentName: e.agent,
			Payload: map[string]any{"path": e.path}})
	}
	return int64(len(lapsed)), nil
}
