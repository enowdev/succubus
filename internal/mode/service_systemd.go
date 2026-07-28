//go:build linux || freebsd

package mode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const systemdUnit = "succubus.service"

func unitPath() string {
	// XDG_CONFIG_HOME wins where it is set; ~/.config is the fallback.
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "systemd", "user", systemdUnit)
}

// A systemd *user* unit, so no root and no sudo. Restart=on-failure keeps it
// alive; WantedBy=default.target is what starts it at login.
const unitTemplate = `[Unit]
Description=succubus — shared coordination for AI coding agents
Documentation=https://github.com/enowdev/succubus
After=network.target

[Service]
Type=simple
ExecStart=%s daemon --addr %s --db %s
Restart=on-failure
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`

func serviceInstall(bin, addr, db string) error {
	if err := ensureStateDir(); err != nil {
		return err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf(
			"systemctl not found — this system does not use systemd.\n"+
				"Run the daemon from your own init or session startup:\n    %s daemon", bin)
	}

	addr, db = serviceEnv(addr, db)
	path := unitPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	log := serviceLogPath()
	body := fmt.Sprintf(unitTemplate, bin, addr, db, log, log)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}

	run := func(args ...string) error {
		out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl --user %s: %v: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("daemon-reload"); err != nil {
		return err
	}
	if err := run("enable", systemdUnit); err != nil {
		return err
	}
	if err := run("restart", systemdUnit); err != nil {
		return err
	}

	fmt.Printf("  ✓ systemd user unit installed\n    %s\n", path)
	// Without lingering, the unit stops when the last session closes — which
	// defeats the point on a headless box.
	if out, err := exec.Command("loginctl", "show-user", os.Getenv("USER"),
		"--property=Linger").Output(); err == nil &&
		strings.Contains(string(out), "Linger=no") {
		fmt.Printf("    note: to keep it running after you log out, enable lingering:\n" +
			"      sudo loginctl enable-linger $USER\n")
	}
	return nil
}

func serviceUninstall() error {
	exec.Command("systemctl", "--user", "stop", systemdUnit).Run()
	exec.Command("systemctl", "--user", "disable", systemdUnit).Run()

	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

// autostartInstalled reports whether the user unit exists.
func autostartInstalled() bool {
	_, err := os.Stat(unitPath())
	return err == nil
}

func serviceStatus() error {
	path := unitPath()
	_, err := os.Stat(path)
	fmt.Println()
	defer fmt.Println()

	if err := reportStatus(err == nil, path); err != nil {
		return err
	}
	if out, e := exec.Command("systemctl", "--user", "is-active", systemdUnit).Output(); e == nil {
		fmt.Printf("  systemd: %s", string(out))
	}
	return nil
}
