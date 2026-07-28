package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/succubus/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.UpsertProject("p1", "proj", "/tmp/proj", ""); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return New(st, false), st
}

// do issues a request and returns the recorder. body may be nil.
func do(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func decodeInto(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
}

func TestHealth(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(t, s, "GET", "/api/health", nil)
	if w.Code != 200 {
		t.Fatalf("health: got %d", w.Code)
	}
}

// TestRegisterIsIdempotentPerSession is the property the whole identity model
// rests on: the same session key always gets the same name back.
func TestRegisterIsIdempotentPerSession(t *testing.T) {
	s, _ := newTestServer(t)

	reg := func(sessionKey string) (id, name string, reused bool) {
		w := do(t, s, "POST", "/api/projects/p1/agents/register", map[string]any{
			"tool": "claude-code", "session_key": sessionKey, "cwd": "/tmp/proj",
		})
		if w.Code != 200 {
			t.Fatalf("register: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Agent  store.Agent `json:"agent"`
			Reused bool        `json:"reused"`
		}
		decodeInto(t, w, &out)
		return out.Agent.ID, out.Agent.Name, out.Reused
	}

	id1, name1, reused1 := reg("sess-a")
	if reused1 {
		t.Fatal("first registration should not be a reuse")
	}
	id2, name2, reused2 := reg("sess-a")
	if !reused2 || id1 != id2 || name1 != name2 {
		t.Fatalf("resuming a session must return the same identity: %s/%s vs %s/%s (reused=%v)",
			id1, name1, id2, name2, reused2)
	}

	// A different session in the same project gets a different name.
	_, name3, _ := reg("sess-b")
	if name3 == name1 {
		t.Fatalf("two concurrent sessions must not share the name %q", name1)
	}
}

// TestClaimConflictOverHTTP: the lease rules must hold through the API, not
// just in the store.
func TestClaimConflictOverHTTP(t *testing.T) {
	s, _ := newTestServer(t)

	register := func(key string) store.Agent {
		w := do(t, s, "POST", "/api/projects/p1/agents/register", map[string]any{
			"tool": "test", "session_key": key,
		})
		var out struct {
			Agent store.Agent `json:"agent"`
		}
		decodeInto(t, w, &out)
		return out.Agent
	}
	a, b := register("a"), register("b")

	claim := func(ag store.Agent) (granted bool, holder string) {
		w := do(t, s, "POST", "/api/projects/p1/claims", map[string]any{
			"agent_id": ag.ID, "agent_name": ag.Name,
			"paths": []string{"src/main.go"}, "ttl_sec": 600,
		})
		if w.Code != 200 {
			t.Fatalf("claim: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Granted bool                `json:"granted"`
			Results []store.ClaimResult `json:"results"`
		}
		decodeInto(t, w, &out)
		if len(out.Results) > 0 {
			holder = out.Results[0].Holder
		}
		return out.Granted, holder
	}

	if ok, _ := claim(a); !ok {
		t.Fatal("first claim should be granted")
	}
	ok, holder := claim(b)
	if ok {
		t.Fatal("second agent must be denied")
	}
	if holder != a.Name {
		t.Fatalf("denial should name the holder %q, got %q", a.Name, holder)
	}

	// check reports the same conflict without taking a lock.
	w := do(t, s, "POST", "/api/projects/p1/claims/check", map[string]any{
		"agent_id": b.ID, "paths": []string{"src/main.go"},
	})
	var chk struct {
		Conflict bool   `json:"conflict"`
		Holder   string `json:"holder"`
	}
	decodeInto(t, w, &chk)
	if !chk.Conflict || chk.Holder != a.Name {
		t.Fatalf("check should report a conflict held by %s, got %+v", a.Name, chk)
	}
}

// TestFinishingTaskReleasesItsFiles guards the rule that a completed task
// stops holding files.
func TestFinishingTaskReleasesItsFiles(t *testing.T) {
	s, _ := newTestServer(t)

	w := do(t, s, "POST", "/api/projects/p1/agents/register", map[string]any{
		"tool": "test", "session_key": "a",
	})
	var reg struct {
		Agent store.Agent `json:"agent"`
	}
	decodeInto(t, w, &reg)

	w = do(t, s, "POST", "/api/projects/p1/tasks", map[string]any{"title": "do it"})
	var task store.Task
	decodeInto(t, w, &task)

	do(t, s, "POST", "/api/projects/p1/claims", map[string]any{
		"agent_id": reg.Agent.ID, "agent_name": reg.Agent.Name,
		"task_id": task.ID, "paths": []string{"a.go", "b.go"}, "ttl_sec": 600,
	})

	w = do(t, s, "GET", "/api/projects/p1/claims", nil)
	var claims []store.Claim
	decodeInto(t, w, &claims)
	if len(claims) != 2 {
		t.Fatalf("expected 2 claims before finishing, got %d", len(claims))
	}

	if w := do(t, s, "PATCH", "/api/tasks/"+task.ID, map[string]any{"status": "done"}); w.Code != 200 {
		t.Fatalf("update task: %d %s", w.Code, w.Body.String())
	}

	w = do(t, s, "GET", "/api/projects/p1/claims", nil)
	decodeInto(t, w, &claims)
	if len(claims) != 0 {
		t.Fatalf("finishing a task must release its files, %d still held", len(claims))
	}
}

// TestTaskDependencyCycleRejected: a cycle would make the board unsatisfiable.
func TestTaskDependencyCycleRejected(t *testing.T) {
	s, _ := newTestServer(t)

	mk := func(title string) store.Task {
		w := do(t, s, "POST", "/api/projects/p1/tasks", map[string]any{"title": title})
		var task store.Task
		decodeInto(t, w, &task)
		return task
	}
	a, b := mk("a"), mk("b")

	if w := do(t, s, "POST", "/api/tasks/"+b.ID+"/deps",
		map[string]any{"depends_on": a.ID}); w.Code != 200 {
		t.Fatalf("first dep should be accepted: %d", w.Code)
	}
	// a -> b would close the loop.
	if w := do(t, s, "POST", "/api/tasks/"+a.ID+"/deps",
		map[string]any{"depends_on": b.ID}); w.Code == 200 {
		t.Fatal("a dependency cycle must be rejected")
	}
}

// TestContextTellsAgentsWhatToWrite is the regression for one-way coordination:
// injection has to prompt agents to record work, not only to read it.
func TestContextTellsAgentsWhatToWrite(t *testing.T) {
	s, _ := newTestServer(t)

	w := do(t, s, "POST", "/api/projects/p1/agents/register", map[string]any{
		"tool": "test", "session_key": "a",
	})
	var reg struct {
		Agent store.Agent `json:"agent"`
	}
	decodeInto(t, w, &reg)

	w = do(t, s, "GET", "/api/projects/p1/context?agent_id="+reg.Agent.ID, nil)
	var ctx ContextPayload
	decodeInto(t, w, &ctx)

	for _, want := range []string{
		reg.Agent.Name,         // it must know who it is
		"succubus_plan_create", // and be told to plan
		"succubus_task_create", // and to record tasks
		"succubus_claim_files", // and to claim before editing
		"succubus_ask",         // and to ask rather than guess
	} {
		if !strings.Contains(ctx.Text, want) {
			t.Errorf("injected context is missing %q:\n%s", want, ctx.Text)
		}
	}

	// With no plan yet, it should say so rather than stay silent.
	if !strings.Contains(ctx.Text, "NO ACTIVE PLAN") {
		t.Errorf("empty project should be told there is no plan:\n%s", ctx.Text)
	}
}

func TestDeleteProjectClearsEverything(t *testing.T) {
	s, st := newTestServer(t)

	do(t, s, "POST", "/api/projects/p1/agents/register", map[string]any{
		"tool": "test", "session_key": "a",
	})
	do(t, s, "POST", "/api/projects/p1/tasks", map[string]any{"title": "t"})
	do(t, s, "POST", "/api/projects/p1/room", map[string]any{
		"author_name": "HUMAN", "body_md": "hello",
	})

	if w := do(t, s, "DELETE", "/api/projects/p1", nil); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := st.GetProject("p1"); err == nil {
		t.Fatal("project should be gone")
	}
	// Cascades: nothing may survive pointing at a project that no longer exists.
	if tasks, _ := st.ListTasks("p1", store.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("tasks survived deletion: %d", len(tasks))
	}
	if agents, _ := st.ListAgents("p1", true); len(agents) != 0 {
		t.Fatalf("agents survived deletion: %d", len(agents))
	}
	if msgs, _ := st.RoomMessages("p1", 10); len(msgs) != 0 {
		t.Fatalf("room messages survived deletion: %d", len(msgs))
	}

	if w := do(t, s, "DELETE", "/api/projects/p1", nil); w.Code != 404 {
		t.Fatalf("deleting a missing project should 404, got %d", w.Code)
	}
}

func TestDocsEndpoints(t *testing.T) {
	s, _ := newTestServer(t)

	w := do(t, s, "GET", "/api/docs", nil)
	var sections []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	decodeInto(t, w, &sections)
	if len(sections) == 0 {
		t.Fatal("expected some documentation sections")
	}

	w = do(t, s, "GET", "/api/docs/"+sections[0].ID, nil)
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("docs section: %d, %d bytes", w.Code, w.Body.Len())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("docs should be served as markdown, got %q", ct)
	}

	// Path traversal must not escape the embedded directory.
	for _, bad := range []string{"../go.mod", "..%2Fgo.mod", "nope"} {
		if w := do(t, s, "GET", "/api/docs/"+bad, nil); w.Code != 404 {
			t.Errorf("GET /api/docs/%s should 404, got %d", bad, w.Code)
		}
	}
}

func TestUnknownIDsReturn404(t *testing.T) {
	s, _ := newTestServer(t)
	for _, path := range []string{
		"/api/projects/nope",
		"/api/tasks/nope",
		"/api/plans/nope",
	} {
		if w := do(t, s, "GET", path, nil); w.Code != 404 {
			t.Errorf("GET %s: expected 404, got %d", path, w.Code)
		}
	}
}
