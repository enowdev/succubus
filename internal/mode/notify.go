package mode

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/enowdev/succubus/internal/store"
)

// Notify manages outbound webhook targets.
//
// Worth being clear about the scope: webhooks reach *people*, not agents. An
// agent has no HTTP endpoint and no process running between its turns, so there
// is nothing to push to — it reads the room when its session next takes a turn.
// What this fixes is the other half: an unanswered question sitting for an hour
// because nobody happened to be looking at the dashboard.
func Notify(args []string) error {
	if len(args) == 0 {
		return notifyList()
	}

	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	format := fs.String("format", "", "json, slack, or discord (guessed from the URL if omitted)")
	events := fs.String("events", "", "comma-separated event types, or * for everything")
	fs.Parse(args[1:])
	rest := fs.Args()

	switch args[0] {
	case "add":
		if len(rest) == 0 {
			return errors.New("usage: succubus notify add <url> [--format slack] [--events a,b]")
		}
		url := rest[0]

		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		for _, w := range cfg.Webhooks {
			if w.URL == url {
				return fmt.Errorf("that URL is already configured")
			}
		}

		target := store.WebhookConfig{URL: url, Format: *format}
		if target.Format == "" {
			target.Format = guessFormat(url)
		}
		if *events != "" {
			for _, e := range strings.Split(*events, ",") {
				if e = strings.TrimSpace(e); e != "" {
					target.Events = append(target.Events, e)
				}
			}
		}

		cfg.Webhooks = append(cfg.Webhooks, target)
		if err := SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("added %s (%s)\n", redactURL(url), target.Format)
		fmt.Println("restart the daemon to pick it up:  succubus service restart")
		return nil

	case "remove", "rm":
		if len(rest) == 0 {
			return errors.New("usage: succubus notify remove <url>")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		kept := cfg.Webhooks[:0]
		found := false
		for _, w := range cfg.Webhooks {
			// Match on a prefix so you do not have to paste the whole secret.
			if w.URL == rest[0] || strings.HasPrefix(w.URL, rest[0]) {
				found = true
				continue
			}
			kept = append(kept, w)
		}
		if !found {
			return fmt.Errorf("no webhook matching %q", rest[0])
		}
		cfg.Webhooks = kept
		if err := SaveConfig(cfg); err != nil {
			return err
		}
		fmt.Println("removed. restart the daemon:  succubus service restart")
		return nil

	case "test":
		sess, err := OpenSession("", false)
		if err != nil {
			return fmt.Errorf("daemon not reachable — start it first")
		}
		if _, err := sess.Client.PostMessage(sess.Project.ID, "", store.MsgAnnounce, "",
			store.HumanAuthor, "succubus notification test — if you can read this, webhooks work.",
			nil); err != nil {
			return err
		}
		fmt.Println("posted a test message to the agent room; check your webhook target")
		return nil

	case "list":
		return notifyList()
	}

	return fmt.Errorf("unknown subcommand %q (add, remove, list, test)", args[0])
}

func notifyList() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Webhooks) == 0 {
		fmt.Println("No webhooks configured.")
		fmt.Println()
		fmt.Println("  succubus notify add https://hooks.slack.com/services/…")
		fmt.Println("  succubus notify add https://discord.com/api/webhooks/…")
		fmt.Println()
		fmt.Println("Webhooks notify you, not the agents — an agent reads the room on its")
		fmt.Println("next turn regardless. They are for questions that need a person.")
		return nil
	}

	fmt.Printf("%d webhook(s), from %s\n\n", len(cfg.Webhooks), ConfigPath())
	for _, w := range cfg.Webhooks {
		evs := "room.message, handoff, claim.denied, agent.left (default)"
		if len(w.Events) > 0 {
			evs = strings.Join(w.Events, ", ")
		}
		fmt.Printf("  %s\n    format: %s\n    events: %s\n\n", redactURL(w.URL), w.Format, evs)
	}
	return nil
}

// guessFormat picks a payload shape from the hostname, since Slack and Discord
// both want a specific field name rather than a generic body.
func guessFormat(url string) string {
	switch {
	case strings.Contains(url, "hooks.slack.com"):
		return "slack"
	case strings.Contains(url, "discord.com"), strings.Contains(url, "discordapp.com"):
		return "discord"
	}
	return "json"
}

// redactURL hides the secret that Slack and Discord carry in the path.
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return u[:i+3] + rest[:j] + "/…"
		}
	}
	return u
}
