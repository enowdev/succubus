package mode

import (
	"strings"
	"testing"
)

// The tool schemas declare required arguments, but nothing enforced them. A
// call missing an id was passed straight through, interpolated into the URL,
// and produced `POST /api/room//resolve: no such endpoint` — an error about
// the endpoint when the real problem was the argument. Not every MCP client
// validates against the schema before sending, so the server has to.
func TestRequiredArgumentsAreEnforced(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"succubus_resolve", map[string]any{}, "id"},
		{"succubus_resolve", map[string]any{"id": ""}, "id"},
		{"succubus_resolve", map[string]any{"id": "   "}, "id"},
		{"succubus_task_update", map[string]any{"status": "done"}, "id"},
		{"succubus_task_claim", map[string]any{}, "id"},
		{"succubus_plan_get", map[string]any{}, "id"},
		{"succubus_claim_files", map[string]any{"paths": []any{}}, "paths"},
		{"succubus_say", map[string]any{}, "message"},
		{"succubus_ask", map[string]any{}, "question"},
	}

	for _, c := range cases {
		missing := missingRequired(c.tool, c.args)
		if len(missing) == 0 {
			t.Errorf("%s%v: accepted a call missing %q", c.tool, c.args, c.want)
			continue
		}
		if !contains(missing, c.want) {
			t.Errorf("%s: reported %v, expected it to name %q", c.tool, missing, c.want)
		}
	}
}

// A complete call must not be rejected — an over-eager check would break every
// agent instead of only the malformed calls.
func TestCompleteCallsAreAccepted(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"succubus_resolve", map[string]any{"id": "01ABC"}},
		{"succubus_task_update", map[string]any{"id": "01ABC", "status": "done"}},
		{"succubus_claim_files", map[string]any{"paths": []any{"a.go"}}},
		{"succubus_say", map[string]any{"message": "hello"}},
		{"succubus_ask", map[string]any{"question": "why?"}},
		// Tools with no required arguments at all.
		{"succubus_context", map[string]any{}},
		{"succubus_agents", map[string]any{}},
		{"succubus_whoami", map[string]any{}},
		{"succubus_task_list", map[string]any{}},
	}
	for _, c := range cases {
		if missing := missingRequired(c.tool, c.args); len(missing) > 0 {
			t.Errorf("%s: a valid call was rejected for missing %v", c.tool, missing)
		}
	}
}

// TestEveryDeclaredRequirementIsCheckable walks the published schemas: if a
// tool declares a requirement this cannot see, the enforcement silently does
// nothing for it.
func TestEveryDeclaredRequirementIsCheckable(t *testing.T) {
	for _, tool := range toolSchemas() {
		name, _ := tool["name"].(string)
		schema, ok := tool["inputSchema"].(map[string]any)
		if !ok {
			continue
		}
		required, ok := schema["required"].([]string)
		if !ok || len(required) == 0 {
			continue
		}
		// An empty argument map must report every one of them.
		missing := missingRequired(name, map[string]any{})
		if len(missing) != len(required) {
			t.Errorf("%s declares %v as required but an empty call reported %v",
				name, required, missing)
		}
	}
}

// An unknown tool has no schema, so it must not be rejected here — the
// dispatcher below reports it, with a better message.
func TestUnknownToolIsNotRejectedByTheArgumentCheck(t *testing.T) {
	if missing := missingRequired("succubus_not_a_tool", map[string]any{}); len(missing) > 0 {
		t.Errorf("an unknown tool was reported as missing %v", missing)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
