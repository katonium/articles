#!/usr/bin/env python3
"""
Claude Code通信ログ クエリツール

mitmproxyのSQLiteログとフックのSQLiteログを横断的にクエリする。
Cloud Logging風の柔軟なクエリを目指す。

Usage:
    # 全リクエストの概要
    python query_logs.py summary

    # ツール呼び出し一覧
    python query_logs.py tools

    # セッション一覧（サブエージェント検出）
    python query_logs.py sessions

    # システムプロンプトの抽出
    python query_logs.py system-prompt [flow_id]

    # 特定ツールの呼び出し詳細
    python query_logs.py tool-detail <tool_name>

    # SSEイベントの時系列
    python query_logs.py sse-timeline [flow_id]

    # フックイベント一覧
    python query_logs.py hooks

    # トークン使用量サマリー
    python query_logs.py tokens

    # 通信タイムライン（プロキシ+フック統合）
    python query_logs.py timeline

    # カスタムSQL
    python query_logs.py sql "SELECT * FROM requests WHERE model LIKE '%opus%'"

    # CLAUDE.mdの内容を抽出
    python query_logs.py claude-md

    # ツール定義一覧を抽出
    python query_logs.py tool-definitions [flow_id]
"""

import argparse
import json
import sqlite3
import sys
from pathlib import Path
from datetime import datetime


# デフォルトパス
DEFAULT_PROXY_DB = Path(__file__).parent.parent / "logs" / "claude_traffic.db"
DEFAULT_HOOK_DB = Path(__file__).parent.parent / "logs" / "hooks.db"


def connect(db_path: Path) -> sqlite3.Connection | None:
    if not db_path.exists():
        return None
    conn = sqlite3.connect(str(db_path))
    conn.row_factory = sqlite3.Row
    return conn


def fmt_size(n: int | None) -> str:
    if n is None:
        return "-"
    if n < 1024:
        return f"{n}B"
    if n < 1024 * 1024:
        return f"{n/1024:.1f}KB"
    return f"{n/1024/1024:.1f}MB"


def fmt_ts(ts: str | None) -> str:
    if not ts:
        return "-"
    try:
        dt = datetime.fromisoformat(ts.replace("Z", "+00:00"))
        return dt.strftime("%H:%M:%S.%f")[:-3]
    except Exception:
        return ts[:12]


def print_table(headers: list[str], rows: list[list]):
    if not rows:
        print("(no results)")
        return
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(str(cell)))

    header_line = " | ".join(h.ljust(widths[i]) for i, h in enumerate(headers))
    print(header_line)
    print("-+-".join("-" * w for w in widths))
    for row in rows:
        print(" | ".join(str(cell).ljust(widths[i]) for i, cell in enumerate(row)))


# ─── コマンド実装 ───

def cmd_summary(proxy_db, hook_db):
    """全リクエストの概要"""
    if not proxy_db:
        print("Proxy DB not found"); return

    rows = proxy_db.execute("""
        SELECT
            r.flow_id,
            r.timestamp,
            r.model,
            r.tools_count,
            r.messages_count,
            r.system_prompt_size,
            r.has_thinking,
            r.has_claude_md,
            r.body_size,
            resp.status_code,
            resp.input_tokens,
            resp.output_tokens,
            resp.cache_read_input_tokens
        FROM requests r
        LEFT JOIN responses resp ON r.flow_id = resp.flow_id
        ORDER BY r.timestamp
    """).fetchall()

    print_table(
        ["Time", "Flow", "Model", "Tools", "Msgs", "SysSize", "Think", "CLAUDE.md",
         "ReqSize", "Status", "InTok", "OutTok", "CacheRead"],
        [[
            fmt_ts(r["timestamp"]), r["flow_id"][:8],
            (r["model"] or "-")[-20:], r["tools_count"] or "-",
            r["messages_count"] or "-", fmt_size(r["system_prompt_size"]),
            "Y" if r["has_thinking"] else "N",
            "Y" if r["has_claude_md"] else "N",
            fmt_size(r["body_size"]), r["status_code"] or "-",
            r["input_tokens"] or "-", r["output_tokens"] or "-",
            r["cache_read_input_tokens"] or "-",
        ] for r in rows]
    )


def cmd_tools(proxy_db, hook_db):
    """ツール呼び出し一覧"""
    if not proxy_db:
        print("Proxy DB not found"); return

    rows = proxy_db.execute("""
        SELECT timestamp, direction, tool_name, tool_use_id, tool_input, tool_result, is_error
        FROM tool_calls
        ORDER BY timestamp
    """).fetchall()

    print_table(
        ["Time", "Dir", "Tool", "ID", "Input/Result (preview)", "Err"],
        [[
            fmt_ts(r["timestamp"]),
            "→" if r["direction"] == "response" else "←",
            r["tool_name"] or "-",
            (r["tool_use_id"] or "-")[:12],
            (r["tool_input"] or r["tool_result"] or "-")[:60],
            "!" if r["is_error"] else "",
        ] for r in rows]
    )


def cmd_sessions(proxy_db, hook_db):
    """セッション一覧（サブエージェント検出）"""
    if not proxy_db:
        print("Proxy DB not found"); return

    rows = proxy_db.execute("""
        SELECT timestamp, session_type, model, system_prompt_hash, tools_hash,
            (SELECT COUNT(*) FROM requests WHERE requests.flow_id >= sessions.flow_id) as req_count
        FROM sessions
        ORDER BY timestamp
    """).fetchall()

    print_table(
        ["Time", "Type", "Model", "SysHash", "ToolsHash", "Reqs"],
        [[
            fmt_ts(r["timestamp"]), r["session_type"], (r["model"] or "-")[-20:],
            r["system_prompt_hash"] or "-", r["tools_hash"] or "-", r["req_count"],
        ] for r in rows]
    )


def cmd_system_prompt(proxy_db, hook_db, flow_id=None):
    """システムプロンプトの抽出"""
    if not proxy_db:
        print("Proxy DB not found"); return

    if flow_id:
        row = proxy_db.execute("SELECT body FROM requests WHERE flow_id LIKE ?", (f"{flow_id}%",)).fetchone()
    else:
        row = proxy_db.execute("SELECT body FROM requests ORDER BY timestamp LIMIT 1").fetchone()

    if not row or not row["body"]:
        print("No request body found"); return

    try:
        data = json.loads(row["body"])
        system = data.get("system")
        if system:
            print(json.dumps(system, indent=2, ensure_ascii=False))
        else:
            print("No system prompt in this request")
    except json.JSONDecodeError:
        print("Failed to parse request body")


def cmd_tool_definitions(proxy_db, hook_db, flow_id=None):
    """ツール定義一覧の抽出"""
    if not proxy_db:
        print("Proxy DB not found"); return

    if flow_id:
        row = proxy_db.execute("SELECT body FROM requests WHERE flow_id LIKE ?", (f"{flow_id}%",)).fetchone()
    else:
        row = proxy_db.execute("SELECT body FROM requests ORDER BY timestamp LIMIT 1").fetchone()

    if not row or not row["body"]:
        print("No request body found"); return

    try:
        data = json.loads(row["body"])
        tools = data.get("tools", [])
        print(f"Total tools: {len(tools)}\n")
        for i, tool in enumerate(tools):
            name = tool.get("name", "?")
            desc = (tool.get("description") or "")[:80]
            params = list((tool.get("input_schema", {}).get("properties", {})).keys())
            print(f"  [{i+1:3d}] {name}")
            print(f"        {desc}")
            print(f"        params: {', '.join(params)}")
            print()
    except json.JSONDecodeError:
        print("Failed to parse request body")


def cmd_claude_md(proxy_db, hook_db):
    """CLAUDE.mdの内容を探す"""
    if not proxy_db:
        print("Proxy DB not found"); return

    rows = proxy_db.execute(
        "SELECT flow_id, body FROM requests WHERE has_claude_md = 1 ORDER BY timestamp LIMIT 1"
    ).fetchall()

    if not rows:
        print("No CLAUDE.md content found in requests"); return

    for row in rows:
        try:
            data = json.loads(row["body"])
            # systemプロンプト内を探す
            system = data.get("system", "")
            if isinstance(system, list):
                for block in system:
                    text = block.get("text", "") if isinstance(block, dict) else str(block)
                    if "CLAUDE.md" in text or "claude.md" in text.lower():
                        print(f"=== Found in system prompt (flow={row['flow_id'][:8]}) ===")
                        print(text[:3000])
                        print("...")
                        return

            # メッセージ内を探す
            for msg in data.get("messages", []):
                content = msg.get("content", "")
                content_str = json.dumps(content) if not isinstance(content, str) else content
                if "CLAUDE.md" in content_str:
                    print(f"=== Found in messages (flow={row['flow_id'][:8]}, role={msg.get('role')}) ===")
                    print(content_str[:3000])
                    print("...")
                    return
        except json.JSONDecodeError:
            pass


def cmd_sse_timeline(proxy_db, hook_db, flow_id=None):
    """SSEイベントの時系列"""
    if not proxy_db:
        print("Proxy DB not found"); return

    if flow_id:
        rows = proxy_db.execute(
            "SELECT * FROM sse_events WHERE flow_id LIKE ? ORDER BY event_index",
            (f"{flow_id}%",)
        ).fetchall()
    else:
        rows = proxy_db.execute(
            "SELECT * FROM sse_events ORDER BY timestamp, event_index LIMIT 200"
        ).fetchall()

    print_table(
        ["Time", "Flow", "#", "EventType", "ContentType", "Tool", "Preview"],
        [[
            fmt_ts(r["timestamp"]), r["flow_id"][:8], r["event_index"],
            r["event_type"], r["content_type"] or "-",
            r["tool_name"] or "-", (r["text_preview"] or "-")[:50],
        ] for r in rows]
    )


def cmd_hooks(proxy_db, hook_db):
    """フックイベント一覧"""
    if not hook_db:
        print("Hook DB not found"); return

    rows = hook_db.execute(
        "SELECT * FROM hook_events ORDER BY timestamp"
    ).fetchall()

    print_table(
        ["Time", "Event", "Session", "Tool", "Error"],
        [[
            fmt_ts(r["timestamp"]), r["event"], (r["session_id"] or "-")[:12],
            r["tool_name"] or "-", r["is_error"] or "-",
        ] for r in rows]
    )


def cmd_tokens(proxy_db, hook_db):
    """トークン使用量サマリー"""
    if not proxy_db:
        print("Proxy DB not found"); return

    rows = proxy_db.execute("""
        SELECT
            r.model,
            COUNT(*) as requests,
            SUM(resp.input_tokens) as total_input,
            SUM(resp.output_tokens) as total_output,
            SUM(resp.cache_creation_input_tokens) as total_cache_create,
            SUM(resp.cache_read_input_tokens) as total_cache_read
        FROM requests r
        LEFT JOIN responses resp ON r.flow_id = resp.flow_id
        GROUP BY r.model
    """).fetchall()

    print_table(
        ["Model", "Requests", "InputTok", "OutputTok", "CacheCreate", "CacheRead"],
        [[
            (r["model"] or "-")[-25:], r["requests"],
            r["total_input"] or "-", r["total_output"] or "-",
            r["total_cache_create"] or "-", r["total_cache_read"] or "-",
        ] for r in rows]
    )


def cmd_timeline(proxy_db, hook_db):
    """通信タイムライン（プロキシ+フック統合）"""
    events = []

    if proxy_db:
        for r in proxy_db.execute("SELECT timestamp, 'REQ' as type, model, tools_count, messages_count FROM requests").fetchall():
            events.append((r["timestamp"], "⬆️  REQ", f"model={r['model']} tools={r['tools_count']} msgs={r['messages_count']}"))
        for r in proxy_db.execute("SELECT timestamp, direction, tool_name, tool_use_id FROM tool_calls").fetchall():
            icon = "🔨 TOOL→" if r["direction"] == "response" else "🔧 ←RESULT"
            events.append((r["timestamp"], icon, f"{r['tool_name'] or '?'} id={r['tool_use_id'][:12] if r['tool_use_id'] else '?'}"))

    if hook_db:
        for r in hook_db.execute("SELECT timestamp, event, tool_name FROM hook_events").fetchall():
            events.append((r["timestamp"], f"🪝 {r['event']}", r["tool_name"] or ""))

    events.sort(key=lambda x: x[0])

    print_table(
        ["Time", "Type", "Detail"],
        [[fmt_ts(e[0]), e[1], e[2][:80]] for e in events]
    )


def cmd_sql(proxy_db, hook_db, query: str):
    """カスタムSQL実行"""
    db = proxy_db or hook_db
    if not db:
        print("No DB found"); return

    try:
        rows = db.execute(query).fetchall()
        if not rows:
            print("(no results)"); return
        headers = rows[0].keys()
        print_table(
            list(headers),
            [[str(r[h])[:60] for h in headers] for r in rows]
        )
    except sqlite3.Error as e:
        print(f"SQL Error: {e}")


# ─── メイン ───

def main():
    parser = argparse.ArgumentParser(description="Claude Code通信ログ クエリツール")
    parser.add_argument("command", choices=[
        "summary", "tools", "sessions", "system-prompt", "tool-definitions",
        "claude-md", "sse-timeline", "hooks", "tokens", "timeline",
        "tool-detail", "sql",
    ])
    parser.add_argument("args", nargs="*", default=[])
    parser.add_argument("--proxy-db", type=Path, default=DEFAULT_PROXY_DB)
    parser.add_argument("--hook-db", type=Path, default=DEFAULT_HOOK_DB)

    args = parser.parse_args()
    proxy_db = connect(args.proxy_db)
    hook_db = connect(args.hook_db)

    commands = {
        "summary": lambda: cmd_summary(proxy_db, hook_db),
        "tools": lambda: cmd_tools(proxy_db, hook_db),
        "sessions": lambda: cmd_sessions(proxy_db, hook_db),
        "system-prompt": lambda: cmd_system_prompt(proxy_db, hook_db, args.args[0] if args.args else None),
        "tool-definitions": lambda: cmd_tool_definitions(proxy_db, hook_db, args.args[0] if args.args else None),
        "claude-md": lambda: cmd_claude_md(proxy_db, hook_db),
        "sse-timeline": lambda: cmd_sse_timeline(proxy_db, hook_db, args.args[0] if args.args else None),
        "hooks": lambda: cmd_hooks(proxy_db, hook_db),
        "tokens": lambda: cmd_tokens(proxy_db, hook_db),
        "timeline": lambda: cmd_timeline(proxy_db, hook_db),
        "tool-detail": lambda: cmd_tools(proxy_db, hook_db),  # 同じだけどフィルタ追加予定
        "sql": lambda: cmd_sql(proxy_db, hook_db, args.args[0] if args.args else "SELECT 1"),
    }

    commands[args.command]()

    if proxy_db:
        proxy_db.close()
    if hook_db:
        hook_db.close()


if __name__ == "__main__":
    main()
