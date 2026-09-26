#!/usr/bin/env bash
# gosidian-hook.sh — Claude Code hooks for a gosidian vault (IMP-094).
#
# One script, dispatched on the hook event it receives on stdin:
#
#   SessionStart  inject <project>/hot.md into the context — whole when it
#                 fits GOSIDIAN_HOOK_FOCUS_BYTES, with its etag in the
#                 memory_bootstrap reminder so the bootstrap does not repeat
#                 it; otherwise its "Current focus" section — plus this
#                 session's checkpoint note on resume/compact
#   PreCompact    append a checkpoint digest to <project>/sessions/<date>-<id>.md
#   SessionEnd    append the session digest to the same note
#   Stop          no-op unless GOSIDIAN_HOOK_STOP_LOG=1 (one entry per turn)
#   PostToolUse   with GOSIDIAN_MIRROR=1, refresh the local mirror after a
#                 gosidian MCP write
#
# Digests are built without any LLM: first prompt, turn counts, tools used,
# files touched, last assistant message. They are a safety net, not curated
# memory — the agent still owns hot.md, log.md and the directives workflow.
#
# Requires bash, curl and jq. Never blocks Claude Code: every failure is a
# line on stderr and exit 0.
#
# Configuration (environment wins over files):
#   GOSIDIAN_URL        MCP base URL, e.g. https://vault.example.com/mcp
#   GOSIDIAN_TOKEN      bearer with write scope (Settings → My MCP tokens)
#   GOSIDIAN_PROJECT    vault project (top-level folder) this checkout maps to
#   GOSIDIAN_HOOK_ENV   explicit env file; otherwise the first readable of
#                       $CLAUDE_PROJECT_DIR/.claude/gosidian.env,
#                       ~/.config/gosidian/hook.env
#   GOSIDIAN_HOOK_FOCUS_BYTES   hot.md up to this size is injected whole, a
#                               larger one as its focus excerpt (6000)
#   GOSIDIAN_HOOK_LOG_ENTRY     1 = also append a one-line pointer to log.md
#   GOSIDIAN_HOOK_STOP_LOG      1 = append the last assistant message per turn
#   GOSIDIAN_MIRROR             1 = keep a local read-only mirror of the project
#                               (`gosidian mirror sync`, IMP-102): synced in the
#                               background at SessionStart and after MCP writes,
#                               announced in the session context. The project
#                               admin must enable "mirror" on the project.
#   GOSIDIAN_MIRROR_DIR         mirror root (default <project dir>/.gosidian/mirror)
#   GOSIDIAN_BIN                gosidian binary for the mirror (default: gosidian on PATH)

set -u

warn() { printf 'gosidian-hook: %s\n' "$*" >&2; }
die0() { warn "$*"; exit 0; }

command -v jq >/dev/null 2>&1 || die0 "jq not found"
command -v curl >/dev/null 2>&1 || die0 "curl not found"

INPUT=$(cat 2>/dev/null || true)
EVENT=$(printf '%s' "$INPUT" | jq -r '.hook_event_name // empty' 2>/dev/null)
[ -n "$EVENT" ] || die0 "no hook_event_name on stdin"

# --- configuration --------------------------------------------------------

PRE_URL=${GOSIDIAN_URL:-}; PRE_TOKEN=${GOSIDIAN_TOKEN:-}; PRE_PROJECT=${GOSIDIAN_PROJECT:-}
PRE_MIRROR=${GOSIDIAN_MIRROR:-}; PRE_MIRROR_DIR=${GOSIDIAN_MIRROR_DIR:-}; PRE_BIN=${GOSIDIAN_BIN:-}
for f in "${GOSIDIAN_HOOK_ENV:-}" "${CLAUDE_PROJECT_DIR:-$PWD}/.claude/gosidian.env" "${HOME:-}/.config/gosidian/hook.env"; do
  if [ -n "$f" ] && [ -r "$f" ]; then
    set -a
    # shellcheck disable=SC1090
    . "$f"
    set +a
    break
  fi
done
GOSIDIAN_URL=${PRE_URL:-${GOSIDIAN_URL:-}}
GOSIDIAN_TOKEN=${PRE_TOKEN:-${GOSIDIAN_TOKEN:-}}
GOSIDIAN_PROJECT=${PRE_PROJECT:-${GOSIDIAN_PROJECT:-}}
GOSIDIAN_MIRROR=${PRE_MIRROR:-${GOSIDIAN_MIRROR:-0}}
GOSIDIAN_MIRROR_DIR=${PRE_MIRROR_DIR:-${GOSIDIAN_MIRROR_DIR:-}}
GOSIDIAN_BIN=${PRE_BIN:-${GOSIDIAN_BIN:-gosidian}}
[ -n "$GOSIDIAN_URL" ] && [ -n "$GOSIDIAN_TOKEN" ] && [ -n "$GOSIDIAN_PROJECT" ] \
  || die0 "GOSIDIAN_URL, GOSIDIAN_TOKEN and GOSIDIAN_PROJECT are required (env or .claude/gosidian.env)"

# The MCP base URL minus any trailing /sse or slash: the byte endpoints hang
# off it (/download, /append), like the bootstrap hints say.
URL=${GOSIDIAN_URL%/}; URL=${URL%/sse}; URL=${URL%/}
FOCUS_BYTES=${GOSIDIAN_HOOK_FOCUS_BYTES:-6000}
PROJECT=$GOSIDIAN_PROJECT

SID=$(printf '%s' "$INPUT" | jq -r '.session_id // empty')
SID8=${SID:0:8}; [ -n "$SID8" ] || SID8=nosession
TRANSCRIPT=$(printf '%s' "$INPUT" | jq -r '.transcript_path // empty')
CWD=$(printf '%s' "$INPUT" | jq -r '.cwd // empty')
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
TODAY=$(date -u +%Y%m%d)
SESSION_NOTE="$PROJECT/sessions/$TODAY-$SID8.md"
PROJECT_DIR=${CLAUDE_PROJECT_DIR:-$PWD}
MIRROR_ROOT=${GOSIDIAN_MIRROR_DIR:-$PROJECT_DIR/.gosidian/mirror}

# --- HTTP helpers ----------------------------------------------------------

# api_get <vault path>: note body on stdout; returns 1 on any non-200.
api_get() {
  curl -sS -f --max-time 8 -H "Authorization: Bearer $GOSIDIAN_TOKEN" \
    "$URL/download?path=$1" 2>/dev/null
}

# api_get_etag <vault path> <header file>: like api_get, and leaves the
# response headers in the file for the ETag.
api_get_etag() {
  curl -sS -f --max-time 8 -D "$2" -H "Authorization: Bearer $GOSIDIAN_TOKEN" \
    "$URL/download?path=$1" 2>/dev/null
}

# api_append <vault path>: appends stdin; prints a warning on failure.
api_append() {
  local out
  if ! out=$(curl -sS --max-time 8 -X POST -H "Authorization: Bearer $GOSIDIAN_TOKEN" \
      -H 'Content-Type: text/markdown' --data-binary @- "$URL/append?path=$1" 2>&1); then
    warn "append to $1 failed: $out"
    return 1
  fi
  case "$out" in
    *'"error"'*) warn "append to $1 refused: $out"; return 1 ;;
  esac
  return 0
}

# --- digest ----------------------------------------------------------------

# focus_excerpt: the "## Current focus" section of hot.md, capped.
focus_excerpt() {
  awk 'BEGIN{p=0} /^## /{ if(p){exit}; if($0 ~ /^## Current focus/){p=1; next} } p{print}' \
    | head -c "$FOCUS_BYTES"
}

# transcript_digest: markdown bullets from the transcript JSONL, best-effort
# (the format is not a published contract), or a one-line fallback.
transcript_digest() {
  local tp=$1
  if [ -z "$tp" ] || [ ! -r "$tp" ]; then
    printf -- '- _(transcript not readable: no digest)_\n'
    return
  fi
  jq -rRs --argjson maxp 240 --argjson maxa 400 '
    def txt: if type=="string" then . elif type=="array" then ([.[] | select(type=="object" and .type=="text") | (.text // "")] | join(" ")) else "" end;
    def role: (.message.role // .role // "");
    def content: (.message.content // .content // "");
    def squash: gsub("\\s+"; " ") | ltrimstr(" ") | rtrimstr(" ");
    def trunc($n): if length > $n then .[:$n] + "…" else . end;
    # one line = one JSON object; lines that do not parse are skipped
    (split("\n") | map(select(length > 0) | (fromjson? // empty)) | map(select(type=="object"))) as $m
    | ($m | map(select(role=="user" and ((content|txt|length) > 0)))) as $users
    | ($m | map(select(role=="assistant"))) as $asst
    | ([$asst[] | content | if type=="array" then .[] | select(type=="object" and .type=="tool_use") else empty end]) as $tools
    | ([$tools[] | .input | (.file_path // .notebook_path // .path // empty) | strings] | unique) as $files
    | ([$asst[] | content | txt | squash | select(length > 0)] | last // "") as $lastText
    | ([$m[] | .timestamp? // empty | strings] | [first, last]) as $ts
    | "- **First prompt**: " + (($users[0] | content | txt | squash | trunc($maxp)) // "—")
    + "\n- **Turns**: \($users | length) user · \($asst | length) assistant"
    + (if ($ts[0] // "") != "" then "\n- **Span**: \($ts[0]) → \($ts[1])" else "" end)
    + "\n- **Tools**: " + (if ($tools | length) > 0 then ($tools | group_by(.name) | sort_by(-length) | .[:8] | map("\(.[0].name)×\(length)") | join(", ")) else "—" end)
    + "\n- **Files touched**: " + (if ($files | length) > 0 then ($files | .[:20] | join(", ")) + (if ($files | length) > 20 then " … (+\($files | length - 20))" else "" end) else "—" end)
    + "\n- **Last assistant message**: " + (if ($lastText | length) > 0 then ($lastText | trunc($maxa)) else "—" end)
  ' "$tp" 2>/dev/null || printf -- '- _(transcript not parseable: no digest)_\n'
}

# session_header: frontmatter + title for a session note that does not
# exist yet (the append endpoint creates it from this first body).
session_header() {
  cat <<EOF
---
title: Session $SID8 — $TODAY
description: Auto-captured digest of a Claude Code session (contrib/claude-code hooks). Raw, not curated — promote what matters into hot.md, log.md and the memory notes.
tags: [$PROJECT, type:doc, topic:session, topic:auto-capture]
type: doc
importance: 1
created: ${NOW%%T*}
---

# Session $SID8 — $TODAY

- **Session id**: \`$SID\`
- **cwd**: \`$CWD\`

EOF
}

# append_session <body on stdin>: appends to the session note, adding the
# header when the note does not exist yet.
append_session() {
  local body
  body=$(cat)
  if api_get "$SESSION_NOTE" >/dev/null 2>&1; then
    printf '%s\n' "$body" | api_append "$SESSION_NOTE"
  else
    { session_header; printf '%s\n' "$body"; } | api_append "$SESSION_NOTE"
  fi
}

# --- local mirror ----------------------------------------------------------

# mirror_sync_bg starts `gosidian mirror sync` detached, output to
# <root>/.sync.log, so the hook returns at once; the binary's lock turns a
# sync already running into a no-op. Fails when the binary is missing.
mirror_sync_bg() {
  command -v "$GOSIDIAN_BIN" >/dev/null 2>&1 || { warn "mirror: $GOSIDIAN_BIN not found"; return 1; }
  mkdir -p "$MIRROR_ROOT" 2>/dev/null || { warn "mirror: cannot create $MIRROR_ROOT"; return 1; }
  local detach=""
  command -v setsid >/dev/null 2>&1 && detach=setsid
  ( GOSIDIAN_TOKEN="$GOSIDIAN_TOKEN" $detach nohup "$GOSIDIAN_BIN" mirror sync \
      --url "$URL" --project "$PROJECT" --dir "$MIRROR_ROOT" \
      </dev/null >>"$MIRROR_ROOT/.sync.log" 2>&1 & )
  return 0
}

# mirror_context tells the agent where the mirror is and how to use it.
# shellcheck disable=SC2016 # backticks are markdown, not command substitution
mirror_context() {
  local shown=$MIRROR_ROOT last
  case "$shown" in "$PROJECT_DIR"/*) shown=${shown#"$PROJECT_DIR"/} ;; esac
  last=$(jq -r '.synced_at // empty' "$MIRROR_ROOT/.$PROJECT.state.json" 2>/dev/null)
  printf '## Local read-only mirror of `%s`\n\n' "$PROJECT"
  printf 'A read-only copy of the project is at `%s/%s` (last sync: %s; a sync has just started in the background). ' \
    "$shown" "$PROJECT" "${last:-none yet — the first one is running, read through MCP until the files appear}"
  printf 'Read and search it with your file tools. Start from `hot.md`, `README.md` and `_index.md` (every note from the most recent); '
  printf 'follow tags and [[wikilinks]] (paths from `%s`); try synonyms and the other language before concluding there is no answer; when two notes disagree, prefer the more recent one. ' "$shown"
  printf 'Never edit these files: write only with the gosidian MCP tools; the mirror refreshes after each write.\n'
}

# --- events ----------------------------------------------------------------

case "$EVENT" in
  SessionStart)
    reason=$(printf '%s' "$INPUT" | jq -r '.startup_reason // .source // "startup"')
    # A hot.md that fits the cap goes in whole and its etag into the
    # bootstrap reminder (known_etags), so the bootstrap does not bring the
    # same text a second time; a larger one goes in as its focus excerpt.
    hdr=$(mktemp 2>/dev/null || echo "/tmp/gosidian-hook.$$")
    hot=$(api_get_etag "$PROJECT/hot.md" "$hdr" || true)
    etag=$(tr -d '\r' <"$hdr" 2>/dev/null | sed -n 's/^[Ee][Tt][Aa][Gg]:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' | tail -n 1)
    rm -f "$hdr"
    known=""
    if [ -z "$hot" ]; then
      ctx="## gosidian — project \`$PROJECT\` (hot.md)"$'\n\n'
      ctx+="_(hot.md not readable from $URL — check GOSIDIAN_URL/TOKEN/PROJECT)_"$'\n\n'
    elif [ "$(printf '%s' "$hot" | wc -c)" -le "$FOCUS_BYTES" ]; then
      ctx="## gosidian — project \`$PROJECT\` (hot.md, whole)"$'\n\n'"$hot"$'\n\n'
      [ -n "$etag" ] && known=", known_etags: {\"$PROJECT/hot.md\": \"$etag\"}"
    else
      ctx="## gosidian — project \`$PROJECT\` (hot.md → Current focus, excerpt)"$'\n\n'
      ctx+="$(printf '%s' "$hot" | focus_excerpt)"$'\n\n'
    fi
    case "$reason" in
      resume|compact)
        cp=$(api_get "$SESSION_NOTE" 2>/dev/null | tail -c 3000 || true)
        if [ -n "$cp" ]; then
          ctx+="## This session so far (auto-captured checkpoint, $reason)"$'\n\n'"$cp"$'\n\n'
        fi
        ;;
    esac
    if [ "$GOSIDIAN_MIRROR" = "1" ]; then
      if mirror_sync_bg; then
        ctx+="$(mirror_context)"$'\n\n'
      else
        ctx+="_Local mirror enabled (GOSIDIAN_MIRROR=1) but \`$GOSIDIAN_BIN\` was not found: install the gosidian binary or set GOSIDIAN_BIN._"$'\n\n'
      fi
    fi
    if [ -n "$known" ]; then
      ctx+="_Injected by the gosidian hook. Run \`memory_bootstrap({project: \"$PROJECT\"$known})\` before touching code: the bootstrap is authoritative and carries the directives; with that etag it does not repeat the hot.md above (\`unchanged: true\`)._"
    else
      ctx+="_This excerpt is a preview injected by the gosidian hook. Run \`memory_bootstrap({project: \"$PROJECT\"})\` before touching code: the bootstrap is authoritative and carries the directives._"
    fi
    jq -n --arg ctx "$ctx" '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
    ;;

  PreCompact)
    trigger=$(printf '%s' "$INPUT" | jq -r '.trigger // "unknown"')
    {
      printf '### Checkpoint %s — compact (%s)\n\n' "$NOW" "$trigger"
      transcript_digest "$TRANSCRIPT"
    } | append_session || true
    ;;

  SessionEnd)
    reason=$(printf '%s' "$INPUT" | jq -r '.end_reason // .reason // "other"')
    {
      printf '## Session end %s — %s\n\n' "$NOW" "$reason"
      transcript_digest "$TRANSCRIPT"
    } | append_session || true
    if [ "${GOSIDIAN_HOOK_LOG_ENTRY:-0}" = "1" ]; then
      printf -- '- %s — session — auto-captured digest in [[%s]] (%s)\n' "$NOW" "${SESSION_NOTE%.md}" "$reason" \
        | api_append "$PROJECT/log.md" || true
    fi
    ;;

  Stop)
    [ "${GOSIDIAN_HOOK_STOP_LOG:-0}" = "1" ] || exit 0
    active=$(printf '%s' "$INPUT" | jq -r '.stop_hook_active // false')
    [ "$active" = "true" ] && exit 0
    last=$(printf '%s' "$INPUT" | jq -r '.last_assistant_message // empty' | head -c 600)
    [ -n "$last" ] || exit 0
    printf '### Turn %s\n\n%s\n' "$NOW" "$last" | append_session || true
    ;;

  PostToolUse)
    [ "$GOSIDIAN_MIRROR" = "1" ] || exit 0
    tool=$(printf '%s' "$INPUT" | jq -r '.tool_name // empty')
    case "$tool" in
      mcp__*osidian*__memory_*) ;;
      *) exit 0 ;;
    esac
    case "${tool##*__memory_}" in
      create|update|edit|append|delete|move_note|rename_note|ingest|create_table_note|create_media_note)
        mirror_sync_bg || true ;;
    esac
    ;;

  *)
    exit 0
    ;;
esac
exit 0
