#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# Claude Code Hook Blocker（実験用）
#
# 特定のツールや条件でツール実行をブロックする実験用フック。
# exit 2で返すとClaude Codeはそのツール実行をキャンセルする。
# stderrの内容がClaude側にフィードバックされる。
#
# 環境変数 BLOCK_TOOLS で対象ツールを指定（カンマ区切り）
# 例: BLOCK_TOOLS="Bash,Write" ./hook_blocker.sh
# ─────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="${CLAUDE_HOOK_LOG_DIR:-$SCRIPT_DIR/../logs}"
JSONL_FILE="$LOG_DIR/blocked.jsonl"

mkdir -p "$LOG_DIR"

INPUT=$(cat)
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%S.%3NZ")

TOOL_NAME=""
if command -v jq &>/dev/null; then
    TOOL_NAME=$(echo "$INPUT" | jq -r '.tool_name // ""')
fi

# ブロック対象のツールかチェック
BLOCK_TOOLS="${BLOCK_TOOLS:-}"
if [ -n "$BLOCK_TOOLS" ] && [ -n "$TOOL_NAME" ]; then
    IFS=',' read -ra TARGETS <<< "$BLOCK_TOOLS"
    for target in "${TARGETS[@]}"; do
        if [ "$TOOL_NAME" = "$target" ]; then
            # ログ記録
            echo "{\"timestamp\":\"$TIMESTAMP\",\"action\":\"blocked\",\"tool\":\"$TOOL_NAME\",\"raw\":$INPUT}" >> "$JSONL_FILE"
            # stderrがClaudeへのフィードバックになる
            echo "🚫 Tool '$TOOL_NAME' is blocked by experiment hook. Please use an alternative approach." >&2
            exit 2  # ブロック
        fi
    done
fi

# ブロック対象でなければ通過
exit 0
