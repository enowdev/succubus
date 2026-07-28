package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/enowdev/succubus/internal/store"
)

// TestFrontendListensForEveryEvent guards a failure mode with no symptom.
//
// SSE events with a name never reach EventSource.onmessage — only a listener
// registered for that exact name sees them. An event the dashboard does not
// register is simply never delivered: no error, no warning, the page just quietly
// stops being live for that kind of change. This is how room.message shipped
// broken.
func TestFrontendListensForEveryEvent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("cannot locate repository root: %v", err)
	}

	emitted, err := eventConstants(filepath.Join(root, "internal", "store", "model.go"))
	if err != nil {
		t.Fatalf("read server events: %v", err)
	}
	if len(emitted) == 0 {
		t.Fatal("found no Ev* constants — the parser needs updating")
	}

	registered, err := frontendEventTypes(filepath.Join(root, "web", "src", "lib", "sse.ts"))
	if err != nil {
		t.Skipf("cannot read the frontend event list: %v", err)
	}

	var missing []string
	for _, e := range emitted {
		if !registered[e] {
			missing = append(missing, e)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the dashboard never listens for these events, so they are not live: %v\n"+
			"add them to EVENT_TYPES in web/src/lib/sse.ts", missing)
	}
}

// TestEventNamesAreStable pins the names the frontend and the hook dialects
// both depend on.
func TestEventNamesAreStable(t *testing.T) {
	for _, want := range []struct{ got, expect string }{
		{store.EvAgentRegistered, "agent.registered"},
		{store.EvAgentLeft, "agent.left"},
		{store.EvTaskMoved, "task.moved"},
		{store.EvClaimGranted, "claim.granted"},
		{store.EvClaimDenied, "claim.denied"},
		{store.EvRoomMessage, "room.message"},
		{store.EvHandoff, "handoff"},
	} {
		if want.got != want.expect {
			t.Errorf("event name changed: got %q, expected %q", want.got, want.expect)
		}
	}
}

var evConstRe = regexp.MustCompile(`Ev\w+\s*=\s*"([^"]+)"`)

func eventConstants(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range evConstRe.FindAllStringSubmatch(string(b), -1) {
		out = append(out, m[1])
	}
	return out, nil
}

func frontendEventTypes(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(b)
	start := strings.Index(src, "EVENT_TYPES")
	if start < 0 {
		return nil, os.ErrNotExist
	}
	end := strings.Index(src[start:], "] as const")
	if end < 0 {
		return nil, os.ErrNotExist
	}

	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).
		FindAllStringSubmatch(src[start:start+end], -1) {
		out[m[1]] = true
	}
	return out, nil
}

// repoRoot walks up until it finds go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
