#!/bin/sh
# ARTEX supervisor start script (Linux / macOS / Docker ENTRYPOINT)
#
# Usage:
#   ./start.sh                       run in the foreground (Ctrl-C to stop)
#   nohup ./start.sh >artex.log 2>&1 &   run persistently in the background
#   ./start.sh -addr :9000           extra arguments are passed through to artex as-is
#
# It does exactly one thing: run artex and, once the process exits, decide by exit code whether to relaunch it.
#
#   0      user stopped normally  -> exit the loop
#   75     program requested a restart -> rerun immediately ("one-click update" or "rollback" was clicked in the web UI)
#   other  crash               -> rerun after backoff (1→2→4... up to 60 seconds)
#
# Download, SHA256 verification, and the swap are deliberately not done here: that logic would need two implementations in sh and bat,
# and it is precisely the step that must never fail — once an unrunnable binary is swapped in, this script would faithfully relaunch it
# forever, and the user could only fix it on the machine by hand. So all verification/swap logic stays in Go (the selfupdate package),
# done by artex itself at startup, keeping the script dumb.
set -u

cd "$(dirname "$0")" || exit 1

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] executable $BIN not found" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# Forward stop signals to artex itself.
#
# Under Docker this is required: docker stop sends SIGTERM only to PID 1 (this script),
# never to the child. Without forwarding, artex never sees the signal and cannot shut down gracefully; after 10 seconds it gets
# hard-killed by SIGKILL and any running task is cut off mid-flight.
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# A signal interrupts wait and makes it return >128. At that point the child is still shutting down gracefully;
	# wait must be called once more to get its real exit code.
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] stopped"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] exited normally"
			exit 0
			;;
		"$RESTART_CODE")
			# Update/rollback is ready: after the rerun, artex completes the swap at startup (see selfupdate.Bootstrap).
			echo "[artex] restart requested (applying new version)…"
			delay=1
			;;
		*)
			echo "[artex] abnormal exit (code=$code), restarting in ${delay}s" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
