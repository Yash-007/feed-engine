#!/usr/bin/env bash
# Cron/launchd wrapper. Adds its own random delay on top of the binary's
# start_jitter_max_sec, so runs never land on the same minute twice.
#
#   17 9  * * * /path/to/feed-engine/scripts/run.sh
#   43 14 * * * /path/to/feed-engine/scripts/run.sh
#   09 21 * * * /path/to/feed-engine/scripts/run.sh
#
# Headed Chrome needs a logged-in desktop session. On macOS prefer launchd.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

BIN="${FEED_ENGINE_BIN:-$ROOT/bin/feed-engine}"
CONFIG="${FEED_ENGINE_CONFIG:-$ROOT/config.yaml}"
LOCK="${TMPDIR:-/tmp}/feed-engine.lock"
MAX_EXTRA_DELAY="${FEED_ENGINE_MAX_DELAY:-600}"   # seconds

# cron hands over a bare PATH; claude and chrome usually live outside it
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"

[ -x "$BIN" ] || { echo "build it first: go build -o bin/feed-engine ./cmd/feed-engine" >&2; exit 1; }

# One run at a time. A long scroll session must not overlap the next cron tick.
if command -v shlock >/dev/null 2>&1 || [ -e "$LOCK" ]; then
  if [ -e "$LOCK" ] && kill -0 "$(cat "$LOCK" 2>/dev/null)" 2>/dev/null; then
    echo "$(date -u +%FT%TZ) another run is still going, skipping" >&2
    exit 0
  fi
fi
echo $$ > "$LOCK"
trap 'rm -f "$LOCK"' EXIT

sleep $((RANDOM % (MAX_EXTRA_DELAY + 1)))

exec "$BIN" -config "$CONFIG" "$@"
