package mode

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/enowdev/succubus/internal/client"
)

// Service installs, removes, and inspects the per-user autostart entry that
// keeps the daemon running in the background.
//
// Every platform uses its own native mechanism, and all three are *per user* —
// nothing here needs administrator rights, and nothing is written outside the
// user's own configuration:
//
//	macOS    launchd    ~/Library/LaunchAgents/com.enowdev.succubus.plist
//	Linux    systemd    ~/.config/systemd/user/succubus.service
//	Windows  registry   HKCU\...\CurrentVersion\Run  (falls back to a Startup .cmd)
func Service(args []string) error {
	if len(args) == 0 {
		return serviceUsage()
	}

	fs := flag.NewFlagSet("service", flag.ExitOnError)
	addr := fs.String("addr", "", "listen address for the installed service")
	db := fs.String("db", "", "database path for the installed service")
	fs.Parse(args[1:])

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}

	switch args[0] {
	case "install", "enable":
		if err := serviceInstall(bin, *addr, *db); err != nil {
			return err
		}
		fmt.Printf("\n  succubus will now start automatically when you log in.\n")
		fmt.Printf("  Logs: %s\n\n", serviceLogPath())

		// Prove it, rather than claiming it.
		fmt.Print("  Waiting for the daemon…")
		c := client.New(DefaultAddr(), 2*time.Second)
		for i := range 20 {
			if c.Health() == nil {
				fmt.Printf("\r  ✓ daemon is running at http://%s\n\n", DefaultAddr())
				return nil
			}
			if i == 19 {
				fmt.Printf("\r  ! the daemon has not answered yet.\n")
				fmt.Printf("    Check %s, or run `succubus daemon` in the foreground.\n\n",
					serviceLogPath())
			}
			time.Sleep(500 * time.Millisecond)
		}
		return nil

	case "uninstall", "disable", "remove":
		if err := serviceUninstall(); err != nil {
			return err
		}
		fmt.Println("succubus will no longer start automatically.")
		return nil

	case "status":
		return serviceStatus()

	case "restart":
		if err := serviceUninstall(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return serviceInstall(bin, *addr, *db)
	}

	return serviceUsage()
}

func serviceUsage() error {
	fmt.Print(`succubus service — run the daemon in the background, starting at login

  succubus service install [--addr host:port] [--db path]
  succubus service status
  succubus service restart
  succubus service uninstall

Uses launchd on macOS, a systemd user unit on Linux, and the per-user Run key
on Windows. No administrator rights are required on any of them.
`)
	return nil
}

// serviceEnv returns the values to bake into the service definition, falling
// back to whatever the current environment implies.
func serviceEnv(addr, db string) (string, string) {
	if addr == "" {
		addr = DefaultAddr()
	}
	if db == "" {
		if p, err := os.UserHomeDir(); err == nil {
			db = filepath.Join(p, ".succubus", "succubus.db")
		}
	}
	return addr, db
}

// serviceLogPath is where the background daemon's output is written. A service
// with nowhere to log is a service you cannot debug.
func serviceLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "succubus.log"
	}
	return filepath.Join(home, ".succubus", "daemon.log")
}

// ensureStateDir creates ~/.succubus so the service has somewhere to write.
func ensureStateDir() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(home, ".succubus"), 0o755)
}

// reportStatus prints whether the daemon is actually answering, which matters
// more than whether the unit file exists.
func reportStatus(installed bool, where string) error {
	if installed {
		fmt.Printf("  ✓ autostart installed\n    %s\n", where)
	} else {
		fmt.Printf("  – autostart not installed\n    install it with: succubus service install\n")
	}

	c := client.New(DefaultAddr(), 2*time.Second)
	if err := c.Health(); err == nil {
		fmt.Printf("  ✓ daemon responding at http://%s\n", DefaultAddr())
	} else {
		fmt.Printf("  ✗ daemon not responding at %s\n", DefaultAddr())
		if installed {
			fmt.Printf("    check %s\n", serviceLogPath())
		}
	}
	return nil
}
