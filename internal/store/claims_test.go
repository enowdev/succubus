package store

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.UpsertProject("p1", "proj", "/tmp/proj", ""); err != nil {
		t.Fatalf("project: %v", err)
	}
	return s
}

// liveAgent registers a real agent row. Claims are only honoured while their
// holder is alive, so tests that assert conflicts must use registered agents
// rather than synthetic ids.
func liveAgent(t *testing.T, s *Store, name string) *Agent {
	t.Helper()
	a, _, err := s.RegisterAgent("p1", name, "test", "sess-"+name, "/tmp", 0)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return a
}

func claim(t *testing.T, s *Store, agent, path string, ttl int64) bool {
	t.Helper()
	res, err := s.ClaimFiles("p1", "", agent, agent, "", "write", []string{path}, ttl)
	if err != nil {
		t.Fatalf("claim %s: %v", agent, err)
	}
	return len(res) == 1 && res[0].Granted
}

// TestClaimRace is the regression test for the lease deadlock. Many goroutines
// fight for one path; exactly one must win.
func TestClaimRace(t *testing.T) {
	s := testStore(t)
	const n = 32

	// Real agent rows: a claim is only defensible while its holder is alive.
	ids := make([]string, n)
	for i := range n {
		ids[i] = liveAgent(t, s, "R"+itoa(i)).ID
	}

	var wg sync.WaitGroup
	granted := make([]bool, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := s.ClaimFiles("p1", "", ids[i], "AGENT"+itoa(i), "", "write",
				[]string{"src/main.go"}, 60)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			granted[i] = res[0].Granted
		}()
	}
	close(start)
	wg.Wait()

	count := 0
	for _, g := range granted {
		if g {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 grant among %d racers, got %d", n, count)
	}
}

// TestExpiredClaimIsStealable is the specific bug that broke the naive schema:
// an expired-but-unreleased claim must not lock the path forever.
func TestExpiredClaimIsStealable(t *testing.T) {
	s := testStore(t)
	old := liveAgent(t, s, "OLD")
	fresh := liveAgent(t, s, "NEW")

	if !claim(t, s, old.ID, "a.go", 1) {
		t.Fatal("initial claim should be granted")
	}
	// Force expiry without releasing — the agent is still alive, so only the
	// expiry rule can unblock this path.
	if _, err := s.writeDB.Exec(
		`UPDATE file_claims SET expires_at=? WHERE path='a.go'`, now()-1000); err != nil {
		t.Fatal(err)
	}
	if !claim(t, s, fresh.ID, "a.go", 60) {
		t.Fatal("expired lease must be stealable — this is the deadlock regression")
	}
	claims, err := s.ActiveClaims("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].AgentID != fresh.ID {
		t.Fatalf("expected holder %s, got %+v", fresh.Name, claims)
	}
}

// TestDeadAgentClaimIsStealable covers the second deadlock shape: a claim made
// shortly before its agent died outlives the sweep that marked the agent dead,
// because the sweep only releases claims at the moment of transition.
func TestDeadAgentClaimIsStealable(t *testing.T) {
	s := testStore(t)

	ghost, _, err := s.RegisterAgent("p1", "GHOST", "claude-code", "sess-ghost", "/tmp", 1)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := s.RegisterAgent("p1", "LIVE", "opencode", "sess-live", "/tmp", 2)
	if err != nil {
		t.Fatal(err)
	}

	// GHOST takes a long lease, then dies without releasing.
	res, err := s.ClaimFiles("p1", "", ghost.ID, ghost.Name, "", "write", []string{"z.go"}, 3600)
	if err != nil || !res[0].Granted {
		t.Fatalf("ghost claim should be granted: %v %+v", err, res)
	}
	if _, err := s.writeDB.Exec(`UPDATE agents SET status='dead' WHERE id=?`, ghost.ID); err != nil {
		t.Fatal(err)
	}

	// The lease is nowhere near expiry, so only the dead-holder rule can help.
	res, err = s.ClaimFiles("p1", "", live.ID, live.Name, "", "write", []string{"z.go"}, 600)
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Granted {
		t.Fatalf("a dead agent's unexpired claim must be stealable, got %+v", res[0])
	}

	// And a dead holder's claim must not show up as an active lock anywhere.
	if _, err := s.writeDB.Exec(`UPDATE agents SET status='dead' WHERE id=?`, live.ID); err != nil {
		t.Fatal(err)
	}
	claims, err := s.ActiveClaims("p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("dead holders must not appear in ActiveClaims, got %+v", claims)
	}
	checks, err := s.CheckFiles("p1", "", "", []string{"z.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !checks[0].Granted {
		t.Fatalf("CheckFiles must report a dead holder's path as free, got %+v", checks[0])
	}
}

func TestClaimConflictAndRenew(t *testing.T) {
	s := testStore(t)
	a := liveAgent(t, s, "AA")
	b := liveAgent(t, s, "BB")

	if !claim(t, s, a.ID, "x.go", 60) {
		t.Fatal("first claim should win")
	}
	if claim(t, s, b.ID, "x.go", 60) {
		t.Fatal("second agent must be denied")
	}
	// Re-claiming your own path is a renewal, not a conflict.
	if !claim(t, s, a.ID, "x.go", 60) {
		t.Fatal("self re-claim should be granted (idempotent renew)")
	}
}

func TestReleaseThenClaim(t *testing.T) {
	s := testStore(t)
	a := liveAgent(t, s, "AA")
	b := liveAgent(t, s, "BB")

	claim(t, s, a.ID, "y.go", 60)
	if _, err := s.ReleaseFiles("p1", "", a.ID, []string{"y.go"}); err != nil {
		t.Fatal(err)
	}
	if !claim(t, s, b.ID, "y.go", 60) {
		t.Fatal("released path must be claimable by another agent")
	}
}

// TestMultiPathAllOrNothing: if one path in a batch conflicts, none are taken.
func TestMultiPathAllOrNothing(t *testing.T) {
	s := testStore(t)
	a := liveAgent(t, s, "AA")
	b := liveAgent(t, s, "BB")
	c := liveAgent(t, s, "CC")

	claim(t, s, a.ID, "shared.go", 60)

	res, err := s.ClaimFiles("p1", "", b.ID, b.Name, "", "write",
		[]string{"free1.go", "shared.go", "free2.go"}, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Granted {
			t.Fatalf("no path should be granted when the batch conflicts: %+v", r)
		}
	}
	// The uncontested paths must still be free for someone else afterwards.
	if !claim(t, s, c.ID, "free1.go", 60) {
		t.Fatal("free1.go must remain claimable after the rolled-back batch")
	}
}

// TestClaimOrderingNoDeadlock: two agents grabbing overlapping sets in opposite
// orders must not deadlock (paths are sorted internally).
func TestClaimOrderingNoDeadlock(t *testing.T) {
	s := testStore(t)
	a := liveAgent(t, s, "AA")
	b := liveAgent(t, s, "BB")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 20 {
			s.ClaimFiles("p1", "", a.ID, a.Name, "", "write", []string{"1.go", "2.go", "3.go"}, 5)
			s.ReleaseAllForAgent("p1", a.ID)
		}
	}()
	for range 20 {
		s.ClaimFiles("p1", "", b.ID, b.Name, "", "write", []string{"3.go", "2.go", "1.go"}, 5)
		s.ReleaseAllForAgent("p1", b.ID)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: overlapping claim batches in opposite order")
	}
}

func TestNormalizePath(t *testing.T) {
	root := "/tmp/proj"
	cases := map[string]string{
		"./src/main.go":      "src/main.go",
		"src\\main.go":       "src/main.go",
		"/tmp/proj/src/a.go": "src/a.go",
		"  src/b.go  ":       "src/b.go",
		"/src/c.go/":         "src/c.go",
	}
	for in, want := range cases {
		if got := NormalizePath(root, in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizePathIsOSIndependent is a regression test for a bug CI found on
// Windows and no amount of local testing on macOS would have.
//
// The old implementation used filepath.IsAbs, which answers only for the OS it
// was compiled for. On Windows it called "/tmp/proj/src/a.go" *relative*, so the
// project root was never stripped and the claim was stored under a different key
// than the one an agent on Linux would produce for the same file — two agents
// editing one file, each believing they held it.
//
// Normalization must depend on the path, not on which OS the daemon runs on.
func TestNormalizePathIsOSIndependent(t *testing.T) {
	cases := []struct{ root, in, want string }{
		// Unix-shaped root and path, which must normalize identically whether
		// the daemon runs on Linux, macOS, or Windows.
		{"/tmp/proj", "/tmp/proj/src/a.go", "src/a.go"},
		{"/tmp/proj", "/tmp/proj/deep/nested/b.go", "deep/nested/b.go"},
		{"/tmp/proj/", "/tmp/proj/src/a.go", "src/a.go"}, // trailing slash on root

		// Windows-shaped, which likewise must not depend on the host OS.
		{`C:\work\proj`, `C:\work\proj\src\a.go`, "src/a.go"},
		{"C:/work/proj", "C:/work/proj/src/a.go", "src/a.go"},

		// Outside the root: left alone rather than mangled into a false match.
		{"/tmp/proj", "/etc/passwd", "etc/passwd"},

		// The root itself.
		{"/tmp/proj", "/tmp/proj", ""},
	}

	for _, c := range cases {
		want := c.want
		// The case-insensitive filesystems fold; Linux does not.
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			want = strings.ToLower(want)
		}
		if got := NormalizePath(c.root, c.in); got != want {
			t.Errorf("NormalizePath(%q, %q) = %q, want %q", c.root, c.in, got, want)
		}
	}
}

// TestNormalizePathRespectsSegmentBoundaries: /tmp/project-two is not inside
// /tmp/project, and a plain string prefix check would say otherwise — silently
// filing one project's claims under another.
func TestNormalizePathRespectsSegmentBoundaries(t *testing.T) {
	got := NormalizePath("/tmp/project", "/tmp/project-two/src/a.go")
	if got == "src/a.go" {
		t.Fatal("/tmp/project-two was treated as living inside /tmp/project")
	}
	if !strings.Contains(got, "project-two") {
		t.Errorf("the path outside the root was mangled: got %q", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
