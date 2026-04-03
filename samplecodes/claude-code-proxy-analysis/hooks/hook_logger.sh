#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# Claude Code Hook Logger
#
# 全フックイベントをJSONLファイルに記録する。
# stdinからJSON入力を受け取り、タイムスタンプを付与して保存。
# exit 0で返すので、Claude Codeの動作には一切干渉しない。
#
# 出力先: $CLAUDE_HOOK_LOG_DIR/hooks.jsonl (デフォルト: ../logs/)
# 分析:   DuckDBでread_json_auto('hooks.jsonl')してクエリ
# ─────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="${CLAUDE_HOOK_LOG_DIR:-$SCRIPT_DIR/../logs}"
JSONL_FILE="$LOG_DIR/hooks.jsonl"

mkdir -p "$LOG_DIR"

# stdinからJSON入力を読み取り
INPUT=$(cat)

TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ")

# jqがあれば構造化して保存、なければ生JSONにタイムスタンプだけ付与
if command -v jq &>/dev/null; then
    echo "$INPUT" | jq -c --arg ts "$TIMESTAMP" '{
        timestamp: $ts,
        event: .hook_event_name,
        session_id: .session_id,
        cwd: .cwd,
        tool_name: .tool_name,
        tool_input: .tool_input,
        tool_output: .tool_output,
        is_error: .is_error,
        raw: .
    }' >> "$JSONL_FILE"

    EVENT=$(echo "$INPUT" | jq -r '.hook_event_name // "unknown"')
    TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // empty')
else
    echo "{\"timestamp\":\"$TIMESTAMP\",\"raw\":$INPUT}" >> "$JSONL_FILE"
    EVENT="unknown"
    TOOL_NAME=""
fi

# stderrにリアルタイムログ（ターミナルで見える）
echo "[HOOK $TIMESTAMP] $EVENT ${TOOL_NAME:+tool=$TOOL_NAME}" >&2

# exit 0 = Claudeの動作をブロックしない
exit 0
