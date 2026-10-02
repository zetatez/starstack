#!/bin/sh
set -e

# Start the Go API server in the background.
starstack &
API_PID=$!

# Trap SIGTERM/SIGINT to stop the API child first, then nginx (which we exec so
# it keeps receiving signals). Docker sends SIGTERM on stop.
cleanup() {
  kill "$API_PID" 2>/dev/null || true
  exit 0
}
trap cleanup TERM INT

# nginx receives signals directly (exec replaces this shell process).
exec nginx -g 'daemon off;'