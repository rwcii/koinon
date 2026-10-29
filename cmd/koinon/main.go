// Command koinon provides the Go daemon, its client and agent commands, and its
// installation, upgrade and import commands.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/launcher"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	if launcher.Families[args[0]] {
		o, err := launcher.Parse(args[0], args[1:])
		if err != nil {
			return err
		}
		return launcher.Run(ctx, o, out)
	}
	switch args[0] {
	case "setup", "guide", "hook":
		return agentCommand(ctx, args, in, out)
	case "register":
		return registerDeepSeek(ctx, args[1:], out)
	case "memory", "recover":
		return memoryCommand(ctx, args, in, out)
	case "work", "claim":
		return workCommand(ctx, args, out)
	case "library":
		return libraryCommand(ctx, args[1:], out)
	case "room":
		return roomCommand(ctx, args[1:], out)
	case "decision":
		return decisionCommand(ctx, args[1:], out)
	case "context":
		return contextCommand(ctx, args[1:], out)
	case "participant":
		return participantCommand(ctx, args[1:], out)
	case "sessions", "maintenance", "claims", "rooms":
		ops := map[string][]string{"sessions": {"list", "purge", "unpurge", "retire"}, "maintenance": {"sweep"},
			"claims": {"release"}, "rooms": {"prune", "delete"}}[args[0]]
		if len(args) < 2 || !slices.Contains(ops, args[1]) {
			return usageError{"invalid_request", "use koinon " + args[0] + " " + strings.Join(ops, ", ")}
		}
		if args[0] == "claims" || args[0] == "rooms" {
			return claimsRoomsCommand(ctx, args[0], args[1], args[2:], out)
		}
		return cleanupCommand(ctx, args[0], args[1], args[2:], out)
	case "inbox":
		if len(args) > 1 && args[1] == "clear" {
			return cleanupCommand(ctx, "inbox", "clear", args[2:], out)
		}
	case "dashboard":
		return dashboardCommand(ctx, args[1:], out)
	case "maintainer":
		return maintainerCommand(ctx, args[1:], in, out)
	case "version":
		return versionCommand(out)
	case "install", "uninstall", "import", "upgrade":
		return lifecycleCommand(ctx, args, in, out)
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	var listen, listen6, address, as *string
	var after, limit, participant *int64
	var lockWait *time.Duration
	var maintainer maintainerFlags
	positional := 0
	switch args[0] {
	case "mcp":
		// An agent starts this server; a launched agent passes the launcher's state root
		// and daemon address through its environment.
		if v := os.Getenv("KOINON_STATE_DIR"); v != "" {
			flags.Set("state-dir", v)
		}
		address = addressFlag(flags)
		maintainer.add(flags)
	case "serve":
		// Without listen options, the daemon listens at the state directory's recorded port.
		listen = flags.String("listen", "", "IPv4 loopback listener")
		listen6 = flags.String("listen-v6", "", "IPv6 loopback listener")
		lockWait = flags.Duration("lock-wait", 0, "wait this long for the daemon lock, then exit with status 0")
	case "status", "peers", "peer-status", "send", "inbox", "ack", "cycle-status", "cycle-ready", "cycle-pickup":
		address = addressFlag(flags)
		if args[0] != "status" {
			as = flags.String("as", "", "calling session as FAMILY:ID")
		}
		positional = map[string]int{"peer-status": 1, "send": 2, "ack": 1, "cycle-ready": 3, "cycle-pickup": 5}[args[0]]
		if args[0] == "inbox" {
			after = flags.Int64("after", 0, "read messages after this sequence")
			limit = flags.Int64("limit", 50, "most messages to read")
			participant = flags.Int64("participant-after", -1, "read the held participant's messages after this sequence")
		}
		if args[0] == "ack" {
			participant = flags.Int64("participant", -1, "also acknowledge the held participant's inbox through this sequence")
		}
	default:
		return errors.New("unknown command; use koinon --help")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("invalid command options; use koinon --help")
	}
	if flags.NArg() != positional {
		return errors.New("wrong number of command arguments; use koinon --help")
	}
	if args[0] == "serve" {
		if *listen == "" || *listen6 == "" {
			port, err := core.RecordedPort(*state)
			if err != nil {
				return err
			}
			defaults := core.Listeners(port)
			if *listen == "" {
				*listen = defaults[0]
			}
			if *listen6 == "" {
				*listen6 = defaults[1]
			}
		}
		return serve(ctx, *state, []string{*listen, *listen6}, *lockWait, out)
	}
	given := addressGiven(flags)
	if err := resolveAddress(flags); err != nil {
		return err
	}
	if args[0] == "mcp" && maintainer.used(flags) {
		return maintainerMCP(ctx, *state, *address, followAddress(*state, given), maintainer, flags, in, out)
	}
	if args[0] == "mcp" {
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		// The replaced program's state is for this program only, never for its children.
		resume := os.Getenv(mcpResumeVariable)
		os.Unsetenv(mcpResumeVariable)
		changed, replace := mcpReplacement()
		return mcp.Serve(ctx, mcp.Config{StateDir: *state, Address: *address, Resolve: followAddress(*state, given), Getenv: os.Getenv,
			ParentPID: os.Getppid(), Command: platform.ProcessCommand, Directory: directory, Now: time.Now,
			TmuxSession: mcp.TmuxSessionName,
			Tmux:        mcp.RunTmux, Parents: platform.ProcessParents, TmuxExecutable: mcp.LookTmux,
			Version: version, Changed: changed, Replace: replace, Resume: []byte(resume), Stderr: os.Stderr}, in, out)
	}
	return call(ctx, args[0], *state, *address, as, after, limit, participant, flags.Args(), in, out)
}

// daemonLog receives the daemon's lifecycle lines; the service manager sends standard error
// to the log file or journal (#283). A variable only so that tests can read it.
var daemonLog io.Writer = os.Stderr

// lifecycleLog writes one lifecycle line to the writer that daemonLog held when the run
// began, so a run that ends late never writes into another run's log. Lines hold fixed codes,
// the version, the state directory and listener addresses: never a secret, message body or
// session ID.
func lifecycleLog() func(event string, fields map[string]any) {
	w := daemonLog
	var mu sync.Mutex
	return func(event string, fields map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		line := map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "event": event}
		for key, value := range fields {
			line[key] = value
		}
		json.NewEncoder(w).Encode(line)
	}
}

// serve runs the daemon. With lockWait, a daemon that finds the lock held retries every 2
// seconds for that long and then exits with status 0, so a service manager does not restart
// it in a loop while another daemon runs.
func serve(ctx context.Context, state string, listen []string, lockWait time.Duration, out io.Writer) error {
	logEvent := lifecycleLog()
	// The executable is recorded first, so an upgrade during the start is still seen.
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	changed := watchExecutable(watchCtx, launchPath(), executableCheck)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	stopped := func() {
		// The signal that canceled ctx reaches this channel too; give it a moment.
		select {
		case sig := <-signals:
			logEvent("stop", map[string]any{"reason": "signal", "signal": sig.String()})
		case <-time.After(100 * time.Millisecond):
			logEvent("stop", map[string]any{"reason": "canceled"})
		}
	}
	deadline := time.Now().Add(lockWait)
	waited := false
	var d *core.Daemon
	for {
		var err error
		d, err = core.Start(core.Config{StateDir: state, Listen: listen, Release: version, LogEvent: logEvent})
		if err == nil {
			break
		}
		if lockWait <= 0 || !platform.LockHeld(err) {
			logEvent("stop", map[string]any{"reason": "error", "code": "start_failed"})
			return err
		}
		if !waited {
			logEvent("lock_wait", map[string]any{"state_dir": state, "wait_seconds": lockWait.Seconds()})
			waited = true
		}
		if !time.Now().Before(deadline) {
			logEvent("stop", map[string]any{"reason": "lock_held"})
			return nil
		}
		select {
		case <-ctx.Done():
			stopped()
			return nil
		case <-time.After(min(2*time.Second, time.Until(deadline))):
		}
	}
	defer d.Close()
	if waited {
		logEvent("lock_acquired", nil)
	}
	logEvent("start", map[string]any{"version": version, "commit": commit, "state_dir": state, "listeners": d.Addresses()})
	ensurePickupSkills(logEvent)
	if err := json.NewEncoder(out).Encode(map[string]any{"ok": true, "daemon": "running", "listeners": d.Addresses()}); err != nil {
		logEvent("stop", map[string]any{"reason": "error", "code": "report_failed"})
		return err
	}
	select {
	case <-ctx.Done():
		stopped()
		return d.Close()
	case err := <-d.Errors():
		logEvent("stop", map[string]any{"reason": "error", "code": "listener_error"})
		return err
	case <-changed:
		logEvent("stop", map[string]any{"reason": "binary_changed"})
		if err := d.Close(); err != nil {
			return err
		}
		// A non-zero exit: the service manager starts the new binary.
		return errors.New("binary_changed: the daemon's executable was replaced; the service manager starts the new one")
	}
}

// ensurePickupSkills writes the managed cycle's koinon-pickup skill for the person the daemon
// runs as, when a file is missing or holds other content (#406): under the Claude Code
// configuration root of the daemon's environment ($CLAUDE_CONFIG_DIR, else ~/.claude), in
// ~/.agents/skills for Codex, in OpenCode's and agy's configuration directories when they
// exist (#461), and in the Grok home when it exists (#515). It runs at each start, so a brew upgrade, which restarts the
// daemon, writes it too. A failure is logged and never stops the daemon; the adapter's Probe
// then names the install step for that session.
func ensurePickupSkills(logEvent func(string, map[string]any)) {
	home, err := os.UserHomeDir()
	if err != nil {
		logEvent("pickup_skill", map[string]any{"result": "failed", "code": "home_unknown"})
		return
	}
	roots := core.SkillRoots(core.ClaudeConfigRoot(os.Getenv("CLAUDE_CONFIG_DIR"), home), home,
		core.OpenCodeConfigRoot(os.Getenv("XDG_CONFIG_HOME"), home), core.AgyConfigRoot(home), core.GrokHome(os.Getenv("GROK_HOME"), home))
	roots = slices.DeleteFunc(roots, func(root core.SkillRoot) bool { return !root.Present() })
	for _, root := range roots {
		path := root.Path(core.PickupSkillName)
		if _, err := core.InstallPickupSkill(path); err != nil {
			logEvent("pickup_skill", map[string]any{"family": root.Family, "path": path, "result": "failed", "code": "write_failed"})
		}
	}
	// DeepSeek's entry, in an existing harness home only (#514).
	if dsh := core.DeepSeekHome(os.Getenv("DSH_HOME"), home); dirExists(dsh) {
		path := core.DeepSeekPickupSkillPath(dsh)
		if _, err := core.InstallSkillFile(path, core.DeepSeekPickupSkill()); err != nil {
			logEvent("pickup_skill", map[string]any{"family": "deepseek", "path": path, "result": "failed", "code": "write_failed"})
		}
	}
	// The retired Python runtime's instructions leave the Codex AGENTS.md (#487).
	instructions := core.CodexInstructions(os.Getenv("CODEX_HOME"), home)
	if result, err := core.RemoveRetiredCodexBlocks(instructions); err != nil {
		logEvent("retired_instructions", map[string]any{"path": instructions, "result": "failed", "code": "write_failed"})
	} else if result == "removed" {
		logEvent("retired_instructions", map[string]any{"path": instructions, "result": "removed"})
	}
	// The other skill entries go to the same places (#453, #461).
	skills, err := core.TeamSkills()
	if err != nil {
		logEvent("team_skill", map[string]any{"result": "failed", "code": "catalog_unavailable"})
		return
	}
	for _, skill := range skills {
		for _, root := range roots {
			path := root.Path(skill.Name)
			if _, err := core.InstallTeamSkill(path, skill); err != nil {
				logEvent("team_skill", map[string]any{"family": root.Family, "path": path, "result": "failed", "code": "write_failed"})
			}
		}
	}
}

// launchPath is the path that started this process; a variable only so that tests can set it.
var launchPath = func() string { return os.Args[0] }

// executableCheck is how often the daemon checks its executable.
var executableCheck = 5 * time.Second

// watchExecutable closes the returned channel when path resolves to another regular
// executable file than at the start: brew upgrade moves the opt link to a new keg, and a
// copy install renames a new file over the old one (#397). A relative path, or one that does
// not resolve at the start, turns the check off; a missing or unreadable file later is not a
// change.
func watchExecutable(ctx context.Context, path string, every time.Duration) <-chan struct{} {
	changed := make(chan struct{})
	if !filepath.IsAbs(path) {
		return changed
	}
	start, err := os.Stat(path)
	if err != nil || !start.Mode().IsRegular() {
		return changed
	}
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if now, err := os.Stat(path); replacedExecutable(start, now, err) {
				close(changed)
				return
			}
		}
	}()
	return changed
}

// replacedExecutable reports whether the stat of an executable's path now shows another
// regular executable file than start.
func replacedExecutable(start, now os.FileInfo, err error) bool {
	return err == nil && now.Mode().IsRegular() && now.Mode().Perm()&0111 != 0 && !os.SameFile(start, now)
}

const usage = `Usage: koinon install [--prefix DIR] [--state-dir DIR] [--port N] [--agent FAMILY]... [--no-start] [--system]
       koinon install --plan [--prefix DIR] [--state-dir DIR]
       koinon uninstall [--prefix DIR] [--state-dir DIR] [--agent FAMILY]...
       koinon upgrade --from-python [--python-prefix DIR] [--prefix DIR] [--state-dir DIR] [--agent FAMILY]... [--repository KEY=PATH]... [--python PATH]
       koinon upgrade --status [--state-dir DIR]
       koinon import [--from DIR] [--python-prefix DIR] [--state-dir DIR] [--repository KEY=PATH]... [--verify]
       koinon version
       koinon serve [--state-dir DIR] [--listen 127.0.0.1:PORT] [--listen-v6 [::1]:PORT] [--lock-wait 60s]
       koinon register --as deepseek:ID [--directory DIR] [--repository DIR] [--dsh-url URL] [--dsh-credentials FILE] [--state-dir DIR] [--address HOST:PORT]
       koinon status [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon dashboard [--state-dir DIR] [--address 127.0.0.1:PORT] [--no-open]
       koinon maintainer secret set|generate|clear [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon mcp [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon mcp --maintainer --client NAME --secret-stdin|--secret-file PATH [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon setup <claude|codex|agy|opencode|grok|deepseek> [--cli ABS_PATH] [--binary ABS_PATH]
       koinon setup claude --remove-status-line [--claude-config DIR] [--state-dir DIR]
       koinon setup agy --remove-status-line [--agy-config DIR] [--state-dir DIR]
       koinon setup grok --remove-status-line [--grok-home DIR] [--state-dir DIR]
       koinon guide --agent <claude|codex|agy|opencode|grok|deepseek>
       koinon hook agy-stop
       koinon hook codex-session-start
       koinon hook claude-status [--command COMMAND]
       koinon hook agy-status [--command COMMAND]
       koinon hook grok-status [--command COMMAND]
       koinon memory <status|sync|ack|record|recall> --as FAMILY:ID [--consumer KEY] [options]
       koinon library list --as FAMILY:ID [--after ITEM_ID] [--limit N]
       koinon library read ITEM_ID --as FAMILY:ID
       koinon library state [ITEM_ID ...] --as FAMILY:ID [--scope machine|project] [--directory DIR]
       koinon library decision ITEM_ID --as FAMILY:ID --decision adopted|declined --evidence REF
         --catalog-digest SHA256 --item-digest SHA256 --if-revision N [--pre-edit JSON] [--result JSON]
       koinon room list --as FAMILY:ID [--after ROOM_ID] [--limit N]
       koinon room members ROOM_ID --as FAMILY:ID [--after ORDINAL] [--limit N]
       koinon room post ROOM_ID BODY --as FAMILY:ID --key KEY --deadline UNIX_SECONDS
       koinon room read ROOM_ID --as FAMILY:ID [--after SEQ] [--limit N]
       koinon decision create --as FAMILY:ID --reference REF --title TITLE --kind decision|task|fyi --key KEY --deadline UNIX_SECONDS [--order N] [--where TEXT] [--agent-text TEXT] [--context TEXT] [--recommendation TEXT] [--command CMD]... [--recipient NAME]... [--no-commands] [--no-recipients]
       koinon decision edit --as FAMILY:ID --reference REF --if-revision N [field flags as for create]
       koinon decision list --as FAMILY:ID --filter waiting|active|returned|in_progress|archived|mine [--after ID] [--limit N]
       koinon decision get --as FAMILY:ID --reference REF
       koinon decision history --as FAMILY:ID --reference REF [--withdrawals] [--after ID] [--limit N]
       koinon decision pending --as FAMILY:ID [--after VERDICT_ID] [--limit N]
       koinon decision ack --as FAMILY:ID --verdict VERDICT_ID...
       koinon decision close --as FAMILY:ID --reference REF --if-revision N [--note TEXT] --key KEY --deadline UNIX_SECONDS
       koinon decision withdraw --as FAMILY:ID --reference REF --if-revision N --reason TEXT --key KEY --deadline UNIX_SECONDS
       koinon decision restore --as FAMILY:ID --reference REF [--if-revision N]
       koinon context show [--json] [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon context set FAMILY --mode observe|native_compaction|handoff_cycle --threshold PERCENT
       koinon context enable|disable NAME
       koinon context threshold NAME PERCENT|family
       koinon participant list [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon participant holder ADDRESS PEER [--if-revision N] [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon sessions list [NAME ...] [--state active|expired|retired|purge-marked] [--family FAMILY] [--inactive-for DURATION] [--json]
       koinon sessions purge|retire [NAME ...] [--state STATE] [--family FAMILY] [--inactive-for DURATION] [--yes]
       koinon sessions unpurge [NAME ...] [--state STATE] [--family FAMILY] [--inactive-for DURATION]
       koinon inbox clear [NAME ...] [--state STATE] [--family FAMILY] [--inactive-for DURATION] [--yes]
       koinon maintenance sweep [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon claims release [--held-by NAME]... [--yes]
       koinon rooms prune [ROOM_ID ...] [--yes]
       koinon rooms delete ROOM_ID [--yes]
       koinon work <create|list> --as FAMILY:ID [--consumer KEY] [options]
       koinon work checkout [--directory DIR]
       koinon work checkout <status|request> --as FAMILY:ID [--directory DIR] [options]
       koinon work <get|propose|edit|start|update|release|finish|report|accept|reject> WORK_ID --as FAMILY:ID [--consumer KEY] [options]
       koinon claim renew WORK_ID --as FAMILY:ID --claim-generation N --if-claim-revision N [--lease-seconds S]
       koinon recover [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon <claude|codex|agy|opencode|grok> [--state-dir DIR] [--address HOST:PORT] [--cli ABS_PATH] [--directory DIR] [--tmux-session NAME] [--] [CLI arguments...]
       koinon peers --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon peer-status --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT] NAME
       koinon send --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT] NAME BODY
       koinon inbox --as FAMILY:ID [--after SEQ] [--limit N] [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon ack --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT] SEQ
       koinon cycle-status --as deepseek:ID [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon cycle-ready --as deepseek:ID [--state-dir DIR] [--address 127.0.0.1:PORT] CYCLE_ID REVISION HANDOFF_FILE
       koinon cycle-pickup --as deepseek:ID [--state-dir DIR] [--address 127.0.0.1:PORT] CYCLE_ID REVISION HANDOFF_FILE resumed|paused REPORT
A BODY of - reads the message body from standard input; a REPORT of - reads the pickup report.`

// DeepSeek's MCP support remains unverified. The harness's command path registers
// its exact native session and wake metadata explicitly, without creating a session. The
// session runs the command itself, so the registration is its own call, which can take its
// participant as a managed cycle's created session (#514).
func registerDeepSeek(ctx context.Context, args []string, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("register", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	as := flags.String("as", "deepseek:"+os.Getenv("DSH_SESSION_ID"), "exact harness session")
	dir := flags.String("directory", directory, "session directory")
	repo := flags.String("repository", "", "selected repository")
	base := flags.String("dsh-url", os.Getenv("DSH_WEB_URL"), "harness loopback origin")
	credentialDefault := ""
	if home := os.Getenv("DSH_HOME"); home != "" {
		credentialDefault = filepath.Join(home, ".credentials.yaml")
	}
	credential := flags.String("dsh-credentials", credentialDefault, "private harness credential file")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("invalid register options")
	}
	family, id, ok := strings.Cut(*as, ":")
	if !ok || family != "deepseek" || id == "" || *base == "" || !filepath.IsAbs(*credential) {
		return errors.New("register requires an exact DeepSeek session, harness URL and absolute credential path")
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	wake, _ := json.Marshal(map[string]string{"dsh_url": *base, "dsh_credentials": *credential})
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	data, err := core.Call(ctx, *address, secret, "/v1/sessions/register", core.Registration{Family: family, ID: id, Directory: *dir, Repository: *repo, WakeTarget: wake,
		ToolCall: true})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// call runs one client command against the daemon's API and prints its JSON response.
func call(ctx context.Context, command, state, address string, as *string, after, limit, participant *int64, args []string, in io.Reader, out io.Writer) error {
	var caller core.Key
	if as != nil {
		family, id, found := strings.Cut(*as, ":")
		if !found || family == "" || id == "" {
			return errors.New("--as must name the calling session as FAMILY:ID")
		}
		caller = core.Key{Family: family, ID: id}
	}
	var path string
	var body any
	switch command {
	case "status":
		path = "/v1/status"
	case "peers":
		path, body = "/v1/peers", map[string]any{"caller": caller}
	case "peer-status":
		path, body = "/v1/peers/status", map[string]any{"caller": caller, "peer": args[0]}
	case "send":
		text := args[1]
		if text == "-" {
			data, err := io.ReadAll(io.LimitReader(in, 65537))
			if err != nil {
				return err
			}
			text = string(data)
		}
		path, body = "/v1/messages/send", map[string]any{"caller": caller, "to": args[0], "body": text}
	case "inbox":
		request := map[string]any{"caller": caller, "after": *after, "limit": *limit}
		if *participant >= 0 {
			request["participant_after"] = *participant
		}
		path, body = "/v1/inbox/read", request
	case "cycle-status", "cycle-ready", "cycle-pickup":
		// The own-cycle commands keep DeepSeek's explicit native guard (#196): the harness
		// session names itself, and its session is its epoch.
		if caller.Family != "deepseek" {
			return errors.New("the cycle commands are for an exact DeepSeek session: --as deepseek:ID")
		}
		if command == "cycle-status" {
			path, body = "/v1/cycles/status", map[string]any{"caller": caller}
			break
		}
		revision, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return errors.New("REVISION must be a number")
		}
		request := map[string]any{"caller": caller, "epoch": caller.ID, "cycle_id": args[0], "revision": revision, "artifact": args[2]}
		path = "/v1/cycles/ready"
		if command == "cycle-pickup" {
			report := args[4]
			if report == "-" {
				data, err := io.ReadAll(io.LimitReader(in, 1025))
				if err != nil {
					return err
				}
				report = string(data)
			}
			request["outcome"], request["report"], path = args[3], report, "/v1/cycles/pickup"
		}
		body = request
	case "ack":
		through, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return errors.New("SEQ must be a sequence number")
		}
		request := map[string]any{"caller": caller, "through": through}
		if *participant >= 0 {
			request["participant_through"] = *participant
		}
		path, body = "/v1/inbox/ack", request
	}
	secret, err := core.ClientSecret(state, address)
	if err != nil {
		return err
	}
	result, err := core.Call(ctx, address, secret, path, body)
	if command == "status" && errors.Is(err, core.ErrUnavailable) {
		return daemonDown(ctx, state, address)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(result))
	return err
}

// memoryCommand runs the memory operations and storage recovery through the daemon's API.
func memoryCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	op, rest := "", args[1:]
	if args[0] == "memory" {
		if len(args) < 2 {
			return errors.New("use koinon memory <status|sync|ack|record|recall>")
		}
		op, rest = args[1], args[2:]
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	as := flags.String("as", "", "calling session as FAMILY:ID")
	consumer := flags.String("consumer", "", "stable consumer key")
	body := map[string]any{}
	texts := map[string]*string{}
	numbers := map[string]*float64{}
	integers := map[string]*int64{}
	positional := 0
	switch op {
	case "":
	case "status":
		texts["after"] = flags.String("after", "", "continue after this consumer")
	case "sync":
		texts["snapshot-id"] = flags.String("snapshot-id", "", "snapshot to continue")
		integers["page-token"] = flags.Int64("page-token", 0, "page token to continue from")
	case "ack":
		texts["snapshot-id"] = flags.String("snapshot-id", "", "snapshot to acknowledge")
		integers["through"] = flags.Int64("through", 0, "delta cursor to acknowledge through")
	case "record":
		positional = 1
		texts["type"] = flags.String("type", "finding", "entry type")
		for _, name := range []string{"scope", "scope-target", "path", "author", "key"} {
			texts[name] = flags.String(name, "", name)
		}
		integers["supersedes"] = flags.Int64("supersedes", 0, "entry this one replaces")
		integers["revokes"] = flags.Int64("revokes", 0, "entry this one withdraws")
		numbers["expires"] = flags.Float64("expires", 0, "absolute expiry, epoch seconds")
		numbers["deadline"] = flags.Float64("deadline", 0, "retry deadline, epoch seconds")
	case "recall":
		positional = 1
		integers["before"] = flags.Int64("before", 0, "continue before this sequence")
	default:
		return errors.New("unknown memory operation")
	}
	if err := flags.Parse(rest); err != nil || flags.NArg() != positional {
		return errors.New("invalid memory options; use koinon --help")
	}
	path := "/v1/storage/recover"
	if op != "" {
		path = "/v1/memory/" + op
		family, id, found := strings.Cut(*as, ":")
		if !found || family == "" || id == "" {
			return errors.New("--as must name the calling session as FAMILY:ID")
		}
		body["caller"] = core.Key{Family: family, ID: id}
		if *consumer != "" {
			body["consumer"] = *consumer
		}
		// Only options that were given are sent, so absent and zero stay distinct.
		flags.Visit(func(f *flag.Flag) {
			name := strings.ReplaceAll(f.Name, "-", "_")
			if value, ok := texts[f.Name]; ok {
				body[name] = *value
			}
			if value, ok := numbers[f.Name]; ok {
				body[name] = *value
			}
			if value, ok := integers[f.Name]; ok {
				body[name] = *value
			}
		})
		if op == "record" {
			body["body"] = flags.Arg(0)
			if _, given := body["type"]; !given {
				body["type"] = "finding"
			}
		}
		if op == "recall" {
			body["query"] = flags.Arg(0)
		}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	result, err := core.Call(ctx, *address, secret, path, body)
	if op == "" && errors.Is(err, core.ErrUnavailable) {
		return daemonDown(ctx, *state, *address)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(result))
	return err
}

// unavailable is a status or recover call that found no daemon. It names the installed
// service mode and the command that restarts the daemon; Koinon runs neither (#283).
type unavailable struct {
	address, mode, command, note string
}

func (u unavailable) Error() string {
	if u.command != "" {
		return "no daemon answers at " + u.address + "; restart it with: " + u.command
	}
	return "no daemon answers at " + u.address
}

// newServices reads the service manager; a variable so that tests replace it.
var newServices = platform.NewServices

func daemonDown(ctx context.Context, state, address string) error {
	u := unavailable{address: address, mode: "manual"}
	// The installed service listens only at the state directory's recorded port.
	if core.InstalledAddress(state, address) {
		if s, err := newServices(); err == nil {
			u.mode, u.command, u.note = s.Restart(ctx)
		}
	}
	if u.mode == "manual" {
		u.note = core.StartDaemon(state, address)
	}
	return u
}

// agentCommand runs the commands that configure and guide agents.
func agentCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch args[0] {
	case "hook":
		if len(args) == 2 && args[1] == "codex-session-start" {
			setup.CodexSessionStart(in, setup.HookEnv{Getenv: os.Getenv, Now: time.Now, Parents: platform.ProcessParents})
			return nil
		}
		if len(args) != 2 || args[1] != "agy-stop" {
			return errors.New("unknown hook; use koinon hook agy-stop, agy-status, codex-session-start, claude-status or grok-status")
		}
		return setup.AgyStop(in, out, setup.HookEnv{Getenv: os.Getenv, Now: time.Now})
	case "guide":
		agent := flags.String("agent", "", "agent family")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *agent == "" {
			return errors.New("use koinon guide --agent FAMILY")
		}
		return setup.Guide(*agent, out)
	}
	if len(args) < 2 {
		return errors.New("use koinon setup FAMILY")
	}
	o := setup.Options{Family: args[1]}
	flags.StringVar(&o.CLI, "cli", "", "absolute agent CLI path")
	flags.StringVar(&o.Binary, "binary", "", "absolute koinon binary path")
	flags.StringVar(&o.AgyRoot, "agy-config", "", "agy global customization root")
	flags.StringVar(&o.Opencode, "opencode-config", "", "OpenCode global configuration directory")
	flags.StringVar(&o.ClaudeConfig, "claude-config", "", "Claude configuration directory")
	flags.StringVar(&o.GrokHome, "grok-home", "", "Grok home directory that holds config.toml")
	flags.StringVar(&o.StateDir, "state-dir", "", "private Go state directory")
	flags.BoolVar(&o.RemoveStatusLine, "remove-status-line", false, "restore the Claude, agy or Grok status line that setup replaced")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid setup options; use koinon --help")
	}
	report, err := setup.Run(ctx, o)
	if err != nil {
		return err
	}
	// At a terminal, offer the family's missing permission rules (#449).
	if ask := terminalAsk(in, os.Stderr); ask != nil && report.Permissions != nil && !o.RemoveStatusLine {
		result, err := setup.Offer(o, setup.Permissions, ask, os.Stderr)
		if err != nil {
			return err
		}
		report.Permissions = &result
		if result.Outcome == "added" {
			report.Changed = append(report.Changed, o.Family+" permission rules in "+result.SettingsFile)
		}
	}
	return json.NewEncoder(out).Encode(report)
}

func main() {
	// The status-line command returns the user's own command's output and exit status
	// unchanged, never a Koinon error report.
	if len(os.Args) >= 3 && os.Args[1] == "hook" && os.Args[2] == "claude-status" {
		os.Exit(setup.ClaudeStatus(os.Args[3:], os.Stdin, os.Stdout, os.Stderr, setup.HookEnv{Getenv: os.Getenv, Now: time.Now}))
	}
	if len(os.Args) >= 3 && os.Args[1] == "hook" && os.Args[2] == "agy-status" {
		os.Exit(setup.AgyStatus(os.Args[3:], os.Stdin, os.Stdout, os.Stderr, setup.HookEnv{Getenv: os.Getenv, Now: time.Now}))
	}
	if len(os.Args) >= 3 && os.Args[1] == "hook" && os.Args[2] == "grok-status" {
		os.Exit(setup.GrokStatus(os.Args[3:], os.Stdin, os.Stdout, os.Stderr, setup.HookEnv{Getenv: os.Getenv, Now: time.Now}))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		json.NewEncoder(os.Stderr).Encode(errorReport(err))
		os.Exit(1)
	}
}

// errorReport is the JSON report of a failed command, with its code. Startup errors must never
// include the authentication secret.
func errorReport(err error) map[string]any {
	code := "daemon_error"
	report := map[string]any{"ok": false, "detail": err.Error()}
	var refused core.RefusedError
	var usage usageError
	var down unavailable
	var local core.Refusal
	switch {
	case errors.As(err, &down):
		code, report["service"] = "daemon_unavailable", down.mode
		if down.command != "" {
			report["restart_command"] = down.command
		}
		if down.note != "" {
			report["note"] = down.note
		}
	case errors.As(err, &usage):
		code, report["error"] = usage.code, usage.message
		delete(report, "detail")
	case errors.As(err, &local):
		// A refusal of this process, such as decision record's (#536), keeps its code.
		code, report["error"] = local.Code, local.Message
		delete(report, "detail")
	case errors.As(err, &refused):
		code = refused.Code
		if len(refused.Details) > 0 {
			report["details"] = refused.Details
		}
	}
	report["code"] = code
	return report
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
