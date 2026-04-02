#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# Claude Code Hook Logger
#
# 全フックイベントをJSONLファイルとSQLiteに記録する。
# stdinからJSON入力を受け取り、タイムスタンプを付与して保存。
# exit 0で返すので、Claude Codeの動作には一切干渉しない。
#
# Usage (settings.jsonから呼ばれる):
#   hooks:
#     PreToolUse:
#       - type: command
#         command: "/path/to/hook_logger.sh"
# ─────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="${CLAUDE_HOOK_LOG_DIR:-$SCRIPT_DIR/../logs}"
JSONL_FILE="$LOG_DIR/hooks.jsonl"
DB_FILE="$LOG_DIR/hooks.db"

mkdir -p "$LOG_DIR"

# stdinからJSON入力を読み取り
INPUT=$(cat)

# タイムスタンプ付与
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ")

# JSONL出力（jqがあれば整形、なければそのまま）
if command -v jq &>/dev/null; then
    RECORD=$(echo "$INPUT" | jq -c --arg ts "$TIMESTAMP" '{
        timestamp: $ts,
        event: .hook_event_name,
        session_id: .session_id,
        cwd: .cwd,
        tool_name: .tool_name,
        tool_input: .tool_input,
        tool_output: .tool_output,
        is_error: .is_error,
        raw: .
    }')
    echo "$RECORD" >> "$JSONL_FILE"

    # SQLite保存
    EVENT=$(echo "$INPUT" | jq -r '.hook_event_name // "unknown"')
    SESSION_ID=$(echo "$INPUT" | jq -r '.session_id // "unknown"')
    TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty')
    TOOL_INPUT=$(echo "$INPUT" | jq -c '.tool_input // empty')
    TOOL_OUTPUT=$(echo "$INPUT" | jq -c '.tool_output // empty' | head -c 2000)
    IS_ERROR=$(echo "$INPUT" | jq -r '.is_error // false')
else
    echo "{\"timestamp\":\"$TIMESTAMP\",\"raw\":$INPUT}" >> "$JSONL_FILE"
    EVENT="unknown"
    SESSION_ID="unknown"
    TOOL_NAME=""
    TOOL_INPUT=""
    TOOL_OUTPUT=""
    IS_ERROR="false"
fi

# SQLite初期化 & 書き込み
sqlite3 "$DB_FILE" <<SQL
CREATE TABLE IF NOT EXISTS hook_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp   TEXT NOT NULL,
    event       TEXT NOT NULL,
    session_id  TEXT,
    tool_name   TEXT,
    tool_input  TEXT,
    tool_output TEXT,
    is_error    TEXT,
    raw         TEXT
);
CREATE INDEX IF NOT EXISTS idx_hook_event ON hook_events(event);
CREATE INDEX IF NOT EXISTS idx_hook_ts ON hook_events(timestamp);
CREATE INDEX IF NOT EXISTS idx_hook_tool ON hook_events(tool_name);

INSERT INTO hook_events (timestamp, event, session_id, tool_name, tool_input, tool_output, is_error, raw)
VALUES ('$TIMESTAMP', '$EVENT', '$SESSION_ID', '$TOOL_NAME', '$TOOL_INPUT', '$TOOL_OUTPUT', '$IS_ERROR', '$(echo "$INPUT" | sed "s/'/''/g")');
SQL

# stderr にリアルタイムログ（ターミナルで見える）
echo "[HOOK $TIMESTAMP] $EVENT ${TOOL_NAME:+tool=$TOOL_NAME}" >&2

# exit 0 = Claudeの動作をブロックしない
exit 0
