package mode

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/enowdev/succubus/internal/client"
	"github.com/enowdev/succubus/internal/store"
)

// CLI dispatches the human/script-facing subcommands. Every command supports
// --json so hooks and scripts can consume structured output.
func CLI(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	all := fs.Bool("all", false, "apply to everything (release)")
	status := fs.String("status", "", "filter by status")
	assignee := fs.String("assignee", "", "filter by assignee name")
	title := fs.String("title", "", "title (create commands)")
	body := fs.String("body", "", "body markdown (create commands)")
	ttl := fs.Int64("ttl", store.DefaultLeaseTTLSec, "claim lease seconds")
	force := fs.Bool("force", false, "skip the confirmation prompt (forget)")
	fs.Parse(args)
	rest := fs.Args()

	sess, err := OpenSession("", needsIdentity(cmd))
	if err != nil {
		var down *client.ErrDaemonDown
		if errors.As(err, &down) {
			return fmt.Errorf("daemon not running — start it with: succubus daemon")
		}
		return err
	}
	c := sess.Client
	pid := sess.Project.ID

	switch cmd {
	case "whoami":
		if sess.AgentID == "" {
			fmt.Println("not registered in this project")
			return nil
		}
		if *asJSON {
			return emit(map[string]string{
				"agent_id": sess.AgentID, "name": sess.AgentName,
				"project_id": pid, "tool": sess.Tool, "session_key": sess.Key,
			})
		}
		fmt.Printf("You are %s (%s) in project %s\n", sess.AgentName, sess.Tool, sess.Project.DisplayName)
		return nil

	case "status":
		ctx, err := c.Context(pid, sess.AgentID)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(ctx)
		}
		fmt.Print(ctx.Text)
		return nil

	case "agents":
		agents, err := c.ListAgents(pid)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(agents)
		}
		tw := newTab()
		fmt.Fprintln(tw, "NAME\tTOOL\tSTATUS\tLAST SEEN")
		for _, a := range agents {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Name, a.Tool, a.Status, ago(a.LastHeartbeatAt))
		}
		return tw.Flush()

	case "tasks":
		tasks, err := c.ListTasks(pid, map[string]string{"status": *status, "assignee": *assignee})
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(tasks)
		}
		tw := newTab()
		fmt.Fprintln(tw, "ID\tSTATUS\tASSIGNEE\tTITLE")
		for _, t := range tasks {
			flag := ""
			if t.Blocked {
				flag = " (blocked)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s%s\n", short(t.ID), t.Status, dash(t.AssigneeName), t.Title, flag)
		}
		return tw.Flush()

	case "task":
		if len(rest) == 0 {
			return errors.New("usage: succubus task <create|done|claim> …")
		}
		return taskCmd(sess, rest, *title, *body, *asJSON)

	case "plans":
		plans, err := c.ListPlans(pid)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(plans)
		}
		tw := newTab()
		fmt.Fprintln(tw, "ID\tSTATUS\tTITLE")
		for _, p := range plans {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", short(p.ID), p.Status, p.Title)
		}
		return tw.Flush()

	case "plan":
		if *title == "" {
			return errors.New("usage: succubus plan --title T [--body B]")
		}
		p, err := c.CreatePlan(pid, *title, *body, "active", sess.AgentName)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(p)
		}
		fmt.Printf("created plan %s: %s\n", short(p.ID), p.Title)
		return nil

	case "claims":
		claims, err := c.ListClaims(pid)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(claims)
		}
		tw := newTab()
		fmt.Fprintln(tw, "PATH\tHOLDER\tEXPIRES IN")
		for _, cl := range claims {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", cl.Path, cl.AgentName, until(cl.ExpiresAt))
		}
		return tw.Flush()

	case "claim":
		if len(rest) == 0 {
			return errors.New("usage: succubus claim <path>…")
		}
		res, err := c.ClaimFiles(pid, sess.AgentID, sess.AgentName, "", rest, *ttl)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(res)
		}
		for _, r := range res.Results {
			if r.Granted {
				fmt.Printf("granted  %s\n", r.Path)
			} else {
				fmt.Printf("DENIED   %s — %s\n", r.Path, r.Reason)
			}
		}
		if !res.Granted {
			os.Exit(1)
		}
		return nil

	case "release":
		n, err := c.ReleaseFiles(pid, sess.AgentID, rest, *all)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(map[string]int64{"released": n})
		}
		fmt.Printf("released %d path(s)\n", n)
		return nil

	case "check":
		if len(rest) == 0 {
			return errors.New("usage: succubus check <path>…")
		}
		res, err := c.CheckFiles(pid, sess.AgentID, rest)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(res)
		}
		if res.Conflict {
			fmt.Printf("CONFLICT — held by %s\n", res.Holder)
			os.Exit(1)
		}
		fmt.Println("clear")
		return nil

	case "events":
		evs, err := c.RecentEvents(pid, 50)
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(evs)
		}
		for _, e := range evs {
			fmt.Printf("%s  %-18s %s\n", ago(e.CreatedAt), e.Type, dash(e.AgentName))
		}
		return nil

	case "projects":
		ps, err := c.ListProjects()
		if err != nil {
			return err
		}
		if *asJSON {
			return emit(ps)
		}
		tw := newTab()
		fmt.Fprintln(tw, "ID\tNAME\tPATH")
		for _, p := range ps {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.ID, p.DisplayName, p.RootPath)
		}
		return tw.Flush()

	case "forget":
		// Defaults to the project you are standing in, which is the common
		// case; an explicit id lets you clear one you are not inside.
		target := pid
		label := sess.Project.DisplayName
		if len(rest) > 0 {
			target = rest[0]
			label = target
			if ps, err := c.ListProjects(); err == nil {
				for _, p := range ps {
					if p.ID == target {
						label = p.DisplayName
						break
					}
				}
			}
		}
		if !*force && !confirmTTY(fmt.Sprintf(
			"Forget project %q? Its agents, plans, tasks, claims and room history are deleted.\n"+
				"Files in the repository are untouched.", label)) {
			fmt.Println("cancelled")
			return nil
		}
		if err := c.DeleteProject(target); err != nil {
			return err
		}
		fmt.Printf("forgot %s\n", label)
		return nil

	default:
		return fmt.Errorf("unknown command %q (try: succubus help)", cmd)
	}
}

func taskCmd(sess *Session, rest []string, title, body string, asJSON bool) error {
	c, pid := sess.Client, sess.Project.ID
	switch rest[0] {
	case "create":
		if title == "" && len(rest) > 1 {
			title = strings.Join(rest[1:], " ")
		}
		if title == "" {
			return errors.New("usage: succubus task create --title T")
		}
		t, err := c.CreateTask(pid, client.NewTask{Title: title, BodyMD: body})
		if err != nil {
			return err
		}
		if asJSON {
			return emit(t)
		}
		fmt.Printf("created task %s: %s\n", short(t.ID), t.Title)
		return nil

	case "claim":
		if len(rest) < 2 {
			return errors.New("usage: succubus task claim <id>")
		}
		t, err := c.ClaimTask(rest[1], sess.AgentID, sess.AgentName, false)
		if err != nil {
			return err
		}
		if asJSON {
			return emit(t)
		}
		fmt.Printf("%s now owns %s\n", sess.AgentName, t.Title)
		return nil

	case "done":
		if len(rest) < 2 {
			return errors.New("usage: succubus task done <id>")
		}
		done := store.StatusDone
		t, err := c.UpdateTask(rest[1], store.TaskPatch{Status: &done})
		if err != nil {
			return err
		}
		if asJSON {
			return emit(t)
		}
		fmt.Printf("done: %s\n", t.Title)
		return nil
	}
	return fmt.Errorf("unknown task subcommand %q", rest[0])
}

// needsIdentity reports whether a command should register an agent. Read-only
// commands must not create identities as a side effect of being run.
func needsIdentity(cmd string) bool {
	switch cmd {
	case "claim", "release", "task", "whoami", "status", "check":
		return true
	}
	return false
}

func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func newTab() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
}

func short(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func ago(ms int64) string {
	if ms == 0 {
		return "—"
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func until(ms int64) string {
	d := time.Until(time.UnixMilli(ms))
	if d <= 0 {
		return "expired"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
