#!/usr/bin/env bash
# Periodically snapshots `kushim` processes and their open PDF/mupdf file
# descriptors, so a runaway reclaim/fork loop can be inspected after the
# fact instead of only being visible while it's happening.
#
# Usage: ./watch-kushim.sh [interval_seconds] [outfile]
set -euo pipefail

INTERVAL="${1:-5}"
OUTFILE="${2:-$(dirname "$0")/kushim-ps-watch.log}"

echo "Watching kushim processes every ${INTERVAL}s -> ${OUTFILE}"
echo "Press Ctrl+C to stop."

trap 'echo "=== stopped $(date -Iseconds) ===" >> "$OUTFILE"; exit 0' INT TERM

while true; do
	{
		echo "=== $(date -Iseconds) ==="
		ps_out=$(ps aux | grep '[k]ushim' || true)
		if [ -z "$ps_out" ]; then
			echo "no kushim processes found"
		else
			echo "$ps_out"
			while read -r _ pid _; do
				echo "--- open PDF/mupdf fds for PID $pid ---"
				if command -v lsof >/dev/null 2>&1; then
					lsof -p "$pid" 2>/dev/null | grep -iE '\.pdf|mupdf' || echo "  (none)"
				else
					ls -la "/proc/$pid/fd" 2>/dev/null | grep -iE '\.pdf|mupdf' || echo "  (none)"
				fi
			done <<<"$ps_out"
		fi
		echo
	} >>"$OUTFILE"
	sleep "$INTERVAL"
done
