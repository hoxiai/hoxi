package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/hoxiai/hoxi/internal/scheduler"
)

func runScheduleCommand(
	ctx context.Context,
	args []string,
	cwd string,
	defaultBridgeAddr string,
	bootHeadless HeadlessBootFunc,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printScheduleHelp(stdout)
		return 0
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	client, cleanup, err := ConnectOrSpawn(ctx, defaultBridgeAddr, cwd, bootHeadless)
	if err != nil {
		fmt.Fprintf(stderr, "Error connecting to Hoxi gateway: %v\n", err)
		return 1
	}
	defer cleanup()

	switch sub {
	case "list", "ls":
		return runScheduleList(ctx, client, rest, cwd, stdout, stderr)
	case "add", "create":
		return runScheduleAdd(ctx, client, rest, cwd, stdout, stderr)
	case "run":
		return runScheduleSimplePost(ctx, client, rest, "run", "Triggered scheduled run", stdout, stderr)
	case "stop":
		return runScheduleSimplePost(ctx, client, rest, "stop", "Stopped scheduled run", stdout, stderr)
	case "pause":
		return runScheduleSetEnabled(ctx, client, rest, false, stdout, stderr)
	case "resume", "enable":
		return runScheduleSetEnabled(ctx, client, rest, true, stdout, stderr)
	case "history", "runs":
		return runScheduleHistory(ctx, client, rest, stdout, stderr)
	case "remove", "rm", "delete":
		return runScheduleDelete(ctx, client, rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unknown schedule subcommand %q\n", sub)
		printScheduleHelp(stderr)
		return 1
	}
}

func printScheduleHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: hoxi schedule <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  list [--all] [--dir <path>]           List scheduled tasks")
	fmt.Fprintln(w, "  add --name <n> --prompt <p> ...       Create a scheduled task")
	fmt.Fprintln(w, "  run <id>                              Trigger a scheduled task immediately")
	fmt.Fprintln(w, "  stop <id>                             Abort an active scheduled run")
	fmt.Fprintln(w, "  pause <id>                            Pause a scheduled task")
	fmt.Fprintln(w, "  resume <id>                           Resume a paused scheduled task")
	fmt.Fprintln(w, "  history <id>                          Show recent runs for a scheduled task")
	fmt.Fprintln(w, "  remove <id>                           Delete a scheduled task")
}

func runScheduleList(ctx context.Context, client *Client, args []string, cwd string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi schedule list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var all bool
	var dir string
	fs.BoolVar(&all, "all", false, "list schedules across all workspaces")
	fs.StringVar(&dir, "dir", cwd, "filter by workspace directory")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	endpoint := "/api/hoxi/schedules"
	if !all && strings.TrimSpace(dir) != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		endpoint += "?directory=" + url.QueryEscape(dir)
	}
	var items []scheduler.Schedule
	if err := client.doJSON(ctx, http.MethodGet, endpoint, nil, &items); err != nil {
		fmt.Fprintf(stderr, "Failed to list schedules: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "No scheduled tasks found.")
		return 0
	}
	for _, item := range items {
		state := "paused"
		if item.Running {
			state = "running"
		} else if item.Enabled {
			state = "enabled"
		}
		trig := item.Trigger.Cron
		if item.Trigger.Kind == scheduler.TriggerKindInterval {
			trig = fmt.Sprintf("every %dm", item.Trigger.IntervalMinutes)
		}
		scope := "fresh-session"
		if item.SessionID != "" {
			scope = "session:" + item.SessionID
		}
		nextStr := "-"
		if item.NextRunAt > 0 {
			nextStr = time.UnixMilli(item.NextRunAt).Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(stdout, "%s  [%-7s]  %-24s  %-14s  %-18s  next=%s  last=%s\n",
			item.ID, state, item.Name, trig, scope, nextStr, firstNonEmpty(item.LastOutcome, "-"))
	}
	return 0
}

func runScheduleAdd(ctx context.Context, client *Client, args []string, cwd string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hoxi schedule add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		name           string
		prompt         string
		cronExpr       string
		intervalMins   int
		dir            string
		sessionID      string
		mode           string
		sandboxMode    string
		commandSandbox bool
		runtimeName    string
		providerID     string
		modelID        string
		skillName      string
		skillKey       string
		notifyOn       string
	)
	fs.StringVar(&name, "name", "", "schedule name")
	fs.StringVar(&prompt, "prompt", "", "prompt text to execute")
	fs.StringVar(&cronExpr, "cron", "", "5-field cron expression (e.g. '0 9 * * 1-5')")
	fs.IntVar(&intervalMins, "interval", 0, "interval in minutes (>= 5)")
	fs.StringVar(&dir, "dir", cwd, "workspace directory")
	fs.StringVar(&sessionID, "session", "", "optional existing session ID to bind")
	fs.StringVar(&mode, "mode", "", "conversation mode: build | plan (empty inherits bound session or defaults to build)")
	fs.StringVar(&sandboxMode, "sandbox", "local", "sandbox mode for fresh sessions: local | worktree")
	fs.BoolVar(&commandSandbox, "command-sandbox", false, "enable OS command sandbox for fresh sessions")
	fs.StringVar(&runtimeName, "runtime", "native", "agent runtime")
	fs.StringVar(&providerID, "provider", "", "explicit provider ID")
	fs.StringVar(&modelID, "model", "", "explicit model ID")
	fs.StringVar(&skillName, "skill", "", "optional skill name")
	fs.StringVar(&skillKey, "skill-key", "", "optional skill key")
	fs.StringVar(&notifyOn, "notify", scheduler.NotifyAlertOrError, "alert_or_error | always | never")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	trig := scheduler.TriggerSpec{
		Cron:            strings.TrimSpace(cronExpr),
		IntervalMinutes: intervalMins,
	}
	if trig.Cron != "" {
		trig.Kind = scheduler.TriggerKindCron
	} else if trig.IntervalMinutes > 0 {
		trig.Kind = scheduler.TriggerKindInterval
	}
	var skill *scheduler.SkillRef
	if strings.TrimSpace(skillName) != "" || strings.TrimSpace(skillKey) != "" {
		skill = &scheduler.SkillRef{Key: strings.TrimSpace(skillKey), Name: strings.TrimSpace(skillName)}
	}
	payload := scheduler.UpsertInput{
		Name:      name,
		Directory: dir,
		SessionID: sessionID,
		Trigger:   trig,
		Execution: scheduler.ExecutionSpec{
			Mode:           mode,
			SandboxMode:    sandboxMode,
			CommandSandbox: commandSandbox,
			Runtime:        runtimeName,
			ProviderID:     providerID,
			ModelID:        modelID,
		},
		SkillSelection: skill,
		Prompt:         prompt,
		NotifyOn:       notifyOn,
	}
	var created scheduler.Schedule
	if err := client.doJSON(ctx, http.MethodPost, "/api/hoxi/schedules", payload, &created); err != nil {
		fmt.Fprintf(stderr, "Failed to create schedule: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Created schedule %s (%s)\n", created.ID, created.Name)
	return 0
}

func runScheduleSetEnabled(ctx context.Context, client *Client, args []string, enabled bool, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "Schedule ID is required.")
		return 1
	}
	id := strings.TrimSpace(args[0])
	var updated scheduler.Schedule
	if err := client.doJSON(ctx, http.MethodPatch, "/api/hoxi/schedules/"+url.PathEscape(id), scheduler.PatchInput{Enabled: &enabled}, &updated); err != nil {
		fmt.Fprintf(stderr, "Failed to update schedule: %v\n", err)
		return 1
	}
	verb := "Paused"
	if enabled {
		verb = "Resumed"
	}
	fmt.Fprintf(stdout, "%s schedule %s (%s)\n", verb, updated.ID, updated.Name)
	return 0
}

func runScheduleSimplePost(ctx context.Context, client *Client, args []string, action, verb string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "Schedule ID is required.")
		return 1
	}
	id := strings.TrimSpace(args[0])
	if err := client.doJSON(ctx, http.MethodPost, "/api/hoxi/schedules/"+url.PathEscape(id)+"/"+action, map[string]any{}, nil); err != nil {
		fmt.Fprintf(stderr, "Failed to %s schedule: %v\n", action, err)
		return 1
	}
	fmt.Fprintf(stdout, "%s: %s\n", verb, id)
	return 0
}

func runScheduleHistory(ctx context.Context, client *Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "Schedule ID is required.")
		return 1
	}
	id := strings.TrimSpace(args[0])
	var runs []scheduler.RunRecord
	if err := client.doJSON(ctx, http.MethodGet, "/api/hoxi/schedules/"+url.PathEscape(id)+"/runs", nil, &runs); err != nil {
		fmt.Fprintf(stderr, "Failed to read schedule history: %v\n", err)
		return 1
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "No runs recorded yet.")
		return 0
	}
	for _, r := range runs {
		ts := time.UnixMilli(r.StartedAt).Local().Format("2006-01-02 15:04:05")
		msg := firstNonEmpty(r.Summary, r.Error, "-")
		fmt.Fprintf(stdout, "%s  %s  [%-12s]  %-9s  session=%-18s  %s\n", r.ID, ts, r.Status, r.Trigger, firstNonEmpty(r.SessionID, "-"), msg)
	}
	return 0
}

func runScheduleDelete(ctx context.Context, client *Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "Schedule ID is required.")
		return 1
	}
	id := strings.TrimSpace(args[0])
	if err := client.doJSON(ctx, http.MethodDelete, "/api/hoxi/schedules/"+url.PathEscape(id), nil, nil); err != nil {
		fmt.Fprintf(stderr, "Failed to delete schedule: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Deleted schedule %s\n", id)
	return 0
}
