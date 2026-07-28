package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Webhook delivery.
//
// A note on what this can and cannot do. Agents have no HTTP endpoint and no
// process running between their turns, so there is nothing to push *to* — an
// agent learns about a message when its session next takes a turn, and no
// amount of outbound plumbing changes that.
//
// What webhooks are for is reaching a human: Slack, Discord, a phone. An
// unanswered question sitting in the room for an hour because nobody was
// looking at the dashboard is the failure this fixes.

// WebhookConfig is one delivery target.
type WebhookConfig struct {
	URL string `json:"url"`
	// Events to deliver. Empty means the recommended set: things a person
	// would actually want to be interrupted for.
	Events []string `json:"events,omitempty"`
	// Format shapes the payload: "json" (raw event), "slack", or "discord".
	Format string `json:"format,omitempty"`
}

// defaultWebhookEvents are the ones worth a notification. Task and claim churn
// is constant and would train you to ignore the channel.
var defaultWebhookEvents = []string{
	EvRoomMessage,
	EvHandoff,
	EvClaimDenied,
	EvAgentLeft,
}

// webhookSender delivers events to configured targets.
type webhookSender struct {
	mu      sync.RWMutex
	targets []WebhookConfig
	client  *http.Client
}

func newWebhookSender() *webhookSender {
	return &webhookSender{
		// Short timeout: a slow endpoint must never back up the event bus.
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// SetWebhooks replaces the delivery targets.
func (s *Store) SetWebhooks(targets []WebhookConfig) {
	s.hooks.mu.Lock()
	defer s.hooks.mu.Unlock()
	s.hooks.targets = targets
}

// Webhooks returns the configured targets.
func (s *Store) Webhooks() []WebhookConfig {
	s.hooks.mu.RLock()
	defer s.hooks.mu.RUnlock()
	out := make([]WebhookConfig, len(s.hooks.targets))
	copy(out, s.hooks.targets)
	return out
}

// wants reports whether a target has asked for this event type.
func (w WebhookConfig) wants(eventType string) bool {
	list := w.Events
	if len(list) == 0 {
		list = defaultWebhookEvents
	}
	for _, e := range list {
		if e == eventType || e == "*" {
			return true
		}
		// "room.*" matches every room event.
		if strings.HasSuffix(e, ".*") &&
			strings.HasPrefix(eventType, strings.TrimSuffix(e, "*")) {
			return true
		}
	}
	return false
}

// deliver fires an event at every interested target, in the background.
//
// Delivery is best-effort and never blocks the caller: a webhook endpoint that
// is down must not slow down agents doing real work.
func (s *Store) deliver(ev Event) {
	targets := s.Webhooks()
	if len(targets) == 0 {
		return
	}
	for _, t := range targets {
		if !t.wants(ev.Type) {
			continue
		}
		go s.hooks.post(t, ev, s)
	}
}

func (w *webhookSender) post(target WebhookConfig, ev Event, st *Store) {
	body, err := json.Marshal(payloadFor(target.Format, ev, st))
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", target.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "succubus")
	req.Header.Set("X-Succubus-Event", ev.Type)

	resp, err := w.client.Do(req)
	if err != nil {
		log.Printf("webhook %s: %v", redact(target.URL), err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		log.Printf("webhook %s: %s", redact(target.URL), resp.Status)
	}
}

// payloadFor shapes the body for the destination. Slack and Discord both
// accept a bare `text`/`content` field, which is all a notification needs.
func payloadFor(format string, ev Event, st *Store) any {
	switch format {
	case "slack":
		return map[string]any{"text": describe(ev, st)}
	case "discord":
		return map[string]any{"content": describe(ev, st)}
	default:
		return map[string]any{
			"event":      ev.Type,
			"project_id": ev.ProjectID,
			"agent":      ev.AgentName,
			"subject_id": ev.SubjectID,
			"payload":    ev.Payload,
			"created_at": ev.CreatedAt,
			"text":       describe(ev, st),
		}
	}
}

// describe renders an event as one line a person can act on.
func describe(ev Event, st *Store) string {
	who := ev.AgentName
	if who == "" {
		who = "succubus"
	}
	project := ev.ProjectID
	if p, err := st.GetProject(ev.ProjectID); err == nil {
		project = p.DisplayName
	}

	detail := ""
	if m, ok := ev.Payload.(map[string]any); ok {
		if v, ok := m["preview"].(string); ok {
			detail = v
		} else if v, ok := m["title"].(string); ok {
			detail = v
		} else if v, ok := m["reason"].(string); ok {
			detail = v
		}
	}

	switch ev.Type {
	case EvRoomMessage:
		kind := ""
		if m, ok := ev.Payload.(map[string]any); ok {
			if k, ok := m["kind"].(string); ok && k == MsgQuestion {
				kind = " asked a question"
			}
		}
		if kind == "" {
			kind = " posted"
		}
		return fmt.Sprintf("[%s] %s%s: %s", project, who, kind, detail)
	case EvHandoff:
		return fmt.Sprintf("[%s] %s sent a handoff: %s", project, who, detail)
	case EvClaimDenied:
		return fmt.Sprintf("[%s] %s was blocked from editing: %s", project, who, detail)
	case EvAgentLeft:
		return fmt.Sprintf("[%s] %s left", project, who)
	case EvAgentRegistered:
		return fmt.Sprintf("[%s] %s joined", project, who)
	}
	if detail != "" {
		return fmt.Sprintf("[%s] %s — %s (%s)", project, ev.Type, detail, who)
	}
	return fmt.Sprintf("[%s] %s (%s)", project, ev.Type, who)
}

// redact keeps webhook tokens out of the log — Slack and Discord URLs carry
// their secret in the path.
func redact(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return u[:i+3] + rest[:j] + "/…"
		}
	}
	return u
}
