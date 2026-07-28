//go:build !windows

package mode

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// shutdownSignals are the signals that should stop the daemon cleanly.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// processAlive reports whether a pid is a running succubus daemon.
//
// Signal 0 alone is not enough: it succeeds for a zombie — a process that has
// exited but whose parent has not reaped it — and for whatever unrelated
// program later inherits that pid. Checking the process state and command name
// rules both out, so a crashed daemon cannot hold the lock forever.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On unix FindProcess always succeeds; signal 0 is the liveness probe.
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "state=,comm=").Output()
	if err != nil {
		// ps is missing or the process vanished mid-check: trust the signal
		// probe rather than stealing a lock we cannot verify.
		return true
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 {
		return false // no such process
	}
	if strings.HasPrefix(fields[0], "Z") {
		return false // zombie
	}
	return strings.Contains(fields[len(fields)-1], "succubus")
}
