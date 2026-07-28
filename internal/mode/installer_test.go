package mode

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The installers and the release build agree on a filename by convention alone:
// the Makefile writes dist/succubus-<version>-<os>-<arch>[.exe], and both
// install scripts reconstruct that string to build a download URL.
//
// Nothing enforces that agreement at build time. If the Makefile's naming ever
// changes, `make release` still succeeds and CI still passes — the failure
// appears only when a user runs the installer and gets a 404, which is both the
// worst place to discover it and the hardest to notice.
//
// These tests are that enforcement.

func repoRoot(t *testing.T) string {
	t.Helper()
	// internal/mode -> repo root
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// TestInstallersMatchReleaseAssetNames pins the shared filename convention.
func TestInstallersMatchReleaseAssetNames(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	// The Makefile builds: dist/succubus-$(VERSION)-$$os-$$arch$$ext
	if !strings.Contains(makefile, `out="dist/succubus-$(VERSION)-$$os-$$arch$$ext"`) {
		t.Fatal("the release target's output naming changed; the installers " +
			"construct this filename themselves and must be updated together")
	}

	// install.sh builds: succubus-${VERSION}-${PLATFORM}
	sh := readRepoFile(t, "install.sh")
	if !strings.Contains(sh, `ASSET="succubus-${VERSION}-${PLATFORM}"`) {
		t.Error("install.sh no longer constructs succubus-<version>-<os>-<arch>")
	}
	if !strings.Contains(sh, `PLATFORM="${os}-${arch}"`) {
		t.Error("install.sh no longer joins os and arch as <os>-<arch>")
	}

	// install.ps1 builds: succubus-$version-$platform.exe
	ps := readRepoFile(t, "install.ps1")
	if !strings.Contains(ps, `$asset = "succubus-$version-$platform.exe"`) {
		t.Error("install.ps1 no longer constructs succubus-<version>-<os>-<arch>.exe")
	}
}

// TestInstallersCoverEveryReleasedPlatform: a platform the release publishes but
// no installer can select is a binary nobody can install through the documented
// path.
func TestInstallersCoverEveryReleasedPlatform(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	// Pull the target list out of the release recipe rather than hardcoding it,
	// so adding a platform to the Makefile makes this test demand installer
	// support for it.
	relIdx := strings.Index(makefile, "## release:")
	if relIdx < 0 {
		t.Fatal("no release target in the Makefile")
	}
	rest := makefile[relIdx:]
	if end := strings.Index(rest, "## checksums:"); end > 0 {
		rest = rest[:end]
	}

	targets := regexp.MustCompile(`\b(darwin|linux|windows|freebsd)/(amd64|arm64)\b`).
		FindAllStringSubmatch(rest, -1)
	if len(targets) == 0 {
		t.Fatal("could not read the platform list out of the release target")
	}

	sh := readRepoFile(t, "install.sh")
	ps := readRepoFile(t, "install.ps1")

	seen := map[string]bool{}
	for _, m := range targets {
		os_, arch := m[1], m[2]
		key := os_ + "/" + arch
		if seen[key] {
			continue
		}
		seen[key] = true

		t.Run(key, func(t *testing.T) {
			if os_ == "windows" {
				// install.ps1 maps PROCESSOR_ARCHITECTURE to windows-<arch>.
				if !strings.Contains(ps, "'windows-"+arch+"'") {
					t.Errorf("install.ps1 cannot select windows-%s, but the release publishes it", arch)
				}
				return
			}
			// install.sh normalises uname output to these names.
			if !strings.Contains(sh, os_) {
				t.Errorf("install.sh does not accept the OS %q, but the release publishes it", os_)
			}
			if !strings.Contains(sh, "arch="+arch) {
				t.Errorf("install.sh does not normalise anything to %q, but the release publishes it", arch)
			}
		})
	}
}

// TestReleaseWritesChecksums: both installers verify against checksums.txt and
// warn loudly when it is missing. If the release stops publishing it, every
// install silently degrades to unverified.
func TestReleaseWritesChecksums(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	if !strings.Contains(makefile, "checksums.txt") {
		t.Fatal("the release no longer produces checksums.txt, which both installers verify against")
	}
	// The release target must actually invoke it, not merely define it.
	relIdx := strings.Index(makefile, "## release:")
	rest := makefile[relIdx:]
	if end := strings.Index(rest, "## cross:"); end > 0 {
		rest = rest[:end]
	}
	if !strings.Contains(rest, "checksums") {
		t.Error("the release target does not generate checksums")
	}

	for _, f := range []string{"install.sh", "install.ps1"} {
		if !strings.Contains(readRepoFile(t, f), "checksums.txt") {
			t.Errorf("%s does not verify against checksums.txt", f)
		}
	}
}

// TestInstallShIsPortable guards the properties that make the script run under
// dash and ash, not only under the bash a developer happens to test with.
func TestInstallShIsPortable(t *testing.T) {
	sh := readRepoFile(t, "install.sh")

	// Check code, not prose. Comments in this script legitimately contain the
	// words "local" and "function", and a test that fires on those trains you
	// to ignore it.
	var code strings.Builder
	for _, line := range strings.Split(sh, "\n") {
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		code.WriteString(line)
		code.WriteString("\n")
	}
	body := code.String()

	// bashisms that dash rejects outright.
	for _, bad := range []struct {
		pattern *regexp.Regexp
		why     string
	}{
		{regexp.MustCompile(`\[\[\s`), "[[ ]] is a bashism; use [ ]"},
		{regexp.MustCompile(`(?m)^\s*function\s+\w`), "the `function` keyword is a bashism"},
		{regexp.MustCompile(`(?m)^\s*local\s+\w`), "`local` is not in POSIX sh"},
		{regexp.MustCompile(`\[\s[^]]*==`), "== inside [ ] is a bashism; use ="},
		{regexp.MustCompile(`\$\{\w+\s*(\^|,,)`), "case-conversion expansion is a bashism"},
		{regexp.MustCompile(`\becho\s+-e\b`), "echo -e is not portable; use printf"},
	} {
		if m := bad.pattern.FindString(body); m != "" {
			t.Errorf("install.sh contains %q: %s", strings.TrimSpace(m), bad.why)
		}
	}

	// The script must never exit 0 after failing — a silent failure leaves the
	// user believing succubus is installed.
	if !strings.Contains(sh, "trap cleanup EXIT") {
		t.Error("install.sh has no exit trap, so a mid-script failure could be reported as success")
	}
	if !strings.Contains(sh, "set -eu") {
		t.Error("install.sh must run under `set -eu`")
	}
}

// TestVersionLdflagTargetsTheRealSymbol guards a failure mode that is invisible
// at build time.
//
// The Makefile shipped `-X main.version=...` while no such variable existed.
// The linker does not complain about an -X flag naming a symbol that is not
// there — it silently drops it — so every release binary reported the hardcoded
// string instead of its tag, and the CLI and the MCP handshake disagreed about
// what was running.
func TestVersionLdflagTargetsTheRealSymbol(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	const want = "-X github.com/enowdev/succubus/internal/mode.Version="
	if !strings.Contains(makefile, want) {
		t.Errorf("LDFLAGS does not set %s; a version injected into a symbol that "+
			"does not exist is dropped without an error", want)
	}
	if strings.Contains(makefile, "-X main.version=") {
		t.Error("LDFLAGS still targets main.version, which does not exist")
	}

	// The default must not name a release: a binary built from an arbitrary
	// checkout should not claim to be a tagged version.
	if !strings.Contains(readRepoFile(t, "internal/mode/version.go"), `Version = "dev"`) {
		t.Error("the default Version should be \"dev\", not a release number")
	}
}
