// Package identity resolves a working directory to a stable project id, and
// discovers which agent a process belongs to.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Project describes a resolved project root.
type Project struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	RootPath    string `json:"root_path"`
	GitRemote   string `json:"git_remote,omitempty"`
}

// override is the optional .succubus/project.json escape hatch.
type override struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ResolveProject derives a stable identifier for the project containing dir.
//
// Priority: explicit override, then git remote (stable across clones), then git
// root path, then plain cwd. Using the remote first means the same repo checked
// out twice on one machine coordinates as one project.
func ResolveProject(dir string) Project {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}

	root := gitRoot(abs)
	if root == "" {
		root = abs
	}
	remote := gitRemote(root)

	p := Project{RootPath: root, GitRemote: remote, DisplayName: filepath.Base(root)}

	if o := readOverride(root); o != nil {
		if o.ID != "" {
			p.ID = o.ID
		}
		if o.DisplayName != "" {
			p.DisplayName = o.DisplayName
		}
	}
	if p.ID == "" {
		switch {
		case remote != "":
			p.ID = hash("remote:" + normalizeRemote(remote))
		default:
			p.ID = hash("path:" + root)
		}
	}
	return p
}

func readOverride(root string) *override {
	b, err := os.ReadFile(filepath.Join(root, ".succubus", "project.json"))
	if err != nil {
		return nil
	}
	var o override
	if json.Unmarshal(b, &o) != nil {
		return nil
	}
	return &o
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// normalizeRemote collapses the equivalent spellings of one remote so ssh and
// https clones of the same repo resolve identically.
func normalizeRemote(r string) string {
	r = strings.TrimSpace(strings.ToLower(r))
	r = strings.TrimSuffix(r, ".git")
	r = strings.TrimPrefix(r, "git@")
	r = strings.TrimPrefix(r, "https://")
	r = strings.TrimPrefix(r, "http://")
	r = strings.TrimPrefix(r, "ssh://")
	r = strings.ReplaceAll(r, ":", "/")
	return r
}

func gitRoot(dir string) string {
	out, err := runIn(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitRemote(dir string) string {
	out, err := runIn(dir, "git", "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	b, err := cmd.Output()
	return string(b), err
}

// projectMarkers are the files and directories that make a directory look like
// a project. The list is deliberately broad — a false negative only costs an
// error message, while a false positive registers somewhere nobody meant.
//
// `.succubus` is deliberately absent. succubus creates that directory itself
// when a session registers, so accepting it as evidence would be circular: one
// mistaken registration would make the directory permanently valid, and the
// mistake could never be corrected.
var projectMarkers = []string{
	".git", ".hg", ".svn", ".jj",
	"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "setup.py",
	"pom.xml", "build.gradle", "build.gradle.kts", "Gemfile", "composer.json",
	"Makefile", "CMakeLists.txt", "requirements.txt", "deno.json",
	"AGENTS.md", "CLAUDE.md",
}

// LooksLikeProject reports whether a directory carries any of the usual marks
// of one.
func LooksLikeProject(dir string) bool {
	for _, marker := range projectMarkers {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// IsRegisterable reports whether a resolved root should be recorded as a
// project at all, and why not when it should not.
//
// This has to be enforced where projects are *created*, not only in `init`:
// an agent session registers its project automatically from a SessionStart
// hook, so a session opened in a home directory or a stray subfolder would
// otherwise fill the dashboard with places nobody meant to coordinate.
func IsRegisterable(root string) (bool, string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return false, "unresolvable path"
	}
	abs = filepath.Clean(abs)

	if parent := filepath.Dir(abs); parent == abs {
		return false, "filesystem root"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if hc, err := filepath.Abs(home); err == nil && filepath.Clean(hc) == abs {
			return false, "home directory"
		}
	}
	if !LooksLikeProject(abs) {
		return false, "no version control or project file"
	}
	return true, ""
}

// SessionFile is where an agent's adopted identity is cached, so the same
// session recovers its name after a restart or a context compaction.
func SessionFile(root, sessionKey string) string {
	return filepath.Join(root, ".succubus", "agent-"+sanitize(sessionKey)+".json")
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// Cached is the on-disk record of an adopted identity.
type Cached struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	ProjectID  string `json:"project_id"`
	SessionKey string `json:"session_key"`
	Tool       string `json:"tool"`
}

func SaveCached(root string, c Cached) error {
	dir := filepath.Join(root, ".succubus")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Keep succubus bookkeeping out of the user's commits.
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		os.WriteFile(gi, []byte("*\n"), 0o644)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := SessionFile(root, c.SessionKey) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, SessionFile(root, c.SessionKey))
}

func LoadCached(root, sessionKey string) (*Cached, error) {
	b, err := os.ReadFile(SessionFile(root, sessionKey))
	if err != nil {
		return nil, err
	}
	var c Cached
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
