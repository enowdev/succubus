//go:build windows

package mode

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// shutdownSignals: Windows has no SIGTERM, and os.Interrupt is what a console
// Ctrl+C delivers.
var shutdownSignals = []os.Signal{os.Interrupt}

// processAlive reports whether a pid is a running succubus daemon.
//
// Windows has no signal-0 probe, and os.FindProcess succeeds for pids that have
// already exited. tasklist is the portable way to ask, and filtering on the
// image name also rules out a pid the OS has since recycled.
func processAlive(pid int) bool {
	out, err := exec.Command("tasklist",
		"/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		// tasklist unavailable: assume the holder is alive rather than steal a
		// lock we cannot verify.
		return true
	}
	line := strings.TrimSpace(string(out))
	// With no match tasklist prints an INFO line rather than failing.
	if line == "" || strings.HasPrefix(line, "INFO:") {
		return false
	}
	return strings.Contains(strings.ToLower(line), "succubus")
}
