package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// stream serves the dashboard's live event feed.
//
// Two details make this work in practice: the write deadline must be cleared or
// the server's WriteTimeout kills long-lived streams, and every event carries
// its database id so a reconnecting client can replay via Last-Event-ID.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // defeat proxy buffering

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		// Not fatal: some ResponseWriters do not support deadlines.
		_ = err
	}

	// Subscribe before replaying, so events arriving during replay are not lost.
	ch, cancel := s.St.Subscribe()
	defer cancel()

	if last := lastEventID(r); last > 0 {
		missed, err := s.St.ListEvents(pid, last, 500)
		if err == nil {
			for _, ev := range missed {
				if !writeEvent(w, rc, ev.ID, ev.Type, ev) {
					return
				}
			}
		}
	}

	// An initial comment flushes headers so the browser fires onopen promptly.
	fmt.Fprint(w, ": connected\n\n")
	rc.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.ProjectID != pid {
				continue
			}
			if !writeEvent(w, rc, ev.ID, ev.Type, ev) {
				return
			}

		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// writeEvent emits one SSE frame. It reports false when the client is gone.
func writeEvent(w http.ResponseWriter, rc *http.ResponseController, id int64, typ string, payload any) bool {
	b, err := json.Marshal(payload)
	if err != nil {
		return true // skip malformed payload, keep the stream alive
	}
	if id > 0 {
		fmt.Fprintf(w, "id: %d\n", id)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b)
	return rc.Flush() == nil
}

func lastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("last_event_id")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
