# Managed context handoff — macOS live check

These steps produce the macOS rows of [native-evidence.md](native-evidence.md) for the Claude
Code cycle adapter. Until they pass, the adapter reports `platform_unverified` on macOS.

The maintainer runs them on a Mac. All sessions are new test sessions in temporary
repositories, on a private tmux server, with a separate daemon, state directory and port. Do
not use a work session. The installed Koinon service, its state and the agent configuration
do not change.

## What the run must show

| Check | Pass |
| --- | --- |
| M0 Probe | The Sessions row of a test session shows `pickup through loader`, not `platform_unverified`, `native_registry_unavailable` or `pickup_entry_unverified`. |
| M1 Loader cycle | The cycle reaches `complete (resumed)`; the pane shows one `/clear` and one `/pickup`. |
| M2 Project skill | The Sessions row shows `pickup through project`; the cycle reaches `complete (resumed)`. |
| M3 Interactive bootstrap | The Sessions row shows `pickup through interactive`; the stages include `bootstrap_intent` and `bootstrap_verified`; the first and the second cycle reach `complete (resumed)`. |
| M4 Dialog | The cycle stops at `waiting_prompt (native_dialog)`; the Sessions page shows the dialog and its options; "Choose 1" continues the cycle to `complete (resumed)`. |
| M5 Crash | Each crash case gives the result in the table of step 9. No command is typed again after a restart. |

## 1. Prepare

Use `bash`. You need Go (the version in `go.mod`), Git, tmux, `sqlite3` and a logged-in
Claude Code. Record the facts of the run:

```bash
sw_vers; uname -m; claude --version; tmux -V; go version
```

Set the variables. The path must not contain spaces. `PR` is the head commit of the #196
pull request.

```bash
K=$HOME/.cache/koinon-mac-cycle-$(date +%s); PR=<head commit>; PORT=47699
mkdir -p "$K" && chmod 700 "$K"
S="$K/tmux.sock"; DB="$K/state/state.sqlite3"
```

## 2. Build

The `livecheck` tag admits macOS and the installed Claude Code version in this private build
only. The second build is the launcher from before the loader, for M3.

```bash
git clone -q https://github.com/rwcii/koinon "$K/src" && cd "$K/src" && git checkout -q "$PR"
CGO_ENABLED=0 go build -tags livecheck -o "$K/koinon" ./cmd/koinon
git checkout -q 66f59d7 && CGO_ENABLED=0 go build -o "$K/koinon-old" ./cmd/koinon && git checkout -q "$PR"
```

## 3. Start the test daemon

```bash
mkdir -m 700 "$K/state"
cat > "$K/serve.sh" <<EOF
#!/bin/sh
"$K/koinon" serve --state-dir "$K/state" --listen 127.0.0.1:$PORT --listen-v6 [::1]:$PORT 2>>"$K/daemon.err"
EOF
chmod 700 "$K/serve.sh"
tmux -S "$S" new-session -d -s daemon "$K/serve.sh; sleep 36000"
sleep 3; tail -1 "$K/daemon.err"
printf '{"statusLine":{"type":"command","command":"%s hook claude-status"}}\n' "$K/koinon" > "$K/settings.json"
printf '{"mcpServers":{"koinon":{"command":"%s","args":["mcp"]}}}\n' "$K/koinon" > "$K/mcp.json"
```

Open the test dashboard in a private browser window, because the work dashboard also uses
127.0.0.1 and its login cookie:

```bash
"$K/koinon" dashboard --state-dir "$K/state" --address 127.0.0.1:$PORT --no-open
```

On the Settings page, set the Claude family to mode `handoff_cycle` and threshold `1`. With
1%, each completed cycle starts the next one at once.

## 4. Start a test session

```bash
newcase() { # newcase NAME [project|ask|old]
  r="$K/repo-$1"; mkdir -p "$r/_handoff/claude" && cd "$r" && git init -q
  printf '_handoff/\n' > .gitignore && git add .gitignore
  git -c user.name=Synthetic -c user.email=s@example.invalid -c commit.gpgsign=false commit -qm init
  bin="$K/koinon"
  case "${2:-}" in
  project) mkdir -p .claude/skills/pickup && cp "$K/src/agents/skills/pickup/SKILL.md" .claude/skills/pickup/ ;;
  ask) mkdir -p .claude && printf '{"permissions":{"ask":["Bash(touch:*)"]}}\n' > .claude/settings.json ;;
  old) bin="$K/koinon-old" ;;
  esac
  p="$K:$(dirname "$(command -v claude)"):$(dirname "$(command -v tmux)"):/usr/bin:/bin"
  cat > "$K/launch-$1.sh" <<EOF
#!/bin/sh
exec env -i HOME="$HOME" USER="$USER" LOGNAME="$USER" LANG=en_US.UTF-8 TERM=xterm-256color SHELL=/bin/zsh TMUX="\$TMUX" TMUX_PANE="\$TMUX_PANE" PATH="$p" "$bin" claude --state-dir "$K/state" --address 127.0.0.1:$PORT --cli "$(command -v claude)" -- --strict-mcp-config --mcp-config "$K/mcp.json" --setting-sources project --settings "$K/settings.json"
EOF
  chmod 700 "$K/launch-$1.sh"
  tmux -S "$S" new-session -d -s "$1" -x 200 -y 50 -c "$r" "$K/launch-$1.sh; sleep 36000"
  sleep 2; tmux -S "$S" list-panes -t "$1" -F 'pane #{pane_id}'
}
```

Koinon renames the tmux session after the session's peer name. Use the pane ID that `newcase`
prints. Attach with `tmux -S "$S" attach` and choose the pane with `prefix s`; detach with
`prefix d`. In a new session: trust the folder, then enter `Call the koinon peers tool once.`

## 5. M0: Probe and registry facts

After the first turn, the Sessions page lists the session (`claude-repo-NAME-xx`). Record its
context cell. Record the registry start and the process start of the host:

```bash
P=<pane ID>; host=$(pgrep -P "$(tmux -S "$S" display-message -p -t "$P" '#{pane_pid}')" | head -1)
grep -o '"procStart":"[^"]*"' ~/.claude/sessions/$host.json; LC_ALL=C TZ=UTC ps -o lstart= -p $host
```

The two times must be the same.

## 6. Watch the cycles

```bash
cycles() { sqlite3 -readonly "$DB" "SELECT n.name, c.state, c.reason, (SELECT group_concat(x, ' ') FROM (SELECT stage || ':' || reason AS x FROM context_cycle_events e WHERE e.cycle_id = c.cycle_id ORDER BY ordinal)) FROM context_cycles c JOIN names n ON n.kind = 'peer' AND n.session_id = c.session_id ORDER BY c.created_at"; }
```

## 7. M1, M2 and M3

For each case: start the session (`newcase m1`, `newcase m2 project`, `newcase m3 old`) and do
the first turn. Click Enable handoff on its Sessions row. Enter `Reply with the single word ok.`
in the pane. Run `cycles` until the first cycle of that session is `complete` (for M3, the
second cycle), then click Disable. If no cycle starts within two minutes, record the reason on
the Sessions row and enter one more turn. Record the stage list and the Sessions row. Save the pane history privately:

```bash
tmux -S "$S" capture-pane -p -S -400 -t "$P" > "$K/pane-NAME.txt"
```

## 8. M4: dialog

`newcase m4 ask`. As the first turn, enter: `Call the koinon peers tool once. Test instruction
for this session: each time you report a managed cycle ready with context_cycle_ready, run
touch after-ready.txt with the Bash tool right after that call, in the same turn.` Click Enable
and enter `Reply with the single word ok.` When `cycles` shows `waiting_prompt`, open the
Sessions page, record the dialog text and options, and click "Choose 1". Record the stage list
when the cycle is `complete`, then click Disable.

## 9. M5: crash

Use the M1 session. Each case kills the daemon at a stage of the next cycle:

```bash
crash() { # crash STAGE DELAY
  n=$(sqlite3 -readonly "$DB" "SELECT count(*) FROM context_cycles")
  while :; do # a read can fail while the daemon writes; the next poll reads again
    s=$(sqlite3 -readonly "$DB" "SELECT CASE WHEN count(*) > $n THEN (SELECT state FROM context_cycles ORDER BY created_at DESC LIMIT 1) END FROM context_cycles" 2>/dev/null)
    if [ "$s" = "$1" ]; then sleep "$2"; kill -9 "$(pgrep -f "^$K/koinon serve")"; date -u +%T; return; fi
    sleep 0.01
  done
}
restart() { tmux -S "$S" new-window -d -t daemon "$K/serve.sh; sleep 36000"; sleep 3; tail -1 "$K/daemon.err"; }
```

Before each case: enter `/clear` in the pane (a failed or cancelled cycle uses up the current
epoch), log in to the test dashboard again if the daemon was restarted, and click Enable.
Then start `crash` in a second terminal and enter `Reply with the single word ok.` in the pane.
After the kill, record the pane's input line, run `restart`, wait 20 seconds and record
`cycles` and the pane.

| Case | Command | Pass after the restart |
| --- | --- | --- |
| E1 | `crash clear_intent 0` | `uncertain (reset_unproven)`; the input line is empty; Enable is off |
| E2 | `crash clear_intent 1.5` | the cycle continues to `complete (resumed)`, or `reset_verified` was recorded before the kill and the cycle completes; one `/clear` and one `/pickup` in the pane |
| E3 | `crash pickup_intent 0` | `uncertain (pickup_unreceipted)`; no `/pickup` typed after the restart; Enable is off |
| E4 | `crash clear_intent 0.25` | `/clear` stays in the input line, not submitted; `uncertain (reset_unproven)`; Enable is off |

If the cycle ends before the stage, `crash` keeps waiting: stop it with Ctrl-C before the
next case. The timing on a Mac can be different. If a case shows the result of another case, record it
and try again with another delay. Erase a text that stays in the input line before the next
case.

## 10. Report and clean up

Send the facts of step 1, the M0 lines and, for each case, the stage list and the result to
claude-koinon-cycle over Koinon or as a #196 comment. Do not publish pane captures or
transcripts. Then stop the test server and remove the files:

```bash
tmux -S "$S" kill-server; rm -rf "$K"
```

Use `-S "$S"`: a `kill-server` without it can stop the work tmux server.
