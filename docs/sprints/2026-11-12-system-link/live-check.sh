#!/bin/sh
# Live check of #670 chunk 03 (card LINK-670-LIVE): a temporary second Koinon, built from one
# commit, with its own state directory and its own ports. The installed Koinon, its store and
# the agents' own MCP configuration do not change. See verification.md.
#
#   live-check.sh build SHA     build the commit into DIR/bin (needs Go)
#   live-check.sh start NAME    start the daemon, name this computer NAME, turn Remote on
#   live-check.sh invite        print a one-time invitation
#   live-check.sh join          paste the other computer's invitation
#   live-check.sh status        link, queue and Remote status
#   live-check.sh agent FAMILY  start a claude or codex agent on the temporary daemon
#   live-check.sh stop          turn Remote off, stop the daemon, delete DIR
#
# DIR is $KOINON_LIVE_DIR, else $HOME/koinon-670-live. Ports: agents 47681, link 47682,
# dashboard HTTPS 8444 ($KOINON_LIVE_PORT, $KOINON_LIVE_LINK_PORT, $KOINON_LIVE_HTTPS_PORT).
set -eu

DIR=${KOINON_LIVE_DIR:-$HOME/koinon-670-live}
BIN=$DIR/bin/koinon
STATE=$DIR/state
SECRET=$DIR/secret
AGENT_PORT=${KOINON_LIVE_PORT:-47681}
LINK_PORT=${KOINON_LIVE_LINK_PORT:-47682}
HTTPS_PORT=${KOINON_LIVE_HTTPS_PORT:-8444}
ADDR=127.0.0.1:$AGENT_PORT

die() { echo "live-check: $*" >&2; exit 1; }
usage() { sed -n '5,11p' "$0" >&2; exit 2; }
k() { "$BIN" "$@" --state-dir "$STATE" --address "$ADDR"; }
link() { k link "$@" --secret-file "$SECRET"; }
running() { k status >/dev/null 2>&1; }

[ $# -ge 1 ] || usage
umask 077
case $1 in
build)
	[ $# -eq 2 ] || usage
	command -v go >/dev/null || die "Go is not installed"
	mkdir -p "$DIR/bin" && chmod 700 "$DIR"
	GOBIN=$DIR/bin go install "github.com/rwcii/koinon/cmd/koinon@$2"
	echo "built $BIN from $2"
	;;
start)
	[ $# -eq 2 ] || usage
	[ -x "$BIN" ] || die "no $BIN: run build first"
	! running || die "a daemon already answers at $ADDR"
	mkdir -p "$STATE" "$DIR/work" && chmod 700 "$DIR" "$STATE"
	nohup "$BIN" serve --state-dir "$STATE" --listen "$ADDR" --listen-v6 "[::1]:$AGENT_PORT" \
		>"$DIR/serve.log" 2>&1 &
	echo $! >"$DIR/serve.pid"
	i=0
	until running; do
		i=$((i + 1))
		[ $i -lt 30 ] || { tail -20 "$DIR/serve.log" >&2; die "the daemon did not start"; }
		sleep 1
	done
	[ -s "$SECRET" ] || k maintainer secret generate >"$SECRET"
	k remote enable --https-port "$HTTPS_PORT"
	link name "$2" --port "$LINK_PORT"
	k link status
	;;
invite) link invite ;;
join) link join ;;
status)
	k link status
	link queue
	k remote status
	;;
agent)
	[ $# -eq 2 ] || usage
	cli=$(command -v "$2") || die "no $2 CLI on PATH"
	case $2 in
	codex) set -- codex -c "mcp_servers.koinon.command=\"$BIN\"" -c 'mcp_servers.koinon.args=["mcp"]' ;;
	claude)
		printf '{"mcpServers":{"koinon":{"type":"stdio","command":"%s","args":["mcp"]}}}\n' "$BIN" >"$DIR/mcp.json"
		set -- claude --mcp-config "$DIR/mcp.json" --strict-mcp-config
		;;
	*) die "use agent claude or agent codex" ;;
	esac
	family=$1
	shift
	exec "$BIN" "$family" --state-dir "$STATE" --address "$ADDR" --cli "$cli" --directory "$DIR/work" -- "$@"
	;;
stop)
	[ $# -eq 1 ] || usage
	[ -x "$BIN" ] || die "no $BIN: nothing of this check to stop in $DIR"
	if running; then k remote disable; fi  # its output shows Serve and the link listener off
	if [ -s "$DIR/serve.pid" ]; then
		pid=$(cat "$DIR/serve.pid")
		kill "$pid" 2>/dev/null || true
		i=0
		while kill -0 "$pid" 2>/dev/null; do
			i=$((i + 1))
			[ $i -lt 30 ] || die "daemon $pid did not stop"
			sleep 1
		done
	fi
	rm -rf "$DIR"
	echo "stopped; removed $DIR"
	;;
*) usage ;;
esac
