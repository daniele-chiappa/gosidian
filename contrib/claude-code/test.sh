#!/usr/bin/env bash
# test.sh — exercises gosidian-hook.sh against a fake gosidian server with
# fixture stdin and transcript. Needs bash, jq, curl and python3.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
HOOK="$HERE/gosidian-hook.sh"
WORK=$(mktemp -d)
trap 'kill ${SRV_PID:-0} 2>/dev/null || true; rm -rf "$WORK"' EXIT

# --- fake server: /mcp/download and /mcp/append, recording appends ---------
cat > "$WORK/server.py" <<'PY'
import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs
WORK = sys.argv[1]
NOTES = {"proj/hot.md": "---\ntitle: Hot\n---\n\n# Hot\n\n## Current focus\n\nWorking on the login bug.\nNext: ship v9.\n\n## Active plans\n\nnone\n"}
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _json(self, code, obj):
        body = json.dumps(obj).encode(); self.send_response(code)
        self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(body)))
        self.end_headers(); self.wfile.write(body)
    def do_GET(self):
        u = urlparse(self.path); q = parse_qs(u.query)
        if u.path != "/mcp/download": return self._json(404, {"error": "no"})
        if self.headers.get("Authorization") != "Bearer tok-1": return self._json(401, {"error": "auth"})
        p = q.get("path", [""])[0]
        if p not in NOTES: return self._json(404, {"error": "note not found"})
        body = NOTES[p].encode(); self.send_response(200)
        self.send_header("Content-Type", "text/markdown"); self.send_header("Content-Length", str(len(body)))
        self.send_header("ETag", '"1790-%d"' % len(body))
        self.end_headers(); self.wfile.write(body)
    def do_POST(self):
        u = urlparse(self.path); q = parse_qs(u.query)
        if u.path != "/mcp/append": return self._json(404, {"error": "no"})
        if self.headers.get("Authorization") != "Bearer tok-1": return self._json(401, {"error": "auth"})
        p = q.get("path", [""])[0]
        n = int(self.headers.get("Content-Length", "0")); body = self.rfile.read(n).decode()
        created = p not in NOTES
        NOTES[p] = NOTES.get(p, "") + ("\n" if p in NOTES else "") + body
        with open(os.path.join(WORK, "appends.log"), "a") as f:
            f.write("=== " + p + "\n" + body + "\n")
        self._json(200, {"path": p, "etag": "e1", "created": created})
srv = HTTPServer(("127.0.0.1", 0), H)
open(os.path.join(WORK, "port"), "w").write(str(srv.server_port))
srv.serve_forever()
PY
python3 "$WORK/server.py" "$WORK" & SRV_PID=$!
for _ in $(seq 1 50); do [ -s "$WORK/port" ] && break; sleep 0.1; done
PORT=$(cat "$WORK/port")

export GOSIDIAN_URL="http://127.0.0.1:$PORT/mcp/sse"   # the /sse suffix must be stripped
export GOSIDIAN_TOKEN=tok-1
export GOSIDIAN_PROJECT=proj
export HOME="$WORK"   # no ~/.config/gosidian/hook.env interference

# --- transcript fixture ------------------------------------------------------
cat > "$WORK/transcript.jsonl" <<'JSONL'
{"type":"user","timestamp":"2026-09-25T10:00:00Z","message":{"role":"user","content":"Fix the   login bug in the SPA"}}
{"type":"assistant","timestamp":"2026-09-25T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"Looking at it."},{"type":"tool_use","name":"Read","input":{"file_path":"web/src/views/LoginView.vue"}}]}}
{"type":"user","timestamp":"2026-09-25T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"..."}]}}
{"type":"assistant","timestamp":"2026-09-25T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"web/src/views/LoginView.vue"}},{"type":"tool_use","name":"Bash","input":{"command":"npm test"}}]}}
{"type":"assistant","timestamp":"2026-09-25T10:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Fixed the redirect after login and added a test."}]}}
not json at all
JSONL

fail() { echo "FAIL: $*" >&2; [ -f "$WORK/appends.log" ] && { echo "--- appends.log ---" >&2; cat "$WORK/appends.log" >&2; }; exit 1; }

# --- SessionStart -----------------------------------------------------------
out=$(printf '{"hook_event_name":"SessionStart","session_id":"abcdef1234567890","transcript_path":"%s","cwd":"/w","startup_reason":"startup"}' "$WORK/transcript.jsonl" | "$HOOK")
ctx=$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')
[ "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.hookEventName')" = "SessionStart" ] || fail "SessionStart event name"
case "$ctx" in *"Working on the login bug."*"## Active plans"*) ;; *) fail "a small hot.md must be injected whole: $ctx";; esac
case "$ctx" in *'memory_bootstrap({project: "proj", known_etags: {"proj/hot.md": "1790-'*'"}})'*) ;; *) fail "bootstrap reminder must carry the hot.md etag: $ctx";; esac
echo "ok  SessionStart injects a small hot.md whole, etag in the bootstrap reminder"

out=$(printf '{"hook_event_name":"SessionStart","session_id":"abcdef1234567890","startup_reason":"startup"}' | GOSIDIAN_HOOK_FOCUS_BYTES=60 "$HOOK")
ctx=$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')
case "$ctx" in *"Working on the login bug."*) ;; *) fail "focus excerpt missing: $ctx";; esac
case "$ctx" in *"Active plans"*) fail "excerpt must stop at the next section";; esac
case "$ctx" in *"known_etags"*) fail "an excerpt must not claim the bootstrap can skip hot.md";; esac
case "$ctx" in *'memory_bootstrap({project: "proj"})'*) ;; *) fail "bootstrap reminder missing";; esac
echo "ok  SessionStart injects the focus excerpt of a hot.md over the cap"

# --- PreCompact creates the session note with a header ----------------------
printf '{"hook_event_name":"PreCompact","session_id":"abcdef1234567890","transcript_path":"%s","cwd":"/w","trigger":"auto"}' "$WORK/transcript.jsonl" | "$HOOK"
grep -q "^=== proj/sessions/$(date -u +%Y%m%d)-abcdef12.md" "$WORK/appends.log" || fail "checkpoint not appended to the session note"
grep -q "topic:auto-capture" "$WORK/appends.log" || fail "session note header missing on first append"
grep -q "### Checkpoint .* compact (auto)" "$WORK/appends.log" || fail "checkpoint heading missing"
grep -q "First prompt\*\*: Fix the login bug in the SPA" "$WORK/appends.log" || fail "first prompt not squashed/extracted"
grep -q "Tools\*\*: " "$WORK/appends.log" || fail "tools line missing"
grep -q "Edit×1" "$WORK/appends.log" || fail "tool histogram missing Edit"
grep -q "Files touched\*\*: web/src/views/LoginView.vue" "$WORK/appends.log" || fail "files touched missing"
grep -q "Last assistant message\*\*: Fixed the redirect" "$WORK/appends.log" || fail "last assistant text missing"
grep -q "Turns\*\*: 1 user · 3 assistant" "$WORK/appends.log" || fail "turn counts wrong"
echo "ok  PreCompact appends a checkpoint with header and digest"

# --- SessionStart on compact re-injects the checkpoint ----------------------
out=$(printf '{"hook_event_name":"SessionStart","session_id":"abcdef1234567890","transcript_path":"%s","cwd":"/w","startup_reason":"compact"}' "$WORK/transcript.jsonl" | "$HOOK")
ctx=$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')
case "$ctx" in *"This session so far"*"Checkpoint"*) ;; *) fail "checkpoint not re-injected on compact";; esac
echo "ok  SessionStart(compact) re-injects the checkpoint"

# --- SessionEnd appends without a second header; log entry optional --------
: > "$WORK/appends.log"
printf '{"hook_event_name":"SessionEnd","session_id":"abcdef1234567890","transcript_path":"%s","cwd":"/w","end_reason":"clear"}' "$WORK/transcript.jsonl" | GOSIDIAN_HOOK_LOG_ENTRY=1 "$HOOK"
grep -q "## Session end .* clear" "$WORK/appends.log" || fail "session end heading missing"
grep -q "topic:auto-capture" "$WORK/appends.log" && fail "header must not be repeated on an existing note"
grep -q "^=== proj/log.md" "$WORK/appends.log" || fail "log.md pointer missing with GOSIDIAN_HOOK_LOG_ENTRY=1"
grep -q "\[\[proj/sessions/" "$WORK/appends.log" || fail "log pointer must link the session note"
echo "ok  SessionEnd appends the digest and the optional log pointer"

# --- Stop is a no-op unless enabled -----------------------------------------
: > "$WORK/appends.log"
printf '{"hook_event_name":"Stop","session_id":"abcdef1234567890","last_assistant_message":"Done."}' | "$HOOK"
[ ! -s "$WORK/appends.log" ] || fail "Stop must be a no-op by default"
printf '{"hook_event_name":"Stop","session_id":"abcdef1234567890","last_assistant_message":"Done."}' | GOSIDIAN_HOOK_STOP_LOG=1 "$HOOK"
grep -q "### Turn" "$WORK/appends.log" || fail "Stop with GOSIDIAN_HOOK_STOP_LOG=1 must append"
echo "ok  Stop honours GOSIDIAN_HOOK_STOP_LOG"

# --- never blocks: missing config, unreadable transcript, bad server -------
printf '{"hook_event_name":"SessionEnd","session_id":"x"}' | GOSIDIAN_URL='' GOSIDIAN_TOKEN='' "$HOOK" >/dev/null 2>"$WORK/err" || fail "must exit 0 without config"
grep -q "required" "$WORK/err" || fail "missing-config warning expected"
printf '{"hook_event_name":"SessionEnd","session_id":"x","transcript_path":"/nonexistent"}' | "$HOOK" || fail "must exit 0 with an unreadable transcript"
printf '{"hook_event_name":"SessionStart","session_id":"x"}' | GOSIDIAN_URL=http://127.0.0.1:1/mcp "$HOOK" | jq -e '.hookSpecificOutput.additionalContext | test("not readable")' >/dev/null || fail "unreachable server must still produce context"
echo "ok  failures never block"

# --- local mirror: a fake gosidian binary records how it is called ---------
cat > "$WORK/gosidian" <<'SH'
#!/usr/bin/env bash
# fake: record the call and the token it saw, write a state file like a sync
dir=""; prev=""
for a in "$@"; do [ "$prev" = "--dir" ] && dir=$a; prev=$a; done
echo "$* token=$GOSIDIAN_TOKEN" >> "$FAKE_LOG"
[ -n "$dir" ] && mkdir -p "$dir" && printf '{"project":"proj","synced_at":"2026-09-26T10:00:00Z"}\n' > "$dir/.proj.state.json"
SH
chmod +x "$WORK/gosidian"
export FAKE_LOG="$WORK/fake.log"
wait_log() { for _ in $(seq 1 30); do [ "$(wc -l < "$FAKE_LOG" 2>/dev/null || echo 0)" -ge "$1" ] && return 0; sleep 0.1; done; return 1; }
MIRROR_ENV=(GOSIDIAN_MIRROR=1 GOSIDIAN_BIN="$WORK/gosidian" CLAUDE_PROJECT_DIR="$WORK/checkout")
mkdir -p "$WORK/checkout"

out=$(printf '{"hook_event_name":"SessionStart","session_id":"m1","startup_reason":"startup"}' | env "${MIRROR_ENV[@]}" "$HOOK")
ctx=$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')
wait_log 1 || fail "mirror sync not started at SessionStart"
grep -q "mirror sync --url http://127.0.0.1:$PORT/mcp --project proj --dir $WORK/checkout/.gosidian/mirror token=tok-1" "$FAKE_LOG" || fail "unexpected sync call: $(cat "$FAKE_LOG")"
case "$ctx" in *"Local read-only mirror of \`proj\`"*".gosidian/mirror/proj"*"_index.md"*"write only with the gosidian MCP tools"*) ;; *) fail "mirror context missing: $ctx";; esac
case "$ctx" in *"memory_bootstrap"*) ;; *) fail "bootstrap reminder lost with the mirror on";; esac
echo "ok  SessionStart syncs the mirror in the background and announces it"

out=$(printf '{"hook_event_name":"SessionStart","session_id":"m1","startup_reason":"startup"}' | env "${MIRROR_ENV[@]}" "$HOOK")
case "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')" in *"last sync: 2026-09-26T10:00:00Z"*) ;; *) fail "last sync time not reported";; esac
wait_log 2 || true
echo "ok  SessionStart reports the last sync"

: > "$FAKE_LOG"
printf '{"hook_event_name":"PostToolUse","session_id":"m1","tool_name":"mcp__gosidian__memory_append","tool_input":{"path":"proj/log.md"}}' | env "${MIRROR_ENV[@]}" "$HOOK"
wait_log 1 || fail "PostToolUse on a gosidian write must resync"
printf '{"hook_event_name":"PostToolUse","session_id":"m1","tool_name":"mcp__gosidian__memory_get","tool_input":{"path":"proj/log.md"}}' | env "${MIRROR_ENV[@]}" "$HOOK"
printf '{"hook_event_name":"PostToolUse","session_id":"m1","tool_name":"Edit","tool_input":{}}' | env "${MIRROR_ENV[@]}" "$HOOK"
sleep 0.5
[ "$(wc -l < "$FAKE_LOG")" -eq 1 ] || fail "reads and non-gosidian tools must not resync: $(cat "$FAKE_LOG")"
echo "ok  PostToolUse resyncs after gosidian writes only"

: > "$FAKE_LOG"
out=$(printf '{"hook_event_name":"SessionStart","session_id":"m2","startup_reason":"startup"}' | CLAUDE_PROJECT_DIR="$WORK/checkout" GOSIDIAN_BIN="$WORK/gosidian" "$HOOK")
case "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')" in *"mirror"*) fail "mirror mentioned while GOSIDIAN_MIRROR is off";; esac
printf '{"hook_event_name":"PostToolUse","session_id":"m2","tool_name":"mcp__gosidian__memory_append"}' | GOSIDIAN_BIN="$WORK/gosidian" "$HOOK"
sleep 0.3
[ ! -s "$FAKE_LOG" ] || fail "mirror off must never call the binary"
out=$(printf '{"hook_event_name":"SessionStart","session_id":"m3","startup_reason":"startup"}' | GOSIDIAN_MIRROR=1 GOSIDIAN_BIN="$WORK/nope" CLAUDE_PROJECT_DIR="$WORK/checkout" "$HOOK" 2>/dev/null)
case "$(printf '%s' "$out" | jq -r '.hookSpecificOutput.additionalContext')" in *"was not found"*) ;; *) fail "missing binary must be reported in the context";; esac
echo "ok  mirror off stays silent; a missing binary is reported"

echo "all hook tests passed"
