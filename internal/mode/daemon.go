package mode

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/succubus/internal/httpapi"
	"github.com/enowdev/succubus/internal/store"
)

// DefaultPort is the daemon's fixed loopback port.
const DefaultPort = 7801

// DefaultAddr is where the CLI, hooks, and MCP bridge look for the daemon.
func DefaultAddr() string {
	if v := os.Getenv("SUCCUBUS_ADDR"); v != "" {
		return v
	}
	return fmt.Sprintf("127.0.0.1:%d", DefaultPort)
}

// SPAFS is set by main when the binary embeds a built dashboard.
var SPAFS fs.FS

// Daemon runs the HTTP API, the SSE hub, and the background janitor.
func Daemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	addr := fs.String("addr", DefaultAddr(), "listen address")
	dbPath := fs.String("db", "", "database path (default ~/.succubus/succubus.db)")
	dev := fs.Bool("dev", false, "enable permissive CORS for the Vite dev server")
	devURL := fs.String("dev-url", "http://127.0.0.1:5273", "dev server URL shown when no SPA is embedded")
	fs.Parse(args)

	path := *dbPath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}

	// Refuse to run two daemons against one database.
	unlock, err := acquireLock(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()

	st, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Optional settings — notification targets, mostly. A missing or malformed
	// file is not fatal: the daemon is more useful running than refusing to.
	cfg, err := LoadConfig()
	if err != nil {
		log.Printf("config: %v (continuing without it)", err)
		cfg = &Config{}
	}
	if len(cfg.Webhooks) > 0 {
		st.SetWebhooks(cfg.Webhooks)
		log.Printf("webhooks: %d target(s) configured", len(cfg.Webhooks))
	}

	srv := httpapi.New(st, *dev)
	if SPAFS != nil {
		srv.MountSPA(SPAFS)
	} else {
		srv.MountDevNotice(*devURL)
	}

	httpSrv := &http.Server{
		Addr:querySafe(*addr),
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE streams are long-lived and the handler clears
		// its own deadline anyway.
	}

	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()

	go janitor(ctx, st)

	// Opt-in: spawn a headless turn when an agent is named in the room, rather
	// than leaving the message until that session is next prompted.
	if cfg.AutoWake {
		log.Printf("auto-wake enabled (%s)", wakeableSummary())
		go newAutoWaker(st, time.Duration(cfg.AutoWakeDelaySec)*time.Second).Watch(ctx)
	}

	ln, err := net.Listen("tcp", httpSrv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", httpSrv.Addr, err)
	}

	log.Printf("succubus daemon on http://%s  (db: %s)", httpSrv.Addr, path)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutCtx)
}

// janitor expires lapsed leases and demotes silent agents. Claim correctness
// does not depend on it — claimUpsert steals expired leases on its own — so a
// missed tick is harmless.
func janitor(ctx context.Context, st *store.Store) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			st.ExpireClaims()
			st.SweepAgents()
		}
	}
}

// querySafe normalizes bare ports and host-only forms into a listen address.
func querySafe(addr string) string {
	if addr == "" {
		return DefaultAddr()
	}
	if !strings.Contains(addr, ":") {
		if _, err := strconv.Atoi(addr); err == nil {
			return "127.0.0.1:" + addr
		}
	}
	return addr
}

// acquireLock takes an O_EXCL lockfile so a second daemon cannot start against
// the same database. A lock whose PID is no longer alive is treated as stale.
func acquireLock(dir string) (func(), error) {
	lockPath := filepath.Join(dir, "daemon.lock")

	for attempt := range 2 {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) || attempt > 0 {
			return nil, fmt.Errorf("lock %s: %w", lockPath, err)
		}
		if alive, pid := lockHolderAlive(lockPath); alive {
			return nil, fmt.Errorf(
				"another succubus daemon is running (pid %d).\n"+
					"Stop it first, or remove %s if you are sure it is gone",
				pid, lockPath)
		}
		os.Remove(lockPath) // stale, retry once
	}
	return nil, errors.New("could not acquire daemon lock")
}

// lockHolderAlive reports whether the pid in the lockfile is still a running
// succubus daemon.
//
// Signal(0) alone is not enough: it succeeds for a zombie — a process that has
// exited but whose parent has not reaped it — which would leave the lock held
// by something that can never serve a request. It also succeeds for whatever
// unrelated program later inherits that pid. Both cases are ruled out by
// checking what the process actually is.
func lockHolderAlive(lockPath string) (bool, int) {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		return false, 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false, 0
	}
	if pid == os.Getpid() {
		return false, pid // our own stale lock from a previous crash
	}
	// Liveness checking is platform-specific: see process_unix.go and
	// process_windows.go.
	return processAlive(pid), pid
}
