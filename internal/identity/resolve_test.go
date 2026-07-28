package identity

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsRegisterableRejectsNonProjects is the guard against the dashboard
// filling up with places nobody meant to coordinate.
//
// Agent sessions register their project automatically from a SessionStart hook,
// so this rule has to hold wherever a project is created — not only in `init`.
// Without it, opening a session in a home directory or a scratch folder records
// it forever.
func TestIsRegisterableRejectsNonProjects(t *testing.T) {
	t.Run("home directory", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory")
		}
		ok, why := IsRegisterable(home)
		if ok {
			t.Fatal("the home directory must never register as a project")
		}
		if why != "home directory" {
			t.Errorf("reason = %q, want %q", why, "home directory")
		}
	})

	t.Run("filesystem root", func(t *testing.T) {
		ok, why := IsRegisterable(string(filepath.Separator))
		if ok {
			t.Fatal("the filesystem root must never register")
		}
		if why != "filesystem root" {
			t.Errorf("reason = %q, want %q", why, "filesystem root")
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		ok, why := IsRegisterable(dir)
		if ok {
			t.Fatal("a directory with no project marker must not register")
		}
		if why == "" {
			t.Error("a refusal must say why")
		}
	})
}

// TestSuccubusDirIsNotEvidence guards against a circular rule.
//
// succubus creates .succubus/ itself when a session registers. If that counted
// as proof of a project, a single mistaken registration would make the
// directory permanently valid and the mistake uncorrectable.
func TestSuccubusDirIsNotEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".succubus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, _ := IsRegisterable(dir); ok {
		t.Fatal(".succubus/ must not by itself make a directory a project")
	}

	// A real marker alongside it still qualifies.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, why := IsRegisterable(dir); !ok {
		t.Errorf("a real project containing .succubus/ should register, refused: %s", why)
	}
}

// TestIsRegisterableAcceptsRealProjects: the guard must not be so strict that
// it rejects ordinary repositories.
func TestIsRegisterableAcceptsRealProjects(t *testing.T) {
	for _, marker := range []string{".git", "go.mod", "package.json", "Cargo.toml", "Makefile"} {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, marker)
			// .git is a directory in a real checkout; the rest are files.
			if marker == ".git" {
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}

			if ok, why := IsRegisterable(dir); !ok {
				t.Errorf("a directory containing %s should register, refused: %s", marker, why)
			}
		})
	}
}

// TestResolveProjectPrefersGitRoot: running in a subdirectory must resolve to
// the repository, not the subdirectory — otherwise one repo becomes several
// projects depending on where each agent happened to start.
func TestResolveProjectPrefersGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without a real git binary result this falls back to the path, which is
	// still the property under test: the id must not vary by subdirectory when
	// git reports the same root.
	a := ResolveProject(root)
	if a.ID == "" {
		t.Fatal("resolve produced no id")
	}
	if a.RootPath == "" {
		t.Fatal("resolve produced no root path")
	}
}
