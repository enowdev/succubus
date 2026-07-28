package mode

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/enowdev/succubus/internal/store"
)

// autoWaker spawns a headless turn when an agent is addressed by name, so a
// message does not sit unread until that session is next prompted.
//
// This is opt-in (config: auto_wake) because waking starts a real, billable
// turn. Left off, succubus behaves as before: the message waits.
type autoWaker struct {
	st    *store.Store
	delay time.Duration

	mu      sync.Mutex
	pending map[string]*time.Timer // agent id → debounce timer
	running map[string]bool        // agent id → a turn is already in flight
}

func newAutoWaker(st *store.Store, delay time.Duration) *autoWaker {
	if delay <= 0 {
		delay = 20 * time.Second
	}
	return &autoWaker{
		st:      st,
		delay:   delay,
		pending: map[string]*time.Timer{},
		running: map[string]bool{},
	}
}

// Watch subscribes to the event bus and wakes agents named in room messages.
func (w *autoWaker) Watch(ctx context.Context) {
	events, cancel := w.st.Subscribe()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type != store.EvRoomMessage {
				continue
			}
			w.consider(ctx, ev)
		}
	}
}

// consider works out who the message names, and schedules a wake for each.
func (w *autoWaker) consider(ctx context.Context, ev store.Event) {
	payload, ok := ev.Payload.(map[string]any)
	if !ok {
		return
	}
	raw, _ := payload["mentions"].([]any)
	if len(raw) == 0 {
		return
	}

	named := map[string]bool{}
	all := false
	for _, m := range raw {
		if s, ok := m.(string); ok {
			if s == store.MentionAll {
				all = true
			}
			named[s] = true
		}
	}
	// @ALL is a broadcast. Waking every agent on it would turn one announcement
	// into a fleet of billable turns.
	if all && len(named) == 1 {
		return
	}

	agents, err := w.st.ListAgents(ev.ProjectID, false)
	if err != nil {
		return
	}
	for _, a := range agents {
		if !named[a.Name] || a.Name == ev.AgentName {
			continue // not addressed, or the author itself
		}
		if !CanWake(a.Tool) {
			continue // no headless mode for this tool on this machine
		}
		w.schedule(ctx, a, ev.ProjectID)
	}
}

// schedule debounces per agent: a burst of messages produces one turn.
func (w *autoWaker) schedule(ctx context.Context, a store.Agent, projectID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running[a.ID] {
		return // already answering; it will pick up everything unread
	}
	if t, ok := w.pending[a.ID]; ok {
		t.Reset(w.delay)
		return
	}

	w.pending[a.ID] = time.AfterFunc(w.delay, func() {
		w.mu.Lock()
		delete(w.pending, a.ID)
		if w.running[a.ID] {
			w.mu.Unlock()
			return
		}
		w.running[a.ID] = true
		w.mu.Unlock()

		defer func() {
			w.mu.Lock()
			delete(w.running, a.ID)
			w.mu.Unlock()
		}()

		w.wake(ctx, a, projectID)
	})
}

// wake runs the headless turn.
func (w *autoWaker) wake(ctx context.Context, a store.Agent, projectID string) {
	// Re-check under the delay: the agent may have been prompted in the
	// meantime and answered already, making the wake unnecessary.
	_, mentions, err := w.st.UnreadFor(projectID, a.Name, 20)
	if err != nil {
		return
	}
	var direct []store.Message
	for _, m := range mentions {
		if m.DirectMention {
			direct = append(direct, m)
		}
	}
	if len(direct) == 0 {
		return
	}

	proj, err := w.st.GetProject(projectID)
	if err != nil {
		return
	}

	log.Printf("auto-wake: starting a turn for %s (%s), %d message(s)",
		a.Name, a.Tool, len(direct))

	if err := runWake(a, proj.RootPath, direct, 3*time.Minute); err != nil {
		log.Printf("auto-wake: %s failed: %v", a.Name, err)
		return
	}
	log.Printf("auto-wake: %s answered", a.Name)
}

// wakeableSummary describes what auto-wake can reach, for the startup log.
func wakeableSummary() string {
	tools := WakeableTools()
	if len(tools) == 0 {
		return "no tool on this machine has a headless mode — auto-wake will do nothing"
	}
	out := ""
	for i, t := range tools {
		if i > 0 {
			out += ", "
		}
		out += t
	}
	return out
}

