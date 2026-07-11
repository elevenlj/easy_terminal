#!/bin/sh
# Codex runs this through its `notify` setting when a turn ends. The first
# argument is the previous notification command, followed by its arguments.

set -u

hook_log="${EASY_TERMINAL_HOOK_LOG:-/tmp/easy-terminal-codex-hook.log}"
log_hook() {
  printf '%s session=%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "${EASY_TERMINAL_SESSION_ID:-unknown}" "$1" >> "$hook_log" 2>/dev/null || true
}

log_hook "turn-ended hook invoked"

if [ "$#" -gt 0 ] && [ -n "$1" ]; then
  original_notify="$1"
  shift
  "$original_notify" "$@" >/dev/null 2>&1 &
fi

if [ -z "${EASY_TERMINAL_HOOK_URL:-}" ] || [ -z "${EASY_TERMINAL_SESSION_ID:-}" ] || [ -z "${EASY_TERMINAL_HOOK_TOKEN:-}" ]; then
	log_hook "callback skipped: Easy Terminal environment is unavailable"
  exit 0
fi

status=$(/usr/bin/curl --silent --show-error --max-time 2 -o /dev/null -w '%{http_code}' \
  -X POST "${EASY_TERMINAL_HOOK_URL}/api/sessions/${EASY_TERMINAL_SESSION_ID}/hook/turn-ended" \
  -H "Authorization: Bearer ${EASY_TERMINAL_HOOK_TOKEN}" \
  -H "Content-Type: application/json" \
  --data '{}' 2>/dev/null || true)
log_hook "callback completed: HTTP ${status:-000}"
