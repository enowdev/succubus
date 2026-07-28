// Package docs serves the project documentation from the same markdown files
// that live in this directory.
//
// Embedding the real files rather than copying prose into Go strings means the
// dashboard, the MCP `succubus_docs` tool, and anyone reading the repository on
// GitHub all see exactly the same text — there is no second copy to go stale.
package docs

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.md
var files embed.FS

// Section is one documentation page.
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Summary is the first paragraph, used as a subtitle in the UI.
	Summary string `json:"summary"`
}

// order fixes the reading sequence; anything not listed sorts after, by name.
var order = []string{"SETUP", "MCP", "ARCHITECTURE", "TROUBLESHOOTING"}

// titles overrides the H1 for nav display, where the document title is longer
// than a sidebar entry should be.
var titles = map[string]string{
	"SETUP":           "Setup",
	"MCP":             "MCP tools",
	"ARCHITECTURE":    "Architecture",
	"TROUBLESHOOTING": "Troubleshooting",
}

// List returns the table of contents.
func List() []Section {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil
	}

	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}

	out := make([]Section, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		body, err := files.ReadFile(e.Name())
		if err != nil {
			continue
		}
		title, ok := titles[id]
		if !ok {
			title = headingOf(string(body), id)
		}
		out = append(out, Section{ID: id, Title: title, Summary: summaryOf(string(body))})
	}

	sort.Slice(out, func(i, j int) bool {
		ri, oki := rank[out[i].ID]
		rj, okj := rank[out[j].ID]
		switch {
		case oki && okj:
			return ri < rj
		case oki:
			return true
		case okj:
			return false
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns one section's markdown.
func Get(id string) (string, error) {
	// Reject anything that could escape the embedded directory.
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return "", fmt.Errorf("unknown section %q", id)
	}
	b, err := files.ReadFile(id + ".md")
	if err != nil {
		return "", fmt.Errorf("unknown section %q", id)
	}
	return string(b), nil
}

// headingOf returns the first H1, falling back to the supplied default.
func headingOf(md, fallback string) string {
	for line := range strings.Lines(md) {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return fallback
}

// summaryOf returns the first real paragraph, stripped of markdown emphasis.
//
// Documents that open straight into a subheading have no paragraph under the
// H1, so headings are skipped rather than treated as a stopping point — the
// first prose anywhere in the document is what describes it.
func summaryOf(md string) string {
	var b strings.Builder
	fenced := false

	for line := range strings.Lines(md) {
		t := strings.TrimSpace(line)

		if strings.HasPrefix(t, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(t, "#") {
			continue // any heading level
		}
		if t == "" {
			if b.Len() > 0 {
				break // paragraph ended
			}
			continue
		}
		// Structure is not prose: skip it while still looking for a paragraph.
		if strings.HasPrefix(t, "|") || strings.HasPrefix(t, "- ") ||
			strings.HasPrefix(t, "* ") || strings.HasPrefix(t, ">") ||
			strings.HasPrefix(t, "---") {
			if b.Len() > 0 {
				break
			}
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(t)
	}

	s := strings.ReplaceAll(b.String(), "**", "")
	s = strings.ReplaceAll(s, "`", "")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
