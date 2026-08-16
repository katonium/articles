#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# Claude Code Hook Logger - セットアップスクリプト
#
# 対象プロジェクトの .claude/settings.json にフックを設定する。
#
# Usage:
#   ./setup_hooks.sh /path/to/your/project
# ─────────────────────────────────────────────────────────

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOK_LOGGER="$SCRIPT_DIR/hooks/hook_logger.sh"

if [ $# -lt 1 ]; then
    echo "Usage: $0 <project_directory>"
    echo ""
    echo "Example: $0 ~/my-todo-app"
    exit 1
fi

PROJECT_DIR="$1"
CLAUDE_DIR="$PROJECT_DIR/.claude"
SETTINGS_FILE="$CLAUDE_DIR/settings.json"

mkdir -p "$CLAUDE_DIR"

# settings.jsonが既にある場合はバックアップ
if [ -f "$SETTINGS_FILE" ]; then
    cp "$SETTINGS_FILE" "$SETTINGS_FILE.bak.$(date +%s)"
    echo "📦 既存のsettings.jsonをバックアップしました"
fi

# テンプレートからsettings.jsonを生成
sed "s|HOOK_LOGGER_PATH|$HOOK_LOGGER|g" \
    "$SCRIPT_DIR/hooks/settings.template.json" > "$SETTINGS_FILE"

echo "✅ フック設定完了: $SETTINGS_FILE"
echo ""
echo "設定されたフック:"
echo "  - PreToolUse   (ツール実行前)"
echo "  - PostToolUse  (ツール実行後)"
echo "  - Stop         (応答完了時)"
echo "  - SubagentStart (サブエージェント起動時)"
echo "  - SubagentStop  (サブエージェント終了時)"
echo "  - SessionStart  (セッション開始時)"
echo "  - UserPromptSubmit (プロンプト送信前)"
echo ""
echo "ログ出力先: $SCRIPT_DIR/logs/"
echo ""
echo "確認: cat $SETTINGS_FILE"
