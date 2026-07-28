// Package httpapi exposes the succubus daemon over HTTP: a JSON API for
// agents and the CLI, an SSE stream for the dashboard, and the embedded SPA.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/enowdev/succubus/internal/identity"
	"github.com/enowdev/succubus/internal/store"
)

type Server struct {
	St  *store.Store
	Mux *http.ServeMux
	// Dev enables permissive CORS so the Vite dev server can call the daemon.
	Dev bool
}

func New(st *store.Store, dev bool) *Server {
	s := &Server{St: st, Mux: http.NewServeMux(), Dev: dev}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// ServeMux normalises "/api/docs/../go.mod" and answers with a 307 to
	// "/api/go.mod". That leaks nothing, but a traversal attempt deserves a
	// flat refusal rather than a redirect that looks like it went somewhere.
	if strings.Contains(r.URL.Path, "..") {
		http.NotFound(w, r)
		return
	}

	if s.Dev {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Last-Event-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	if err := s.checkCrossOriginWrite(r); err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}

	s.Mux.ServeHTTP(w, r)
}

// checkCrossOriginWrite refuses writes driven by a web page on another origin.
//
// Binding to loopback is not the protection it appears to be. Any page the user
// visits can POST to 127.0.0.1, and a POST with a "simple" content type
// (text/plain, form-encoded) is sent *without* a CORS preflight — so the browser
// never asks permission, and the write lands. The attacker cannot read the
// reply, which makes this easy to miss, but succubus writes are worth making
// blind: a forged room message is injected into agent context by the hooks, so
// this is a path from any visited web page into what the user's coding agents
// are told to do.
//
// The rule: a request carrying an Origin that is not ours cannot write. Hooks,
// the CLI, MCP and curl send no Origin at all and are unaffected — this costs
// nothing for the clients that matter and closes the browser path entirely.
func (s *Server) checkCrossOriginWrite(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil // safe methods
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// Not a browser-initiated request. Hooks, CLI, MCP, curl.
		return nil
	}
	if sameOrigin(origin, r.Host) {
		return nil
	}
	// The Vite dev server runs on its own port, so it is legitimately a
	// different origin. Trust it only under --dev, and only when it is on
	// loopback — a dev flag should widen what the developer's own machine can
	// do, not what the whole internet can.
	if s.Dev && isLoopbackOrigin(origin) {
		return nil
	}
	return fmt.Errorf("cross-origin write from %s refused", origin)
}

// isLoopbackOrigin reports whether an Origin names this machine on any port.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	h, _ := splitHostPort(u.Host)
	return isLoopbackName(h)
}

// sameOrigin reports whether an Origin header names this daemon.
//
// Compared by host:port. The scheme is not checked because the daemon serves
// plain HTTP on loopback, so a page served by the daemon itself always arrives
// as http://<host>.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	// 127.0.0.1 and localhost are the same daemon, and the dashboard may be
	// reached by either name.
	oh, op := splitHostPort(u.Host)
	hh, hp := splitHostPort(host)
	return op == hp && isLoopbackName(oh) && isLoopbackName(hh)
}

func splitHostPort(hp string) (host, port string) {
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return h, p
	}
	return hp, ""
}

func isLoopbackName(h string) bool {
	h = strings.Trim(strings.ToLower(h), "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) routes() {
	m := s.Mux

	m.HandleFunc("GET /api/health", s.health)
	m.HandleFunc("POST /api/projects/resolve", s.resolveProject)
	m.HandleFunc("GET /api/projects", s.listProjects)
	m.HandleFunc("GET /api/projects/{pid}", s.getProject)
	m.HandleFunc("DELETE /api/projects/{pid}", s.deleteProject)
	// Overview across every project at once, for the sidebar tree.
	m.HandleFunc("GET /api/overview", s.overview)

	// Documentation, served from the repository's own markdown files.
	m.HandleFunc("GET /api/docs", s.docsList)
	m.HandleFunc("GET /api/docs/{id}", s.docsGet)

	m.HandleFunc("POST /api/projects/{pid}/agents/register", s.registerAgent)
	m.HandleFunc("GET /api/projects/{pid}/agents", s.listAgents)
	m.HandleFunc("POST /api/agents/{id}/heartbeat", s.heartbeat)
	m.HandleFunc("POST /api/agents/{id}/rename", s.renameAgent)
	m.HandleFunc("DELETE /api/agents/{id}", s.unregisterAgent)

	m.HandleFunc("GET /api/projects/{pid}/plans", s.listPlans)
	m.HandleFunc("POST /api/projects/{pid}/plans", s.createPlan)
	m.HandleFunc("GET /api/plans/{id}", s.getPlan)
	m.HandleFunc("PATCH /api/plans/{id}", s.updatePlan)
	m.HandleFunc("DELETE /api/plans/{id}", s.deletePlan)

	m.HandleFunc("GET /api/projects/{pid}/tasks", s.listTasks)
	m.HandleFunc("POST /api/projects/{pid}/tasks", s.createTask)
	m.HandleFunc("GET /api/tasks/{id}", s.getTask)
	m.HandleFunc("PATCH /api/tasks/{id}", s.updateTask)
	m.HandleFunc("DELETE /api/tasks/{id}", s.deleteTask)
	m.HandleFunc("POST /api/tasks/{id}/deps", s.addDep)
	m.HandleFunc("DELETE /api/tasks/{id}/deps/{depID}", s.removeDep)
	m.HandleFunc("POST /api/tasks/{id}/claim", s.claimTask)
	m.HandleFunc("POST /api/tasks/{id}/reorder", s.reorderTask)

	m.HandleFunc("GET /api/projects/{pid}/claims", s.listClaims)
	m.HandleFunc("POST /api/projects/{pid}/claims", s.claimFiles)
	m.HandleFunc("POST /api/projects/{pid}/claims/release", s.releaseFiles)
	m.HandleFunc("POST /api/projects/{pid}/claims/renew", s.renewClaims)
	m.HandleFunc("POST /api/projects/{pid}/claims/check", s.checkFiles)

	m.HandleFunc("GET /api/projects/{pid}/room", s.roomList)
	m.HandleFunc("POST /api/projects/{pid}/room", s.roomPost)
	m.HandleFunc("POST /api/projects/{pid}/room/read", s.roomMarkRead)
	m.HandleFunc("POST /api/room/{id}/resolve", s.roomResolve)
	m.HandleFunc("DELETE /api/room/{id}", s.roomDelete)

	m.HandleFunc("GET /api/projects/{pid}/decisions", s.listDecisions)
	m.HandleFunc("POST /api/projects/{pid}/decisions", s.createDecision)
	m.HandleFunc("POST /api/decisions/{id}/ack", s.ackDecision)
	m.HandleFunc("DELETE /api/decisions/{id}", s.deleteDecision)

	m.HandleFunc("GET /api/projects/{pid}/events", s.listEvents)
	m.HandleFunc("GET /api/projects/{pid}/context", s.getContext)
	m.HandleFunc("GET /api/projects/{pid}/stream", s.stream)
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// fail maps store errors onto status codes.
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// decode reads a JSON request body, and insists the sender said it was JSON.
//
// This is the second half of the cross-origin defence. A browser can only send
// a request without a CORS preflight when the content type is one of the
// "simple" ones — text/plain, form-encoded, multipart. Requiring
// application/json means any browser trying to write here must first ask
// permission, and a page on another origin will not get it.
//
// It is also just correct: a body that claims to be text/plain is not JSON, and
// parsing it anyway was never intentional.
func decode(r *http.Request, v any) error {
	defer r.Body.Close()

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		// Tolerated: some clients omit it on a body they always send as JSON,
		// and an absent header is not the browser bypass this guards against.
		return json.NewDecoder(r.Body).Decode(v)
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("unparseable Content-Type %q", ct)
	}
	if mt != "application/json" && !strings.HasSuffix(mt, "+json") {
		return fmt.Errorf("Content-Type must be application/json, got %q", mt)
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func qInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func qInt64(r *http.Request, key string, def int64) int64 {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// projectRoot looks up the stored root path, needed to normalize claim paths.
func (s *Server) projectRoot(pid string) string {
	if p, err := s.St.GetProject(pid); err == nil {
		return p.RootPath
	}
	return ""
}

// ---- projects --------------------------------------------------------------

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "service": "succubus", "db": s.St.Path(),
	})
}

func (s *Server) resolveProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CWD string `json:"cwd"`
	}
	decode(r, &req)
	if req.CWD == "" {
		req.CWD = "."
	}
	p := identity.ResolveProject(req.CWD)

	// Agent sessions register their project automatically from a hook, so this
	// endpoint is where junk projects get created — a session opened in a home
	// directory or a stray subfolder would otherwise appear in the dashboard
	// forever. Refuse rather than record.
	if ok, why := identity.IsRegisterable(p.RootPath); !ok {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("%s is not a project (%s)", p.RootPath, why))
		return
	}

	saved, err := s.St.UpsertProject(p.ID, p.DisplayName, p.RootPath, p.GitRemote)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.St.ListProjects()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.St.GetProject(r.PathValue("pid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// deleteProject forgets a project and everything recorded under it. The files
// in the repository are untouched — this only clears succubus's own record.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeleteProject(r.PathValue("pid")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// AgentSummary is an agent plus what it currently holds, so the sidebar can
// show activity without fetching claims and tasks per project.
type AgentSummary struct {
	store.Agent
	HeldFiles int `json:"held_files"`
	OpenTasks int `json:"open_tasks"`
	// PendingMessages is room traffic this agent has not seen yet. An agent
	// only reads the room on its next turn, so a non-zero count here means
	// "this session needs a prompt before it will respond".
	PendingMessages int `json:"pending_messages"`
	PendingMentions int `json:"pending_mentions"`
}

// ProjectSummary is one node of the sidebar tree: a project plus the agents
// working in it and enough counts to render badges without a second request.
type ProjectSummary struct {
	store.Project
	Agents    []AgentSummary `json:"agents"`
	OpenTasks int            `json:"open_tasks"`
	Claims    int            `json:"claims"`
}

// overview returns every project with its agents attached. The sidebar needs
// all projects at once — showing only the selected project's agents would
// hide the fact that agents belong to projects at all.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	projects, err := s.St.ListProjects()
	if err != nil {
		fail(w, err)
		return
	}

	out := make([]ProjectSummary, 0, len(projects))
	for _, p := range projects {
		sum := ProjectSummary{Project: p, Agents: []AgentSummary{}}

		// Tally per agent in one pass so the tree can badge each row.
		heldBy := map[string]int{}
		if claims, err := s.St.ActiveClaims(p.ID); err == nil {
			sum.Claims = len(claims)
			for _, c := range claims {
				heldBy[c.AgentID]++
			}
		}
		// Tally by assignee *name*, not id: the name is the durable identity,
		// and tasks assigned from the dashboard or by another agent carry only
		// the name.
		tasksBy := map[string]int{}
		if tasks, err := s.St.ListTasks(p.ID, store.TaskFilter{}); err == nil {
			for _, t := range tasks {
				if t.Status == store.StatusDone || t.Status == store.StatusCancelled {
					continue
				}
				sum.OpenTasks++
				if t.AssigneeName != "" {
					tasksBy[t.AssigneeName]++
				}
			}
		}
		if agents, err := s.St.ListAgents(p.ID, false); err == nil {
			for _, a := range agents {
				as := AgentSummary{
					Agent:     a,
					HeldFiles: heldBy[a.ID],
					OpenTasks: tasksBy[a.Name],
				}
				// Room traffic waiting for this agent to take a turn.
				if unread, mentions, err := s.St.UnreadFor(p.ID, a.Name, 50); err == nil {
					as.PendingMessages = len(unread) + len(mentions)
					as.PendingMentions = len(mentions)
				}
				sum.Agents = append(sum.Agents, as)
			}
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- agents ----------------------------------------------------------------

func (s *Server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PreferredName string `json:"preferred_name"`
		Tool          string `json:"tool"`
		SessionKey    string `json:"session_key"`
		CWD           string `json:"cwd"`
		PID           int    `json:"pid"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Tool == "" {
		req.Tool = "unknown"
	}
	a, reused, err := s.St.RegisterAgent(r.PathValue("pid"), req.PreferredName, req.Tool,
		req.SessionKey, req.CWD, req.PID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": a, "reused": reused, "adopted": a.Name})
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	as, err := s.St.ListAgents(r.PathValue("pid"), r.URL.Query().Get("all") == "1")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if err := s.St.Heartbeat(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) renameAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.St.RenameAgent(r.PathValue("id"), req.Name)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) unregisterAgent(w http.ResponseWriter, r *http.Request) {
	if err := s.St.UnregisterAgent(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- plans -----------------------------------------------------------------

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	ps, err := s.St.ListPlans(r.PathValue("pid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string `json:"title"`
		BodyMD    string `json:"body_md"`
		Status    string `json:"status"`
		CreatedBy string `json:"created_by"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.St.CreatePlan(r.PathValue("pid"), req.Title, req.BodyMD, req.Status, req.CreatedBy)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	p, err := s.St.GetPlan(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request) {
	var patch store.PlanPatch
	if err := decode(r, &patch); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.St.UpdatePlan(r.PathValue("id"), patch)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeletePlan(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- tasks -----------------------------------------------------------------

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	f := store.TaskFilter{
		Status:   r.URL.Query().Get("status"),
		Assignee: r.URL.Query().Get("assignee"),
		PlanID:   r.URL.Query().Get("plan_id"),
	}
	ts, err := s.St.ListTasks(r.PathValue("pid"), f)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlanID          string   `json:"plan_id"`
		Title           string   `json:"title"`
		BodyMD          string   `json:"body_md"`
		Status          string   `json:"status"`
		Priority        int      `json:"priority"`
		AssigneeAgentID string   `json:"assignee_agent_id"`
		AssigneeName    string   `json:"assignee_name"`
		DependsOn       []string `json:"depends_on"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.St.CreateTask(r.PathValue("pid"), req.PlanID, req.Title, req.BodyMD, req.Status,
		req.Priority, req.AssigneeAgentID, strings.ToUpper(req.AssigneeName), req.DependsOn)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.St.GetTask(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	var patch store.TaskPatch
	if err := decode(r, &patch); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.St.UpdateTask(r.PathValue("id"), patch)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeleteTask(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) addDep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DependsOn string `json:"depends_on"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.St.AddDep(r.PathValue("id"), req.DependsOn); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.St.GetTask(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) removeDep(w http.ResponseWriter, r *http.Request) {
	if err := s.St.RemoveDep(r.PathValue("id"), r.PathValue("depID")); err != nil {
		fail(w, err)
		return
	}
	t, err := s.St.GetTask(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) claimTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string `json:"agent_id"`
		AgentName string `json:"agent_name"`
		Force     bool   `json:"force"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.St.ClaimTask(r.PathValue("id"), req.AgentID, strings.ToUpper(req.AgentName), req.Force)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, err)
			return
		}
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) reorderTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
		Index  int    `json:"index"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, err := s.St.GetTask(r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	out, err := s.St.ReorderTask(t.ProjectID, t.ID, req.Status, req.Index)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- claims ----------------------------------------------------------------

func (s *Server) listClaims(w http.ResponseWriter, r *http.Request) {
	cs, err := s.St.ActiveClaims(r.PathValue("pid"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) claimFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string   `json:"agent_id"`
		AgentName string   `json:"agent_name"`
		TaskID    string   `json:"task_id"`
		Mode      string   `json:"mode"`
		Paths     []string `json:"paths"`
		TTLSec    int64    `json:"ttl_sec"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pid := r.PathValue("pid")
	res, err := s.St.ClaimFiles(pid, s.projectRoot(pid), req.AgentID, req.AgentName,
		req.TaskID, req.Mode, req.Paths, req.TTLSec)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	granted := true
	for _, x := range res {
		if !x.Granted {
			granted = false
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"granted": granted, "results": res})
}

func (s *Server) releaseFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string   `json:"agent_id"`
		Paths   []string `json:"paths"`
		All     bool     `json:"all"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pid := r.PathValue("pid")
	var n int64
	var err error
	if req.All {
		n, err = s.St.ReleaseAllForAgent(pid, req.AgentID)
	} else {
		n, err = s.St.ReleaseFiles(pid, s.projectRoot(pid), req.AgentID, req.Paths)
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"released": n})
}

func (s *Server) renewClaims(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string `json:"agent_id"`
		TTLSec  int64  `json:"ttl_sec"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	n, err := s.St.RenewClaims(r.PathValue("pid"), req.AgentID, req.TTLSec)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"renewed": n})
}

func (s *Server) checkFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID string   `json:"agent_id"`
		Paths   []string `json:"paths"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pid := r.PathValue("pid")
	res, err := s.St.CheckFiles(pid, s.projectRoot(pid), req.AgentID, req.Paths)
	if err != nil {
		fail(w, err)
		return
	}
	conflict := false
	var holder string
	for _, x := range res {
		if !x.Granted {
			conflict = true
			holder = x.Holder
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conflict": conflict, "holder": holder, "results": res,
	})
}

// ---- decisions & events ----------------------------------------------------

func (s *Server) listDecisions(w http.ResponseWriter, r *http.Request) {
	ds, err := s.St.ListDecisions(r.PathValue("pid"), r.URL.Query().Get("target"),
		r.URL.Query().Get("unacked") == "1", qInt64(r, "since", 0), qInt(r, "limit", 100))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ds)
}

func (s *Server) createDecision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		BodyMD      string `json:"body_md"`
		AuthorID    string `json:"author_agent_id"`
		AuthorName  string `json:"author_name"`
		TargetAgent string `json:"target_agent_name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := s.St.CreateDecision(r.PathValue("pid"), req.Kind, req.Title, req.BodyMD,
		req.AuthorID, req.AuthorName, req.TargetAgent)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) ackDecision(w http.ResponseWriter, r *http.Request) {
	if err := s.St.AckDecision(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteDecision(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeleteDecision(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("recent") == "1" {
		evs, err := s.St.RecentEvents(r.PathValue("pid"), qInt(r, "limit", 100))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, evs)
		return
	}
	evs, err := s.St.ListEvents(r.PathValue("pid"), qInt64(r, "after", 0), qInt(r, "limit", 200))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evs)
}
