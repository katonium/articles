#!/usr/bin/env python3
"""
Claude Code通信ログ クエリツール（DuckDB + JSONL版）

mitmproxyとフックロガーが書き出したJSONLファイルをDuckDBで横断的に分析する。
データは全てJSONLファイル。DuckDBのread_json_auto()で直接クエリ。

ログディレクトリ構成:
    logs/
    ├── requests.jsonl      # mitmproxy: APIリクエスト
    ├── responses.jsonl     # mitmproxy: APIレスポンス
    ├── sse_events.jsonl    # mitmproxy: SSEイベント（パース済み）
    ├── tool_calls.jsonl    # mitmproxy: tool_use / tool_result
    ├── sessions.jsonl      # mitmproxy: セッション検出
    └── hooks.jsonl         # フックロガー: フックイベント

Usage:
    python query_logs.py summary              # 全リクエスト概要
    python query_logs.py tools                # ツール呼び出し一覧
    python query_logs.py tool-ranking         # ツール頻度ランキング
    python query_logs.py sessions             # セッション一覧
    python query_logs.py session-diff         # セッション間ツール差分
    python query_logs.py system-prompt [fid]  # システムプロンプト抽出
    python query_logs.py tool-definitions [fid]  # ツール定義一覧
    python query_logs.py sse-timeline [fid]   # SSEイベント時系列
    python query_logs.py hooks                # フックイベント一覧
    python query_logs.py tokens               # トークン使用量
    python query_logs.py cache-analysis       # Prompt Caching効果
    python query_logs.py timeline             # 統合タイムライン
    python query_logs.py claude-md            # CLAUDE.md抽出
    python query_logs.py sql "SELECT ..."     # カスタムSQL
    python query_logs.py export [dir]         # Parquet出力
"""

import argparse
import json
import sys
from pathlib import Path

try:
    import duckdb
except ImportError:
    print("DuckDB is required:")
    print("  pip install duckdb")
    sys.exit(1)


# デフォルトパス
BASE_DIR = Path(__file__).parent.parent
DEFAULT_LOG_DIR = BASE_DIR / "logs"

# JONLファイル名 → ビュー名のマッピング
JSONL_FILES = {
    "requests":   "requests.jsonl",
    "responses":  "responses.jsonl",
    "sse_events": "sse_events.jsonl",
    "tool_calls": "tool_calls.jsonl",
    "sessions":   "sessions.jsonl",
    "hooks":      "hooks.jsonl",
}


def create_connection(log_dir: Path) -> duckdb.DuckDBPyConnection:
    """DuckDBコネクションを作成し、存在するJSONLファイルをビューとして登録する"""
    conn = duckdb.connect()

    for view_name, filename in JSONL_FILES.items():
        filepath = log_dir / filename
        if filepath.exists() and filepath.stat().st_size > 0:
            conn.execute(f"""
                CREATE VIEW {view_name} AS
                SELECT * FROM read_json_auto('{filepath}', format='newline_delimited')
            """)

    return conn


def has_view(conn: duckdb.DuckDBPyConnection, name: str) -> bool:
    """ビューが存在するか確認"""
    try:
        conn.execute(f"SELECT 1 FROM {name} LIMIT 0")
        return True
    except Exception:
        return False


def print_result(conn: duckdb.DuckDBPyConnection, query: str, params=None):
    """クエリ結果を整形して出力"""
    try:
        result = conn.execute(query, params or [])
        df = result.fetchdf()
        if df.empty:
            print("(no results)")
            return
        print(df.to_string(index=False, max_colwidth=60))
    except Exception as e:
        print(f"Error: {e}")


# ─── コマンド実装 ───

def cmd_summary(conn):
    if not has_view(conn, "requests"):
        print("requests.jsonl not found"); return

    join = "LEFT JOIN responses resp ON r.flow_id = resp.flow_id" if has_view(conn, "responses") else ""
    resp_cols = """
        COALESCE(resp.status_code::VARCHAR, '-') AS status,
        COALESCE(resp.input_tokens::VARCHAR, '-') AS in_tok,
        COALESCE(resp.output_tokens::VARCHAR, '-') AS out_tok,
        COALESCE(resp.cache_read_input_tokens::VARCHAR, '-') AS cache_read
    """ if has_view(conn, "responses") else """
        '-' AS status, '-' AS in_tok, '-' AS out_tok, '-' AS cache_read
    """

    print_result(conn, f"""
        SELECT
            strftime(r.timestamp::TIMESTAMP, '%H:%M:%S') AS time,
            r.flow_id[:8] AS flow,
            COALESCE(r.model, '-')[-20:] AS model,
            COALESCE(r.tools_count::VARCHAR, '-') AS tools,
            COALESCE(r.messages_count::VARCHAR, '-') AS msgs,
            CASE
                WHEN r.system_prompt_size IS NULL THEN '-'
                WHEN r.system_prompt_size < 1024 THEN r.system_prompt_size::VARCHAR || 'B'
                ELSE (r.system_prompt_size / 1024)::VARCHAR || 'KB'
            END AS sys_size,
            CASE WHEN r.has_thinking THEN 'Y' ELSE 'N' END AS think,
            CASE WHEN r.has_claude_md THEN 'Y' ELSE 'N' END AS claude_md,
            (r.body_size / 1024)::VARCHAR || 'KB' AS req_size,
            {resp_cols}
        FROM requests r
        {join}
        ORDER BY r.timestamp
    """)


def cmd_tools(conn):
    if not has_view(conn, "tool_calls"):
        print("tool_calls.jsonl not found"); return

    print_result(conn, """
        SELECT
            strftime(timestamp::TIMESTAMP, '%H:%M:%S.%g') AS time,
            CASE direction WHEN 'response' THEN '-> use' ELSE '<- result' END AS dir,
            COALESCE(tool_name, '-') AS tool,
            COALESCE(tool_use_id[:12], '-') AS id,
            COALESCE(tool_input, tool_result, '-')[:60] AS preview,
            CASE WHEN is_error THEN '!' ELSE '' END AS err
        FROM tool_calls
        ORDER BY timestamp
    """)


def cmd_tool_ranking(conn):
    if not has_view(conn, "tool_calls"):
        print("tool_calls.jsonl not found"); return

    print_result(conn, """
        SELECT
            COALESCE(tool_name, '(unknown)') AS tool,
            COUNT(*) FILTER (WHERE direction = 'response') AS calls,
            COUNT(*) FILTER (WHERE is_error) AS errors,
            MIN(strftime(timestamp::TIMESTAMP, '%H:%M:%S')) AS first,
            MAX(strftime(timestamp::TIMESTAMP, '%H:%M:%S')) AS last
        FROM tool_calls
        GROUP BY tool_name
        ORDER BY calls DESC
    """)


def cmd_sessions(conn):
    if not has_view(conn, "sessions"):
        print("sessions.jsonl not found"); return

    print_result(conn, """
        SELECT
            strftime(timestamp::TIMESTAMP, '%H:%M:%S') AS time,
            session_type AS type,
            COALESCE(model, '-')[-20:] AS model,
            system_prompt_hash AS sys_hash,
            tools_hash
        FROM sessions
        ORDER BY timestamp
    """)


def cmd_session_diff(conn):
    if not has_view(conn, "sessions"):
        print("sessions.jsonl not found"); return

    rows = conn.execute("""
        SELECT session_type, system_prompt_hash, tools_names
        FROM sessions ORDER BY timestamp
    """).fetchall()

    for session_type, sys_hash, tools_names in rows:
        names = tools_names if isinstance(tools_names, list) else []
        print(f"\n{'='*60}")
        print(f"Session: {session_type} (sys_hash={sys_hash})")
        print(f"Tools ({len(names)}): {', '.join(sorted(names))}")


def cmd_system_prompt(conn, flow_id=None):
    if not has_view(conn, "requests"):
        print("requests.jsonl not found"); return

    if flow_id:
        row = conn.execute(
            "SELECT body FROM requests WHERE flow_id LIKE ? LIMIT 1",
            [f"{flow_id}%"]
        ).fetchone()
    else:
        row = conn.execute("SELECT body FROM requests ORDER BY timestamp LIMIT 1").fetchone()

    if not row or not row[0]:
        print("No request body found"); return

    try:
        data = json.loads(row[0])
        system = data.get("system")
        if system:
            print(json.dumps(system, indent=2, ensure_ascii=False))
        else:
            print("No system prompt in this request")
    except json.JSONDecodeError:
        print("Failed to parse request body")


def cmd_tool_definitions(conn, flow_id=None):
    if not has_view(conn, "requests"):
        print("requests.jsonl not found"); return

    if flow_id:
        row = conn.execute(
            "SELECT body FROM requests WHERE flow_id LIKE ? LIMIT 1",
            [f"{flow_id}%"]
        ).fetchone()
    else:
        row = conn.execute("SELECT body FROM requests ORDER BY timestamp LIMIT 1").fetchone()

    if not row or not row[0]:
        print("No request body found"); return

    try:
        data = json.loads(row[0])
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


def cmd_claude_md(conn):
    if not has_view(conn, "requests"):
        print("requests.jsonl not found"); return

    row = conn.execute(
        "SELECT flow_id, body FROM requests WHERE has_claude_md = true ORDER BY timestamp LIMIT 1"
    ).fetchone()

    if not row:
        print("No CLAUDE.md content found in requests"); return

    flow_id, body = row
    try:
        data = json.loads(body)
        system = data.get("system", "")
        if isinstance(system, list):
            for block in system:
                text = block.get("text", "") if isinstance(block, dict) else str(block)
                if "CLAUDE.md" in text or "claude.md" in text.lower():
                    print(f"=== Found in system prompt (flow={flow_id[:8]}) ===")
                    print(text[:5000])
                    return

        for msg in data.get("messages", []):
            content = msg.get("content", "")
            content_str = json.dumps(content, ensure_ascii=False) if not isinstance(content, str) else content
            if "CLAUDE.md" in content_str:
                print(f"=== Found in messages (flow={flow_id[:8]}, role={msg.get('role')}) ===")
                print(content_str[:5000])
                return
    except json.JSONDecodeError:
        pass


def cmd_sse_timeline(conn, flow_id=None):
    if not has_view(conn, "sse_events"):
        print("sse_events.jsonl not found"); return

    where = f"WHERE flow_id LIKE '{flow_id}%'" if flow_id else ""
    limit = "" if flow_id else "LIMIT 200"

    print_result(conn, f"""
        SELECT
            strftime(timestamp::TIMESTAMP, '%H:%M:%S.%g') AS time,
            flow_id[:8] AS flow,
            event_index AS idx,
            event_type,
            COALESCE(content_type, '-') AS ctype,
            COALESCE(tool_name, '-') AS tool,
            COALESCE(text_preview, '-')[:50] AS preview
        FROM sse_events
        {where}
        ORDER BY timestamp, event_index
        {limit}
    """)


def cmd_hooks(conn):
    if not has_view(conn, "hooks"):
        print("hooks.jsonl not found"); return

    print_result(conn, """
        SELECT
            timestamp[:19] AS time,
            event,
            COALESCE(session_id[:12], '-') AS session,
            COALESCE(tool_name, '-') AS tool,
            COALESCE(is_error::VARCHAR, '-') AS error
        FROM hooks
        ORDER BY timestamp
    """)


def cmd_tokens(conn):
    if not has_view(conn, "requests") or not has_view(conn, "responses"):
        print("requests.jsonl / responses.jsonl not found"); return

    print_result(conn, """
        SELECT
            COALESCE(r.model, '-')[-25:] AS model,
            COUNT(*) AS reqs,
            SUM(resp.input_tokens) AS input,
            SUM(resp.output_tokens) AS output,
            SUM(resp.cache_creation_input_tokens) AS cache_new,
            SUM(resp.cache_read_input_tokens) AS cache_hit,
            ROUND(
                COALESCE(SUM(resp.input_tokens), 0) * 3.0 / 1e6 +
                COALESCE(SUM(resp.output_tokens), 0) * 15.0 / 1e6 +
                COALESCE(SUM(resp.cache_read_input_tokens), 0) * 0.3 / 1e6,
                4
            ) AS est_cost_usd
        FROM requests r
        LEFT JOIN responses resp ON r.flow_id = resp.flow_id
        GROUP BY r.model
    """)


def cmd_cache_analysis(conn):
    if not has_view(conn, "requests") or not has_view(conn, "responses"):
        print("requests.jsonl / responses.jsonl not found"); return

    print_result(conn, """
        SELECT
            strftime(r.timestamp::TIMESTAMP, '%H:%M:%S') AS time,
            r.flow_id[:8] AS flow,
            r.messages_count AS msgs,
            (r.body_size / 1024)::VARCHAR || 'KB' AS req_size,
            COALESCE(resp.input_tokens, 0) AS input,
            COALESCE(resp.cache_creation_input_tokens, 0) AS cache_new,
            COALESCE(resp.cache_read_input_tokens, 0) AS cache_hit,
            CASE
                WHEN COALESCE(resp.input_tokens, 0) + COALESCE(resp.cache_read_input_tokens, 0) = 0 THEN '-'
                ELSE ROUND(
                    COALESCE(resp.cache_read_input_tokens, 0) * 100.0 /
                    (COALESCE(resp.input_tokens, 0) + COALESCE(resp.cache_read_input_tokens, 0)),
                    1
                )::VARCHAR || '%'
            END AS hit_rate
        FROM requests r
        LEFT JOIN responses resp ON r.flow_id = resp.flow_id
        ORDER BY r.timestamp
    """)


def cmd_timeline(conn):
    parts = []

    if has_view(conn, "requests"):
        parts.append("""
            SELECT timestamp, '>> REQ' AS type,
                'model=' || COALESCE(model, '?') || ' tools=' || COALESCE(tools_count::VARCHAR, '?') ||
                ' msgs=' || COALESCE(messages_count::VARCHAR, '?') AS detail
            FROM requests
        """)

    if has_view(conn, "tool_calls"):
        parts.append("""
            SELECT timestamp,
                CASE direction WHEN 'response' THEN '.. TOOL>' ELSE '.. <RESULT' END AS type,
                COALESCE(tool_name, '?') || ' id=' || COALESCE(tool_use_id[:12], '?') AS detail
            FROM tool_calls
        """)

    if has_view(conn, "hooks"):
        parts.append("""
            SELECT timestamp, '** HOOK' AS type,
                event || ' ' || COALESCE(tool_name, '') AS detail
            FROM hooks
        """)

    if not parts:
        print("No data sources found"); return

    query = " UNION ALL ".join(parts)
    print_result(conn, f"""
        SELECT
            strftime(timestamp::TIMESTAMP, '%H:%M:%S.%g') AS time,
            type,
            detail[:80] AS detail
        FROM ({query})
        ORDER BY timestamp
    """)


def cmd_sql(conn, query: str):
    print_result(conn, query)


def cmd_export(conn, output_dir: str = None):
    out = Path(output_dir) if output_dir else DEFAULT_LOG_DIR / "export"
    out.mkdir(parents=True, exist_ok=True)

    for view_name in JSONL_FILES:
        if not has_view(conn, view_name):
            continue
        path = out / f"{view_name}.parquet"
        try:
            conn.execute(f"COPY {view_name} TO '{path}' (FORMAT PARQUET)")
            count = conn.execute(f"SELECT COUNT(*) FROM {view_name}").fetchone()[0]
            print(f"  {path.name}: {count} rows")
        except Exception as e:
            print(f"  {view_name}.parquet: Error - {e}")

    print(f"\nExported to: {out}")


# ─── メイン ───

def main():
    parser = argparse.ArgumentParser(
        description="Claude Code通信ログ クエリツール（DuckDB + JSONL版）",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python query_logs.py summary                  # 全リクエスト概要
  python query_logs.py timeline                 # 統合タイムライン
  python query_logs.py cache-analysis           # キャッシュ効果
  python query_logs.py sql "SELECT * FROM requests LIMIT 5"
  python query_logs.py export ./parquet_out     # Parquet出力
        """
    )
    parser.add_argument("command", choices=[
        "summary", "tools", "tool-ranking", "sessions", "session-diff",
        "system-prompt", "tool-definitions", "claude-md",
        "sse-timeline", "hooks", "tokens", "cache-analysis",
        "timeline", "sql", "export",
    ])
    parser.add_argument("args", nargs="*", default=[])
    parser.add_argument("--log-dir", type=Path, default=DEFAULT_LOG_DIR,
                        help="Directory containing JSONL log files")

    args = parser.parse_args()
    conn = create_connection(args.log_dir)

    commands = {
        "summary":          lambda: cmd_summary(conn),
        "tools":            lambda: cmd_tools(conn),
        "tool-ranking":     lambda: cmd_tool_ranking(conn),
        "sessions":         lambda: cmd_sessions(conn),
        "session-diff":     lambda: cmd_session_diff(conn),
        "system-prompt":    lambda: cmd_system_prompt(conn, args.args[0] if args.args else None),
        "tool-definitions": lambda: cmd_tool_definitions(conn, args.args[0] if args.args else None),
        "claude-md":        lambda: cmd_claude_md(conn),
        "sse-timeline":     lambda: cmd_sse_timeline(conn, args.args[0] if args.args else None),
        "hooks":            lambda: cmd_hooks(conn),
        "tokens":           lambda: cmd_tokens(conn),
        "cache-analysis":   lambda: cmd_cache_analysis(conn),
        "timeline":         lambda: cmd_timeline(conn),
        "sql":              lambda: cmd_sql(conn, args.args[0] if args.args else "SELECT 1"),
        "export":           lambda: cmd_export(conn, args.args[0] if args.args else None),
    }

    commands[args.command]()
    conn.close()


if __name__ == "__main__":
    main()
