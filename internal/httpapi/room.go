package httpapi

import (
	"net/http"

	"github.com/enowdev/succubus/internal/store"
)

// RoomPayload is the room plus the counts the header needs.
type RoomPayload struct {
	Messages      []store.Message `json:"messages"`
	OpenQuestions []store.Message `json:"open_questions"`
	Total         int             `json:"total"`
	Open          int             `json:"open"`
}

func (s *Server) roomList(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	msgs, err := s.St.RoomMessages(pid, qInt(r, "limit", 60))
	if err != nil {
		fail(w, err)
		return
	}
	open, err := s.St.OpenQuestions(pid, 20)
	if err != nil {
		fail(w, err)
		return
	}
	total, openCount, err := s.St.RoomStats(pid)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, RoomPayload{
		Messages: msgs, OpenQuestions: open, Total: total, Open: openCount,
	})
}

func (s *Server) roomPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ParentID   string   `json:"parent_id"`
		Kind       string   `json:"kind"`
		AuthorID   string   `json:"author_id"`
		AuthorName string   `json:"author_name"`
		BodyMD     string   `json:"body_md"`
		Mentions   []string `json:"mentions"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// An empty author means the human is posting from the dashboard.
	if req.AuthorName == "" {
		req.AuthorName = store.HumanAuthor
	}

	m, err := s.St.PostMessage(r.PathValue("pid"), req.ParentID, req.Kind,
		req.AuthorID, req.AuthorName, req.BodyMD, req.Mentions)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// roomMarkRead records that an agent has been shown the room up to now, so the
// next injection reports only what arrived since.
func (s *Server) roomMarkRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentName string `json:"agent_name"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.St.MarkRoomRead(r.PathValue("pid"), req.AgentName); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) roomResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		By string `json:"by"`
	}
	decode(r, &req)
	if req.By == "" {
		req.By = store.HumanAuthor
	}
	if err := s.St.ResolveQuestion(r.PathValue("id"), req.By); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) roomDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeleteMessage(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
