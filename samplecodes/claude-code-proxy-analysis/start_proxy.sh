#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# Claude Code Proxy Analysis - 起動スクリプト
#
# mitmproxyを起動し、Claude Codeの全API通信をキャプチャする。
#
# Usage:
#   ./start_proxy.sh              # デフォルト (port 8080)
#   ./start_proxy.sh --port 9090  # カスタムポート
#
# 別ターミナルでClaude Codeを起動:
#   HTTPS_PROXY=http://localhost:8080 \
#   NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem \
#   claude
# ─────────────────────────────────────────────────────────

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="$SCRIPT_DIR/logs"
DB_FILE="$LOG_DIR/claude_traffic.db"
PORT="${1:-8080}"

mkdir -p "$LOG_DIR"

echo "╔══════════════════════════════════════════════════════════╗"
echo "║  Claude Code Proxy Analysis                             ║"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║                                                         ║"
echo "║  Proxy:  http://localhost:$PORT                          ║"
echo "║  DB:     $DB_FILE"
echo "║  Addon:  $SCRIPT_DIR/proxy/claude_logger.py"
echo "║                                                         ║"
echo "║  別ターミナルで以下を実行:                                ║"
echo "║                                                         ║"
echo "║  HTTPS_PROXY=http://localhost:$PORT \\                    ║"
echo "║  NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem \\"
echo "║  claude                                                  ║"
echo "║                                                         ║"
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# mitmproxyのCA証明書が存在するか確認
if [ ! -f "$HOME/.mitmproxy/mitmproxy-ca-cert.pem" ]; then
    echo "⚠️  mitmproxy CA証明書が見つかりません。"
    echo "   mitmproxyを一度起動すると自動生成されます。"
    echo ""
fi

# mitmdumpで起動（Webインターフェースなし、ログに集中）
exec mitmdump \
    --listen-port "$PORT" \
    --set claude_db="$DB_FILE" \
    -s "$SCRIPT_DIR/proxy/claude_logger.py" \
    --set flow_detail=0
