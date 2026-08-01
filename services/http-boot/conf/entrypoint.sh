#!/bin/sh
# Run the render Go binary and nginx side by side, and make sure the
# container dies as soon as *either* of them does. tini is PID 1 and
# reaps whatever is left.
#
# We previously used supervisord but it pulls a Python stack that
# has ABI mismatches with newer alpine expat libraries. tini + two
# background jobs is simpler and has no ABI concerns.
#
# Do NOT reach for `wait -n` here. This is BusyBox ash, not bash, and
# ash accepts `-n` but silently ignores it: `wait -n A B` blocks until
# *every* listed job has exited, not the first one. That turned this
# script into a no-op supervisor — when nginx refused a bad config, the
# script kept waiting on render, so the container sat there reporting
# "Up" with the whole /boot/* path dead. Poll instead: the shell reaps
# background jobs as they exit, so `kill -0` on a finished child fails
# and we notice within a second.

set -eu

export RENDER_LISTEN=":8444"
: "${API_INTERNAL_URL:=https://api:8443}"
export API_INTERNAL_URL

/usr/local/bin/render &
RENDER_PID=$!

nginx -g 'daemon off;' &
NGINX_PID=$!

stop_children() {
    kill -TERM "$RENDER_PID" "$NGINX_PID" 2>/dev/null || true
}

# Forward docker's stop signal so the container shuts down promptly
# instead of waiting out the SIGKILL timeout.
trap 'stop_children; exit 143' TERM INT

while kill -0 "$RENDER_PID" 2>/dev/null && kill -0 "$NGINX_PID" 2>/dev/null; do
    sleep 1
done

if kill -0 "$RENDER_PID" 2>/dev/null; then
    echo "http-boot: nginx exited; bringing the container down" >&2
else
    echo "http-boot: render exited; bringing the container down" >&2
fi

stop_children
exit 1
