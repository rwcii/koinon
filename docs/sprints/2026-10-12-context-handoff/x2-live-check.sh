#!/bin/bash
# X2 of macos-live-check.md in one run: a Codex context_cycle_pickup approval in
# waiting_pickup shows on the Sessions page, and "Choose 1" completes the cycle.
#
# Usage: bash x2-live-check.sh [COMMIT]   (default: d491390, release v0.2.2)
#
# The script builds the private livecheck binary, starts a test daemon on its own port, state
# directory and tmux socket, sets the codex family to handoff_cycle at 1%, starts the approve
# case, enters its turns and clicks Enable. You click "Choose 1" in the browser when it asks.
# On exit it stops the test server and removes its files. The installed Koinon service, its
# state and the Codex configuration do not change.
set -u

PR=${1:-d491390}
PORT=${PORT:-47699}
K=$HOME/.cache/koinon-x2-$(date +%s)
S=$K/tmux.sock
DB=$K/state/state.sqlite3
JAR=$K/cookies
BASE=http://127.0.0.1:$PORT
P=
PEER=

say() { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }
pane() { [ -n "$P" ] && tmux -S "$S" capture-pane -p -t "$P" | grep -v '^[[:space:]]*$' | tail -"${1:-20}"; }
die() {
	say "STOP: $*"
	if [ -n "$P" ]; then
		say "Last lines of the Codex pane (for you; do not publish them):"
		pane 25
	fi
	exit 1
}
cleanup() {
	tmux -S "$S" kill-server 2>/dev/null
	rm -rf "$K"
	say "The test server is stopped and $K is removed."
}
trap cleanup EXIT
trap 'exit 130' INT TERM

for c in git go tmux sqlite3 curl codex; do
	command -v "$c" >/dev/null || die "$c is not on PATH"
done
curl -s -o /dev/null "$BASE/" && die "port $PORT is in use; run again with PORT=<free port>"
# open reaches the browser only from the Mac's desktop login: over SSH, or in a tmux server
# started over SSH, macOS refuses it with launchd error 125. Elsewhere the link is printed only.
desktop() { [ "$(uname -s)" = Darwin ] && [ "$(launchctl managername 2>/dev/null)" = Aqua ]; }

mkdir -p "$K" && chmod 700 "$K"
say "Facts of the run:"
{ sw_vers 2>/dev/null || uname -sr; uname -m; codex --version; tmux -V; go version; } | sed 's/^/    /'

say "Building the livecheck binary at $PR"
git clone -q https://github.com/rwcii/koinon "$K/src" && git -C "$K/src" checkout -q "$PR" || die "clone or checkout failed"
(cd "$K/src" && CGO_ENABLED=0 go build -tags livecheck -o "$K/koinon" ./cmd/koinon) || die "build failed"

say "Starting the test daemon on port $PORT"
mkdir -m 700 "$K/state"
cat >"$K/serve.sh" <<EOF
#!/bin/sh
"$K/koinon" serve --state-dir "$K/state" --listen 127.0.0.1:$PORT --listen-v6 [::1]:$PORT 2>>"$K/daemon.err"
EOF
chmod 700 "$K/serve.sh"
tmux -S "$S" new-session -d -s daemon "$K/serve.sh; sleep 36000"
for _ in $(seq 30); do
	"$K/koinon" status --state-dir "$K/state" --address 127.0.0.1:$PORT >/dev/null 2>&1 && break
	sleep 1
done
"$K/koinon" status --state-dir "$K/state" --address 127.0.0.1:$PORT >/dev/null 2>&1 || die "the test daemon did not start: $(tail -1 "$K/daemon.err" 2>/dev/null)"

token() { "$K/koinon" dashboard --state-dir "$K/state" --address 127.0.0.1:$PORT --no-open | grep -oE 'token=[0-9a-f]+'; }
page() { curl -s -b "$JAR" "$BASE/dashboard/$1"; }
# post ACTION FIELD=VALUE...: a dashboard action with the login cookie and CSRF token; prints
# the notice code of the redirect.
post() {
	local action=$1 args=() f
	shift
	for f in "$@"; do args+=(--data-urlencode "$f"); done
	curl -s -o /dev/null -w '%{redirect_url}' -b "$JAR" -H "Origin: $BASE" \
		--data-urlencode "csrf=$CSRF" "${args[@]}" "$BASE/dashboard/actions/$action" | grep -oE 'notice=[a-z_]+' | cut -d= -f2
}

curl -s -o /dev/null -c "$JAR" "$BASE/dashboard/login?$(token)"
CSRF=$(page settings | grep -oE 'name="csrf" value="[0-9a-f]{64}"' | head -1 | grep -oE '[0-9a-f]{64}')
[ -n "$CSRF" ] || die "the script could not log in to the test dashboard"
r=$(post context-family-settings family=codex mode=handoff_cycle threshold=1)
[ "$r" = context_policy_saved ] || die "Settings refused: ${r:-no answer}"
say "Settings: codex handoff_cycle at 1%"

say "Starting the Codex test session (approve case)"
R=$K/repo-x2
mkdir -p "$R/_handoff/codex" "$R/.agents/skills/pickup"
cp "$K/src/agents/skills/pickup/SKILL.md" "$R/.agents/skills/pickup/"
printf '_handoff/\n' >"$R/.gitignore"
git -C "$R" init -q && git -C "$R" add -A &&
	git -C "$R" -c user.name=Synthetic -c user.email=s@example.invalid -c commit.gpgsign=false commit -qm init || die "test repository failed"
path="$K:$(dirname "$(command -v codex)"):$(dirname "$(command -v tmux)"):/usr/bin:/bin"
cat >"$K/launch-x2.sh" <<EOF
#!/bin/sh
exec env -i HOME="$HOME" USER="$USER" LOGNAME="$USER" LANG=en_US.UTF-8 TERM=xterm-256color SHELL=/bin/zsh TMUX="\$TMUX" TMUX_PANE="\$TMUX_PANE" PATH="$path" "$K/koinon" codex --state-dir "$K/state" --address 127.0.0.1:$PORT --cli "$(command -v codex)" -- -a on-request -c approvals_reviewer="user" -c mcp_servers.koinon.default_tools_approval_mode="approve" -c mcp_servers.koinon.tools.context_cycle_pickup.approval_mode="prompt" -c 'projects={"$R"={trust_level="trusted"}}' -c 'mcp_servers.koinon.command="$K/koinon"'
EOF
chmod 700 "$K/launch-x2.sh"
tmux -S "$S" new-session -d -s x2 -x 200 -y 50 -c "$R" "$K/launch-x2.sh; sleep 36000"
P=$(tmux -S "$S" list-panes -t x2 -F '#{pane_id}')

# send TEXT: types one line into the Codex pane and submits it.
send() {
	tmux -S "$S" send-keys -t "$P" -l "$1"
	sleep 1
	tmux -S "$S" send-keys -t "$P" Enter
	say "Entered: $1"
}
# waitfor SECONDS COMMAND...: runs COMMAND every 2 seconds until it succeeds.
waitfor() {
	local end=$(($(date +%s) + $1))
	shift
	while [ "$(date +%s)" -lt "$end" ]; do
		"$@" && return 0
		sleep 2
	done
	return 1
}
codex_ready() { pane 60 | grep -q 'OpenAI Codex'; }
codex_asks() { pane 60 | grep -qE 'OpenAI Codex|Hooks need review'; }
waitfor 60 codex_asks || die "Codex did not start"
if pane 60 | grep -q 'Hooks need review'; then
	# Codex has not trusted the SessionStart hook of koinon setup codex. Continue without
	# trusting it, so that Codex's configuration does not change; the address then moves at
	# the new thread's first Koinon tool call (docs/INSTALL.md).
	tmux -S "$S" send-keys -t "$P" 3
	sleep 1
	pane 60 | grep -q 'Hooks need review' && tmux -S "$S" send-keys -t "$P" Enter
	say "Codex asked to review its SessionStart hook: continued without trusting it"
fi
waitfor 60 codex_ready || die "Codex did not start"
sleep 5
send '/status'
sleep 3
if pane 40 | grep -q 'Approve for me'; then die "/status shows \"Approve for me\"; the approve case needs approvals by you"; fi
say "/status: $(pane 40 | grep -oE 'Permissions:.*' | head -1)"
send 'Call the koinon peers tool once.'

row() { page sessions | grep -oE 'name="peer" value="codex-repo-x2-[^"]*"><input type="hidden" name="revision" value="[0-9]+"><input type="hidden" name="if_revision" value="[0-9]+"><input type="hidden" name="enabled" value="on">' | head -1; }
row_ready() { [ -n "$(row)" ] && page sessions | grep -q 'pickup through project'; }
waitfor 180 row_ready || die "the Sessions page shows no codex-repo-x2 row with pickup through project: $(page sessions | grep -oE 'cycle unavailable: [a-z_]+' | head -1)"
PEER=$(row | sed -E 's/.*name="peer" value="([^"]*)".*/\1/')
say "X0: $PEER shows pickup through project"
sleep 10 # let the first turn finish before the next one
# A renewal can change the session's revision between the read and the click; read again.
for _ in 1 2 3; do
	form=$(row)
	rev=$(printf '%s' "$form" | sed -E 's/.*name="revision" value="([0-9]+)".*/\1/')
	ifrev=$(printf '%s' "$form" | sed -E 's/.*name="if_revision" value="([0-9]+)".*/\1/')
	r=$(post context-enable peer="$PEER" revision="$rev" if_revision="$ifrev" enabled=on)
	[ "$r" = context_enable_saved ] && break
	sleep 2
done
[ "$r" = context_enable_saved ] || die "Enable refused: ${r:-no answer}"
say "Enable handoff: on for $PEER"
send 'Reply with the single word ok.'

latest() { sqlite3 -readonly "$DB" "SELECT state || CASE WHEN reason <> '' THEN ' (' || reason || ')' ELSE '' END FROM context_cycles ORDER BY created_at DESC LIMIT 1" 2>/dev/null; }
stages() { sqlite3 -readonly "$DB" "SELECT c.state, c.reason, (SELECT group_concat(x, ' ') FROM (SELECT stage || ':' || reason AS x FROM context_cycle_events e WHERE e.cycle_id = c.cycle_id ORDER BY ordinal)) FROM context_cycles c ORDER BY c.created_at" 2>/dev/null; }

say "Watching the cycle (up to 20 minutes; Ctrl-C stops and cleans up)"
last='' since=$(date +%s) shown='' asked='' end=$(($(date +%s) + 1200))
while [ "$(date +%s)" -lt "$end" ]; do
	s=$(latest)
	if [ -n "$s" ] && [ "$s" != "$last" ]; then
		say "cycle: $s"
		last=$s since=$(date +%s) shown=
	fi
	case $s in
	complete* | cancelled* | failed* | uncertain*) break ;;
	waiting_prompt*)
		if [ -z "$asked" ]; then
			asked=1
			link="$BASE/dashboard/login?$(token)"
			say "ACTION FOR YOU: the approval waits on the Sessions page."
			say "  Open this link within 60 seconds (it replaces the work dashboard login in this browser; both use 127.0.0.1):"
			say "  $link"
			say "  On the row $PEER, read the native dialog, then click \"Choose 1\"."
			if desktop; then
				open "$link"
			else
				say "  Open it in a browser on this machine's desktop; the test dashboard listens on loopback only."
			fi
			say "  An expired link? A new one: \"$K/koinon\" dashboard --state-dir \"$K/state\" --address 127.0.0.1:$PORT --no-open"
			page sessions | grep -oE 'native dialog:.*' | sed -E 's/<form.*//; s/<br>/ | /g; s/<[^>]+>//g; s/&#34;/"/g; s/&#39;/'"'"'/g; s/&lt;/</g; s/&gt;/>/g; s/&amp;/\&/g' | sed 's/^/    /'
		fi
		;;
	*)
		if [ -n "$s" ] && [ -z "$shown" ] && [ $(($(date +%s) - since)) -ge 120 ]; then
			shown=1
			say "No change for 2 minutes at $s. Last lines of the Codex pane (do not publish them):"
			pane 20 | sed 's/^/    /'
			say "Wake: $("$K/koinon" status --state-dir "$K/state" --address 127.0.0.1:$PORT | grep -oE '"wake":\{[^}]*\}[^}]*\}')"
		fi
		;;
	esac
	sleep 2
done

say "RESULT (paste this block):"
{
	echo "commit $PR; peer $PEER"
	stages
	case $(latest) in
	complete*) if stages | grep -q 'waiting_prompt'; then echo "X2: PASS"; else echo "X2: NOT EXERCISED (no waiting_prompt)"; fi ;;
	*) echo "X2: FAIL ($(latest))" ;;
	esac
} | sed 's/^/    /'
read -r -p "Press Enter to stop the test and delete its files. " _
