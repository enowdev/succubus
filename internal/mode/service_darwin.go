//go:build darwin

package mode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const launchLabel = "com.enowdev.succubus"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")
}

// plist is a launchd user agent. RunAtLoad starts it at login; KeepAlive brings
// it back if it exits, which is what makes this a background service rather
// than a one-shot launch.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>--addr</string>
    <string>%s</string>
    <string>--db</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`

func serviceInstall(bin, addr, db string) error {
	if err := ensureStateDir(); err != nil {
		return err
	}
	addr, db = serviceEnv(addr, db)
	path := launchAgentPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	log := serviceLogPath()
	body := fmt.Sprintf(plistTemplate, launchLabel, bin, addr, db, log, log)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}

	uid := strconv.Itoa(os.Getuid())
	target := "gui/" + uid
	// Replace any previous registration rather than stacking one on top.
	exec.Command("launchctl", "bootout", target+"/"+launchLabel).Run()

	if out, err := exec.Command("launchctl", "bootstrap", target, path).CombinedOutput(); err != nil {
		// Older macOS releases only have the legacy verbs.
		if out2, err2 := exec.Command("launchctl", "load", "-w", path).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl: %v: %s / %s", err, out, out2)
		}
	}
	exec.Command("launchctl", "kickstart", "-k", target+"/"+launchLabel).Run()

	fmt.Printf("  ✓ launchd agent installed\n    %s\n", path)
	return nil
}

func serviceUninstall() error {
	path := launchAgentPath()
	uid := strconv.Itoa(os.Getuid())

	exec.Command("launchctl", "bootout", "gui/"+uid+"/"+launchLabel).Run()
	exec.Command("launchctl", "unload", "-w", path).Run()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// autostartInstalled reports whether the login agent exists.
func autostartInstalled() bool {
	_, err := os.Stat(launchAgentPath())
	return err == nil
}

func serviceStatus() error {
	path := launchAgentPath()
	_, err := os.Stat(path)
	fmt.Println()
	defer fmt.Println()
	return reportStatus(err == nil, path)
}
