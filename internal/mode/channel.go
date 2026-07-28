package mode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/succubus/internal/store"
)

// Channel support: pushing room messages into a *live* Claude Code session.
//
// This is the piece that makes the room feel like chat rather than email. An
// interactive agent has no inbound endpoint of its own — but it does spawn its
// MCP servers as subprocesses over stdio, and an MCP server may send unsolicited
// notifications at any time. Claude Code turns a `notifications/claude/channel`
// notification into a <channel> tag in the live session, and the agent reacts
// without the human typing anything.
//
// So succubus's own MCP process doubles as the channel: it subscribes to the
// daemon's SSE stream and forwards anything addressed to this agent.
//
// Requires launching with:
//   claude --dangerously-load-development-channels server:succubus
//
// Without that flag the notifications are dropped silently, and succubus falls
// back to what it did before: the message waits for the next turn.

// channelNotification is the wire shape Claude Code listens for.
type channelNotification struct {
	JSONRPC string             `json:"jsonrpc"`
	Method  string             `json:"method"`
	Params  channelNotifParams `json:"params"`
}

type channelNotifParams struct {
	// Content becomes the body of the <channel> tag.
	Content string `json:"content"`
	// Meta entries become attributes on the tag. Keys must be identifiers —
	// letters, digits and underscores only; anything else is dropped silently.
	Meta map[string]string `json:"meta,omitempty"`
}

// channelPusher forwards room traffic from the daemon into the live session.
type channelPusher struct {
	srv *mcpServer

	mu      sync.Mutex
	agent   string // the name this session answers to
	project string
	// seen guards against re-pushing a message after a stream reconnect.
	seen map[string]bool
}

func newChannelPusher(s *mcpServer) *channelPusher {
	return &channelPusher{srv: s, seen: map[string]bool{}}
}

// identify records who this session is, once registration has happened. Until
// then there is nothing to filter on and nothing is pushed.
func (p *channelPusher) identify(projectID, agentName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.project, p.agent = projectID, agentName
}

func (p *channelPusher) identity() (projectID, agentName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.project, p.agent
}

// Watch follows the daemon's event stream and pushes relevant room messages.
//
// It reconnects on its own: the daemon may be restarted, or not running yet
// when the session starts, and neither should permanently disable the channel.
func (p *channelPusher) Watch() {
	backoff := time.Second
	for {
		// Resolve who this session is, if a tool call has not already done it.
		// Waiting for the first succubus tool call would mean a session that
		// never calls one is unreachable — which is exactly the session most
		// in need of being interrupted.
		if _, name := p.identity(); name == "" {
			p.resolveIdentity()
		}
		if p.stream() {
			backoff = time.Second // a clean run resets the delay
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// resolveIdentity registers this session with the daemon so the pusher knows
// which messages belong to it.
func (p *channelPusher) resolveIdentity() {
	sess, err := OpenSession("", true)
	if err != nil || sess.AgentName == "" {
		return
	}
	p.srv.sess = sess
	p.identify(sess.Project.ID, sess.AgentName)
}

// stream holds one SSE connection open. It returns true if it was connected
// long enough to count as a healthy run.
func (p *channelPusher) stream() bool {
	projectID, _ := p.identity()
	if projectID == "" {
		return false // not registered yet
	}

	req, err := http.NewRequest("GET",
		"http://"+DefaultAddr()+"/api/projects/"+projectID+"/stream", nil)
	if err != nil {
		return false
	}
	// No timeout: this connection is meant to stay open.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	started := time.Now()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var event string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if event == store.EvRoomMessage {
				p.handle(data)
			}
			event = ""
		}
	}
	return time.Since(started) > 10*time.Second
}

// handle decides whether one room event belongs to this session, and pushes it.
func (p *channelPusher) handle(data string) {
	var ev store.Event
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return
	}
	projectID, agentName := p.identity()
	if agentName == "" || ev.ProjectID != projectID {
		return
	}
	// Never push an agent's own message back at it.
	if strings.EqualFold(ev.AgentName, agentName) {
		return
	}

	payload, _ := ev.Payload.(map[string]any)
	if payload == nil {
		return
	}

	// Only messages that name this agent are worth interrupting a live session
	// for. General room traffic still arrives through context injection on the
	// next turn — pushing all of it would make the channel unusable.
	var mentions []string
	if raw, ok := payload["mentions"].([]any); ok {
		for _, m := range raw {
			if s, ok := m.(string); ok {
				mentions = append(mentions, s)
			}
		}
	}
	addressed := false
	for _, m := range mentions {
		if strings.EqualFold(m, agentName) || m == store.MentionAll {
			addressed = true
			break
		}
	}
	if !addressed {
		return
	}

	// Deduplicate across reconnects: replayed events carry the same id.
	key := strconv.FormatInt(ev.ID, 10)
	p.mu.Lock()
	if p.seen[key] {
		p.mu.Unlock()
		return
	}
	p.seen[key] = true
	// Keep the set small; ids are monotonic so old ones never return except on
	// a replay window we have already covered.
	if len(p.seen) > 500 {
		p.seen = map[string]bool{key: true}
	}
	p.mu.Unlock()

	preview, _ := payload["preview"].(string)
	kind, _ := payload["kind"].(string)
	if preview == "" {
		preview = "(no content)"
	}

	body := fmt.Sprintf("%s in the succubus agent room: %s", ev.AgentName, preview)
	if kind == store.MsgQuestion {
		body = fmt.Sprintf("%s asked you a question in the succubus agent room: %s",
			ev.AgentName, preview)
	}
	body += fmt.Sprintf("\n\nReply with succubus_say, setting reply_to=%s.", ev.SubjectID)

	p.srv.pushChannel(body, map[string]string{
		"from":     ev.AgentName,
		"kind":     kind,
		"reply_to": ev.SubjectID,
		"agent":    agentName,
	})
}

// pushChannel emits a channel notification on the MCP stdio transport.
//
// Notifications are unsolicited and unacknowledged: they carry no id, and the
// write returning says nothing about whether Claude saw it. If the session was
// not started with channels enabled, this is silently ignored — which is why
// the room still works without it.
func (s *mcpServer) pushChannel(content string, meta map[string]string) {
	n := channelNotification{
		JSONRPC: "2.0",
		Method:  "notifications/claude/channel",
		Params:  channelNotifParams{Content: content, Meta: meta},
	}
	b, err := json.Marshal(n)
	if err != nil {
		return
	}

	// stdout is shared with the request/response path, so serialise writes.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.out.Write(b)
	s.out.WriteByte('\n')
	s.out.Flush()
}
