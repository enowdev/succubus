package store

import "testing"

// A project's id is derived from its git remote when one exists, and from its
// path when it does not. So `git remote add origin` — pushing a local repo to
// GitHub for the first time — changes the id of a project that already has a
// plan, tasks, decisions and history.
//
// Before this was handled, that history was silently orphaned: the dashboard
// showed an empty project next to a full one it no longer linked to, and no
// error appeared anywhere. These tests pin the adoption, and just as importantly
// pin the cases where it must NOT happen — silently merging two projects that
// were meant to stay separate is worse than the orphaning it fixes.

// seedProject fills a project with one of everything that must survive.
func seedProject(t *testing.T, s *Store, pid string) {
	t.Helper()

	a, _, err := s.RegisterAgent(pid, "ORION", "claude-code", "sess-1", "/tmp/x", 0)
	if err != nil {
		t.Fatalf("register agent: %v", err)
	}
	if _, err := s.CreatePlan(pid, "Ship it", "the plan body", "active", a.ID); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := s.CreateTask(pid, "", "a task", "", "todo", 2, "", "", nil); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := s.ClaimFiles(pid, "", a.ID, a.Name, "", "write", []string{"src/a.go"}, 300); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.CreateDecision(pid, "decision", "we chose X", "because Y", a.ID, a.Name, ""); err != nil {
		t.Fatalf("decision: %v", err)
	}
}

// counts reports how much history a project id owns.
func counts(t *testing.T, s *Store, pid string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range projectChildTables {
		var n int
		if err := s.readDB.QueryRow(
			`SELECT COUNT(*) FROM `+table+` WHERE project_id = ?`, pid).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// TestAddingAGitRemoteKeepsTheHistory is the bug: push a local project to
// GitHub, and everything recorded before the push must still be there.
func TestAddingAGitRemoteKeepsTheHistory(t *testing.T) {
	s := testStore(t)
	const root = "/tmp/myproject"

	// Before the push: no remote, so the id is derived from the path.
	oldID := "path0000hash"
	if _, err := s.UpsertProject(oldID, "myproject", root, ""); err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, oldID)

	before := counts(t, s, oldID)
	if total(before) == 0 {
		t.Fatal("the fixture recorded nothing, so this test proves nothing")
	}

	// `git remote add origin` — same directory, new id.
	newID := "remote00hash"
	p, err := s.UpsertProject(newID, "myproject", root, "https://github.com/me/myproject.git")
	if err != nil {
		t.Fatal(err)
	}

	after := counts(t, s, newID)
	for table, want := range before {
		if after[table] != want {
			t.Errorf("%s: %d rows before the remote was added, %d after — history was orphaned",
				table, want, after[table])
		}
	}
	if p.GitRemote == "" {
		t.Error("the adopted project did not record the remote it was adopted onto")
	}

	// And the old id must be gone, not left behind as an empty duplicate — a
	// second project on the same path is exactly what the user sees as the bug.
	projects, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	var onThisPath []Project
	for _, p := range projects {
		if p.RootPath == root {
			onThisPath = append(onThisPath, p)
		}
	}
	if len(onThisPath) != 1 {
		for _, p := range onThisPath {
			t.Logf("  %s  %s  remote=%q", p.ID, p.RootPath, p.GitRemote)
		}
		t.Fatalf("expected one project at %s after adoption, found %d — "+
			"a duplicate on the same path is exactly what the user reports as the bug",
			root, len(onThisPath))
	}
	if onThisPath[0].ID != newID {
		t.Errorf("project id = %s, want %s", onThisPath[0].ID, newID)
	}
}

// TestAdoptionDoesNotMergeSeparateProjects: two real projects that happen to be
// resolvable must stay apart. Over-eager adoption would silently merge two
// people's work, which is worse than the problem it solves.
func TestAdoptionDoesNotMergeSeparateProjects(t *testing.T) {
	t.Run("different root paths", func(t *testing.T) {
		s := testStore(t)
		if _, err := s.UpsertProject("aaa", "one", "/tmp/one", ""); err != nil {
			t.Fatal(err)
		}
		seedProject(t, s, "aaa")

		// A different directory entirely, which merely gained a remote.
		if _, err := s.UpsertProject("bbb", "two", "/tmp/two", "https://github.com/me/two.git"); err != nil {
			t.Fatal(err)
		}
		if n := total(counts(t, s, "bbb")); n != 0 {
			t.Errorf("a project at a different path adopted %d rows that were not its own", n)
		}
		if n := total(counts(t, s, "aaa")); n == 0 {
			t.Error("the untouched project lost its history")
		}
	})

	t.Run("the old project already had a remote", func(t *testing.T) {
		s := testStore(t)
		// Already remote-derived: an id change here means something else is
		// going on, and guessing would be wrong.
		if _, err := s.UpsertProject("aaa", "one", "/tmp/one", "https://github.com/me/old.git"); err != nil {
			t.Fatal(err)
		}
		seedProject(t, s, "aaa")

		if _, err := s.UpsertProject("bbb", "one", "/tmp/one", "https://github.com/me/new.git"); err != nil {
			t.Fatal(err)
		}
		if n := total(counts(t, s, "bbb")); n != 0 {
			t.Errorf("adopted %d rows from a project that already had its own remote", n)
		}
	})

	t.Run("the new id already has history", func(t *testing.T) {
		s := testStore(t)
		if _, err := s.UpsertProject("aaa", "one", "/tmp/one", ""); err != nil {
			t.Fatal(err)
		}
		seedProject(t, s, "aaa")

		// The remote-derived project is already established with its own data.
		if _, err := s.UpsertProject("bbb", "one", "/tmp/one", "https://github.com/me/one.git"); err != nil {
			t.Fatal(err)
		}
		seedProject(t, s, "bbb")
		bbbBefore := total(counts(t, s, "bbb"))

		// Re-resolving must not now fold the old one in on top of it.
		if _, err := s.UpsertProject("bbb", "one", "/tmp/one", "https://github.com/me/one.git"); err != nil {
			t.Fatal(err)
		}
		if got := total(counts(t, s, "bbb")); got != bbbBefore {
			t.Errorf("an established project absorbed another: %d rows became %d", bbbBefore, got)
		}
	})
}

// TestAdoptionIsIdempotent: hooks call resolve on every session start, so this
// path runs constantly. Running it again must change nothing.
func TestAdoptionIsIdempotent(t *testing.T) {
	s := testStore(t)
	const root = "/tmp/myproject"
	const remote = "https://github.com/me/myproject.git"

	if _, err := s.UpsertProject("oldid", "myproject", root, ""); err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "oldid")

	if _, err := s.UpsertProject("newid", "myproject", root, remote); err != nil {
		t.Fatal(err)
	}
	first := counts(t, s, "newid")

	for range 3 {
		if _, err := s.UpsertProject("newid", "myproject", root, remote); err != nil {
			t.Fatal(err)
		}
	}
	second := counts(t, s, "newid")

	for table := range first {
		if first[table] != second[table] {
			t.Errorf("%s changed on re-resolve: %d then %d", table, first[table], second[table])
		}
	}
}
