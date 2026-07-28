package mode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enowdev/succubus/internal/store"
)

// newTestMCP returns a server writing into a buffer, so pushes can be inspected.
func newTestMCP() (*mcpServer, *bytes.Buffer) {
	var buf bytes.Buffer
	s := &mcpServer{out: bufio.NewWriter(&buf)}
	s.channel = newChannelPusher(s)
	return s, &buf
}

// TestChannelNotificationShape pins the wire format Claude Code listens for.
// A typo in the method name or a renamed field means events are dropped
// silently — there is no error, the session simply never hears anything.
func TestChannelNotificationShape(t *testing.T) {
	s, buf := newTestMCP()
	s.pushChannel("hello", map[string]string{"from": "HUMAN", "reply_to": "abc"})

	var n struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Content string            `json:"content"`
			Meta    map[string]string `json:"meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal(buf.Bytes(), &n); err != nil {
		t.Fatalf("push is not valid JSON: %v\n%s", err, buf.String())
	}

	if n.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want 2.0", n.JSONRPC)
	}
	if n.Method != "notifications/claude/channel" {
		t.Errorf("method = %q — Claude Code listens for notifications/claude/channel", n.Method)
	}
	if n.Params.Content != "hello" {
		t.Errorf("content = %q", n.Params.Content)
	}
	if n.Params.Meta["from"] != "HUMAN" {
		t.Errorf("meta lost its entries: %v", n.Params.Meta)
	}

	// A notification carries no id: anything with one is a request, and Claude
	// Code would wait for a response that never comes.
	if bytes.Contains(buf.Bytes(), []byte(`"id"`)) {
		t.Error("a notification must not carry an id")
	}
	// One frame, newline-delimited.
	if strings.Count(strings.TrimSpace(buf.String()), "\n") != 0 {
		t.Errorf("expected a single line, got:\n%s", buf.String())
	}
}

// TestChannelMetaKeysAreIdentifiers guards a silent-drop rule: Claude Code
// discards meta keys containing anything but letters, digits, and underscores.
func TestChannelMetaKeysAreIdentifiers(t *testing.T) {
	s, buf := newTestMCP()
	s.channel.identify("p1", "ORION")

	ev := store.Event{
		ID: 1, ProjectID: "p1", Type: store.EvRoomMessage,
		AgentName: "HUMAN", SubjectID: "msg1",
		Payload: map[string]any{
			"mentions": []any{"ORION"},
			"preview":  "hello there",
			"kind":     store.MsgMessage,
		},
	}
	raw, _ := json.Marshal(ev)
	s.channel.handle(string(raw))

	var n channelNotification
	if err := json.Unmarshal(buf.Bytes(), &n); err != nil {
		t.Fatalf("no push emitted: %v", err)
	}
	for k := range n.Params.Meta {
		for _, r := range k {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '_'
			if !ok {
				t.Errorf("meta key %q contains %q — Claude Code drops such keys silently", k, r)
			}
		}
	}
}

// TestChannelOnlyPushesAddressedMessages: pushing every room message into a
// live session would make the channel unusable. Only a mention interrupts.
func TestChannelOnlyPushesAddressedMessages(t *testing.T) {
	cases := []struct {
		name     string
		author   string
		mentions []any
		want     bool
	}{
		{"addressed by name", "HUMAN", []any{"ORION"}, true},
		{"broadcast", "HUMAN", []any{store.MentionAll}, true},
		{"someone else", "HUMAN", []any{"VESPER"}, false},
		{"no mentions", "HUMAN", nil, false},
		{"own message", "ORION", []any{"ORION"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, buf := newTestMCP()
			s.channel.identify("p1", "ORION")

			payload := map[string]any{"preview": "x", "kind": store.MsgMessage}
			if c.mentions != nil {
				payload["mentions"] = c.mentions
			}
			raw, _ := json.Marshal(store.Event{
				ID: 1, ProjectID: "p1", Type: store.EvRoomMessage,
				AgentName: c.author, SubjectID: "m1", Payload: payload,
			})
			s.channel.handle(string(raw))

			got := buf.Len() > 0
			if got != c.want {
				t.Errorf("pushed = %v, want %v", got, c.want)
			}
		})
	}
}

// TestChannelIgnoresOtherProjects: one daemon serves every project, so a
// session must not be interrupted by traffic from a repository it is not in.
func TestChannelIgnoresOtherProjects(t *testing.T) {
	s, buf := newTestMCP()
	s.channel.identify("p1", "ORION")

	raw, _ := json.Marshal(store.Event{
		ID: 1, ProjectID: "p2", Type: store.EvRoomMessage,
		AgentName: "HUMAN", SubjectID: "m1",
		Payload: map[string]any{"mentions": []any{"ORION"}, "preview": "x"},
	})
	s.channel.handle(string(raw))

	if buf.Len() > 0 {
		t.Errorf("pushed an event from another project:\n%s", buf.String())
	}
}

// TestChannelDeduplicates: the stream replays events after a reconnect, and a
// repeated push would show the same message twice in the session.
func TestChannelDeduplicates(t *testing.T) {
	s, buf := newTestMCP()
	s.channel.identify("p1", "ORION")

	raw, _ := json.Marshal(store.Event{
		ID: 7, ProjectID: "p1", Type: store.EvRoomMessage,
		AgentName: "HUMAN", SubjectID: "m1",
		Payload: map[string]any{"mentions": []any{"ORION"}, "preview": "once"},
	})
	s.channel.handle(string(raw))
	first := buf.Len()
	s.channel.handle(string(raw))

	if buf.Len() != first {
		t.Error("the same event id was pushed twice")
	}
}

// TestChannelSilentUntilIdentified: before registration there is no name to
// filter on, so nothing may be pushed.
func TestChannelSilentUntilIdentified(t *testing.T) {
	s, buf := newTestMCP()

	raw, _ := json.Marshal(store.Event{
		ID: 1, ProjectID: "p1", Type: store.EvRoomMessage,
		AgentName: "HUMAN", SubjectID: "m1",
		Payload: map[string]any{"mentions": []any{"ORION"}, "preview": "x"},
	})
	s.channel.handle(string(raw))

	if buf.Len() > 0 {
		t.Error("pushed before the session had an identity")
	}
}
