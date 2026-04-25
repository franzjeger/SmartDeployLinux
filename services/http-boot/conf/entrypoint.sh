#!/bin/sh
# Run the render Go binary in the background, then exec nginx in
# foreground. tini is PID 1 and reaps both. If render dies, this
# script exits and the container restarts (unless-stopped).
#
# We previously used supervisord but it pulls a Python stack that
# has ABI mismatches with newer alpine expat libraries. tini + bg/fg
# is simpler and has no ABI concerns.

set -eu

export RENDER_LISTEN=":8444"
: "${API_INTERNAL_URL:=https://api:8443}"
export API_INTERNAL_URL

/usr/local/bin/render &
RENDER_PID=$!

# If render exits unexpectedly, take nginx down with it so docker
# restarts the whole container.
nginx -g 'daemon off;' &
NGINX_PID=$!

# Wait on whichever exits first; propagate its status.
wait -n "$RENDER_PID" "$NGINX_PID"
EXIT=$?
kill -TERM "$RENDER_PID" "$NGINX_PID" 2>/dev/null || true
exit "$EXIT"
