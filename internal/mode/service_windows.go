//go:build windows

package mode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const runKeyName = "succubus"

// runKeyPath is the per-user autostart key. HKCU, so no administrator rights.
const runKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

// startupCmdPath is the fallback for machines where policy blocks writing to
// the Run key: a .cmd in the user's Startup folder does the same job.
func startupCmdPath() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		home, _ := os.UserHomeDir()
		appdata = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appdata, "Microsoft", "Windows",
		"Start Menu", "Programs", "Startup", "succubus.cmd")
}

// launchCommand starts the daemon detached, with output appended to the log.
// `start ""` returns immediately so login is never held up, and /min keeps the
// console window out of the way.
func launchCommand(bin, addr, db, log string) string {
	return fmt.Sprintf(`start "" /min "%s" daemon --addr %s --db "%s" >> "%s" 2>&1`,
		bin, addr, db, log)
}

func serviceInstall(bin, addr, db string) error {
	if err := ensureStateDir(); err != nil {
		return err
	}
	addr, db = serviceEnv(addr, db)
	log := serviceLogPath()

	// A .cmd wrapper keeps the Run value short and the quoting sane — the
	// registry is a poor place for a long command line with nested quotes.
	cmdPath := startupHelperPath()
	if err := os.MkdirAll(filepath.Dir(cmdPath), 0o755); err != nil {
		return err
	}
	script := "@echo off\r\n" + launchCommand(bin, addr, db, log) + "\r\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o644); err != nil {
		return err
	}

	out, err := exec.Command("reg", "add", runKeyPath,
		"/v", runKeyName, "/t", "REG_SZ",
		"/d", `"`+cmdPath+`"`, "/f").CombinedOutput()
	if err != nil {
		// Policy can block HKCU\...\Run; the Startup folder is the fallback.
		fallback := startupCmdPath()
		if err2 := os.MkdirAll(filepath.Dir(fallback), 0o755); err2 == nil {
			if err2 = os.WriteFile(fallback, []byte(script), 0o644); err2 == nil {
				fmt.Printf("  ✓ startup entry installed\n    %s\n", fallback)
				startDetached(cmdPath)
				return nil
			}
		}
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}

	fmt.Printf("  ✓ autostart registered\n    %s\\%s\n", runKeyPath, runKeyName)
	startDetached(cmdPath)
	return nil
}

// startupHelperPath is the launcher script, kept beside the database rather
// than in the Startup folder so the registry entry can point at one place.
func startupHelperPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".succubus", "start-daemon.cmd")
}

// startDetached runs the launcher now, so install does not require a reboot.
func startDetached(cmdPath string) {
	exec.Command("cmd", "/c", "start", "", "/min", cmdPath).Start()
}

func serviceUninstall() error {
	exec.Command("reg", "delete", runKeyPath, "/v", runKeyName, "/f").Run()

	for _, p := range []string{startupCmdPath(), startupHelperPath()} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// autostartInstalled reports whether either autostart route is in place.
func autostartInstalled() bool {
	out, err := exec.Command("reg", "query", runKeyPath, "/v", runKeyName).CombinedOutput()
	if err == nil && strings.Contains(string(out), runKeyName) {
		return true
	}
	_, e := os.Stat(startupCmdPath())
	return e == nil
}

func serviceStatus() error {
	fmt.Println()
	defer fmt.Println()

	out, err := exec.Command("reg", "query", runKeyPath, "/v", runKeyName).CombinedOutput()
	installed := err == nil && strings.Contains(string(out), runKeyName)
	where := runKeyPath + `\` + runKeyName

	if !installed {
		if _, e := os.Stat(startupCmdPath()); e == nil {
			installed, where = true, startupCmdPath()
		}
	}
	return reportStatus(installed, where)
}
