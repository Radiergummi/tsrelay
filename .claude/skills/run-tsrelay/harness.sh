#!/bin/sh
# Starts or stops the dev relay (internal/server/uidev_test.go) in the
# background: one relay with sample hooks and calls, behind four listeners
# that each stand for one identity (:8099 dev, :8098 admin, :8097 nobody,
# :8096 other). Run from the repository root.
#
#   harness.sh start   compile the harness, start it, wait until :8099 answers
#                      (refuses if :8099 is taken)
#   harness.sh stop    stop it
#
# The UI the relay serves is web/dist as embedded at compile time, so run
# `make ui` before `start` to see web changes (or use `make dev` for Vite).
set -eu
tmp=${TMPDIR:-/tmp}
out=${TSRELAY_HARNESS_DIR:-${tmp%/}/tsrelay-harness}

stop() {
	pids=$(lsof -ti:8099 -sTCP:LISTEN 2>/dev/null || true)
	[ -n "$pids" ] && kill $pids
	for _ in $(seq 1 50); do
		lsof -ti:8099 -sTCP:LISTEN >/dev/null 2>&1 || return 0
		sleep 0.1
	done
	echo "harness still holds :8099" >&2
	return 1
}

case "${1:-}" in
start)
	# The ports are fixed, so another checkout's harness (or a Playwright
	# run reusing it) may hold them; don't kill what isn't ours.
	if lsof -ti:8099 -sTCP:LISTEN >/dev/null 2>&1; then
		echo ":8099 is taken (lsof -i:8099); run '$0 stop' if it's a stale harness" >&2
		exit 1
	fi
	mkdir -p "$out"
	go test -c -o "$out/uidev.test" ./internal/server
	# The test binary runs in its package directory, as `go test` would.
	(cd internal/server && TSRELAY_UI_DEV=1 exec "$out/uidev.test" -test.run '^TestUIDev$' -test.count=1 -test.timeout=0 -test.v) >"$out/harness.log" 2>&1 &
	for _ in $(seq 1 120); do
		if curl -sf -o /dev/null http://127.0.0.1:8099/; then
			echo "harness up on http://127.0.0.1:8099 (log: $out/harness.log)"
			exit 0
		fi
		sleep 0.5
	done
	cat "$out/harness.log" >&2
	exit 1
	;;
stop) stop ;;
*)
	echo "usage: $0 start|stop" >&2
	exit 2
	;;
esac
