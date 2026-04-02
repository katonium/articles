"""
mitmproxy addon: Claude Code API通信ロガー

Claude Codeの全API通信をSQLiteに記録し、SSEストリームをイベント単位でパースする。
リアルタイムでターミナルにも見やすく出力する。

Usage:
    mitmdump -s claude_logger.py --set claude_db=claude_traffic.db
"""

import json
import sqlite3
import time
import hashlib
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from mitmproxy import ctx, http, options
from mitmproxy.script import concurrent


DB_PATH = "claude_traffic.db"


def init_db(path: str) -> sqlite3.Connection:
    conn = sqlite3.connect(path, check_same_thread=False)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.executescript("""
        CREATE TABLE IF NOT EXISTS requests (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            flow_id     TEXT NOT NULL,
            timestamp   TEXT NOT NULL,
            method      TEXT NOT NULL,
            url         TEXT NOT NULL,
            host        TEXT,
            path        TEXT,
            headers     TEXT,  -- JSON
            body        TEXT,
            body_size   INTEGER,
            -- Claude API固有フィールド（パース済み）
            model              TEXT,
            system_prompt_size INTEGER,  -- システムプロンプトの文字数
            tools_count        INTEGER,  -- ツール定義の数
            messages_count     INTEGER,  -- メッセージ配列の要素数
            has_thinking       BOOLEAN,
            thinking_budget    INTEGER,
            has_claude_md      BOOLEAN,  -- CLAUDE.md内容が含まれるか
            has_fake_tools     BOOLEAN,  -- anti_distillation設定
            stream             BOOLEAN
        );

        CREATE TABLE IF NOT EXISTS responses (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            flow_id     TEXT NOT NULL,
            timestamp   TEXT NOT NULL,
            status_code INTEGER,
            headers     TEXT,  -- JSON
            body_size   INTEGER,
            -- キャッシュ関連
            cache_creation_input_tokens  INTEGER,
            cache_read_input_tokens      INTEGER,
            input_tokens                 INTEGER,
            output_tokens                INTEGER
        );

        CREATE TABLE IF NOT EXISTS sse_events (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            flow_id     TEXT NOT NULL,
            timestamp   TEXT NOT NULL,
            event_index INTEGER,  -- そのフロー内での順番
            event_type  TEXT,     -- message_start, content_block_start, etc.
            data        TEXT,     -- JSON
            -- パース済みフィールド
            content_type TEXT,    -- text, tool_use, thinking, etc.
            tool_name    TEXT,
            tool_input   TEXT,
            text_preview TEXT     -- 最初の200文字
        );

        CREATE TABLE IF NOT EXISTS tool_calls (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            flow_id     TEXT NOT NULL,
            timestamp   TEXT NOT NULL,
            direction   TEXT NOT NULL,  -- 'request' (tool_result) or 'response' (tool_use)
            tool_use_id TEXT,
            tool_name   TEXT,
            tool_input  TEXT,  -- JSON (tool_use時)
            tool_result TEXT,  -- tool_result時の内容（先頭1000文字）
            is_error    BOOLEAN
        );

        CREATE TABLE IF NOT EXISTS sessions (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            flow_id     TEXT NOT NULL,
            timestamp   TEXT NOT NULL,
            session_type TEXT,  -- 'main', 'subagent', 'team_member'
            model       TEXT,
            system_prompt_hash TEXT,  -- 同じシステムプロンプトかどうかの比較用
            tools_hash  TEXT         -- 同じツールセットかどうかの比較用
        );

        CREATE INDEX IF NOT EXISTS idx_requests_flow ON requests(flow_id);
        CREATE INDEX IF NOT EXISTS idx_requests_ts ON requests(timestamp);
        CREATE INDEX IF NOT EXISTS idx_responses_flow ON responses(flow_id);
        CREATE INDEX IF NOT EXISTS idx_sse_events_flow ON sse_events(flow_id);
        CREATE INDEX IF NOT EXISTS idx_sse_events_type ON sse_events(event_type);
        CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(tool_name);
        CREATE INDEX IF NOT EXISTS idx_tool_calls_flow ON tool_calls(flow_id);
        CREATE INDEX IF NOT EXISTS idx_sessions_type ON sessions(session_type);
    """)
    conn.commit()
    return conn


# ─── カラー出力ヘルパー ───

class C:
    RESET   = "\033[0m"
    BOLD    = "\033[1m"
    DIM     = "\033[2m"
    RED     = "\033[31m"
    GREEN   = "\033[32m"
    YELLOW  = "\033[33m"
    BLUE    = "\033[34m"
    MAGENTA = "\033[35m"
    CYAN    = "\033[36m"
    WHITE   = "\033[37m"


def ts() -> str:
    return datetime.now(timezone.utc).strftime("%H:%M:%S.%f")[:-3]


def log(icon: str, color: str, msg: str):
    ctx.log.info(f"{C.DIM}{ts()}{C.RESET} {color}{icon}{C.RESET} {msg}")


# ─── SSEパーサー ───

def parse_sse_stream(raw: bytes) -> list[dict]:
    """SSEストリームをイベントのリストにパースする"""
    events = []
    text = raw.decode("utf-8", errors="replace")
    current_event_type = None
    current_data_lines = []

    for line in text.split("\n"):
        if line.startswith("event: "):
            current_event_type = line[7:].strip()
        elif line.startswith("data: "):
            current_data_lines.append(line[6:])
        elif line == "" and (current_event_type or current_data_lines):
            data_str = "\n".join(current_data_lines)
            try:
                data = json.loads(data_str)
            except json.JSONDecodeError:
                data = {"_raw": data_str}

            events.append({
                "event_type": current_event_type or "unknown",
                "data": data,
            })
            current_event_type = None
            current_data_lines = []

    return events


def extract_sse_fields(event: dict) -> dict:
    """SSEイベントからcontent_type, tool_name等を抽出する"""
    data = event.get("data", {})
    et = event.get("event_type", "")
    fields = {
        "content_type": None,
        "tool_name": None,
        "tool_input": None,
        "text_preview": None,
    }

    if et == "content_block_start":
        cb = data.get("content_block", {})
        fields["content_type"] = cb.get("type")
        if cb.get("type") == "tool_use":
            fields["tool_name"] = cb.get("name")

    elif et == "content_block_delta":
        delta = data.get("delta", {})
        dtype = delta.get("type", "")
        if dtype == "text_delta":
            fields["content_type"] = "text"
            fields["text_preview"] = delta.get("text", "")[:200]
        elif dtype == "input_json_delta":
            fields["content_type"] = "tool_input"
            fields["tool_input"] = delta.get("partial_json", "")[:500]
        elif dtype == "thinking_delta":
            fields["content_type"] = "thinking"
            fields["text_preview"] = delta.get("thinking", "")[:200]

    elif et == "message_start":
        fields["content_type"] = "message_start"

    elif et == "message_delta":
        fields["content_type"] = "message_delta"
        usage = data.get("usage", {})
        if usage:
            fields["text_preview"] = json.dumps(usage)[:200]

    return fields


# ─── リクエスト解析 ───

def analyze_request_body(body: Optional[bytes]) -> dict:
    """Claude APIリクエストボディを解析してメタデータを抽出"""
    result = {
        "model": None,
        "system_prompt_size": None,
        "tools_count": None,
        "messages_count": None,
        "has_thinking": False,
        "thinking_budget": None,
        "has_claude_md": False,
        "has_fake_tools": False,
        "stream": False,
    }

    if not body:
        return result

    try:
        data = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return result

    result["model"] = data.get("model")
    result["stream"] = data.get("stream", False)

    # システムプロンプト
    system = data.get("system")
    if system:
        if isinstance(system, str):
            result["system_prompt_size"] = len(system)
        elif isinstance(system, list):
            total = sum(len(json.dumps(s)) for s in system)
            result["system_prompt_size"] = total
            # CLAUDE.mdの痕跡を探す
            system_text = json.dumps(system)
            if "CLAUDE.md" in system_text or "claude.md" in system_text:
                result["has_claude_md"] = True

    # ツール定義
    tools = data.get("tools")
    if tools:
        result["tools_count"] = len(tools)

    # メッセージ
    messages = data.get("messages")
    if messages:
        result["messages_count"] = len(messages)
        # メッセージ内のCLAUDE.md痕跡
        messages_text = json.dumps(messages)
        if "CLAUDE.md" in messages_text:
            result["has_claude_md"] = True

    # Thinking
    thinking = data.get("thinking")
    if thinking:
        result["has_thinking"] = True
        result["thinking_budget"] = thinking.get("budget_tokens")

    # Anti-distillation
    anti = data.get("anti_distillation")
    if anti and "fake_tools" in (anti if isinstance(anti, list) else []):
        result["has_fake_tools"] = True

    return result


def extract_tool_calls_from_request(body: Optional[bytes], flow_id: str) -> list[dict]:
    """リクエスト内のtool_result（ツール実行結果の返送）を抽出"""
    results = []
    if not body:
        return results

    try:
        data = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError):
        return results

    for msg in data.get("messages", []):
        if msg.get("role") != "user":
            continue
        content = msg.get("content", [])
        if isinstance(content, str):
            continue
        for block in content:
            if block.get("type") == "tool_result":
                result_content = block.get("content", "")
                if isinstance(result_content, list):
                    result_content = json.dumps(result_content, ensure_ascii=False)
                results.append({
                    "flow_id": flow_id,
                    "timestamp": datetime.now(timezone.utc).isoformat(),
                    "direction": "request",
                    "tool_use_id": block.get("tool_use_id"),
                    "tool_name": None,
                    "tool_input": None,
                    "tool_result": str(result_content)[:1000],
                    "is_error": block.get("is_error", False),
                })
    return results


# ─── mitmproxy Addon ───

class ClaudeLogger:
    def __init__(self):
        self.db: Optional[sqlite3.Connection] = None
        self.seen_system_hashes: set[str] = set()

    def load(self, loader):
        loader.add_option(
            "claude_db", str, DB_PATH,
            "Path to SQLite database for Claude traffic logs"
        )

    def configure(self, updated):
        if "claude_db" in updated:
            db_path = ctx.options.claude_db
            self.db = init_db(db_path)
            log("💾", C.GREEN, f"Database: {db_path}")

    def running(self):
        if not self.db:
            self.db = init_db(DB_PATH)
        log("🚀", C.GREEN, f"{C.BOLD}Claude Code Proxy Logger started{C.RESET}")
        log("📡", C.CYAN, "Waiting for Claude Code traffic...")

    def request(self, flow: http.HTTPFlow):
        """リクエストをキャプチャ"""
        if not self.db:
            return
        if "anthropic" not in (flow.request.host or "") and "claude" not in (flow.request.host or ""):
            return

        body = flow.request.get_content()
        body_str = body.decode("utf-8", errors="replace") if body else None
        meta = analyze_request_body(body)

        # セッション識別
        system_hash = None
        tools_hash = None
        if body:
            try:
                data = json.loads(body)
                system = data.get("system", "")
                system_hash = hashlib.md5(json.dumps(system).encode()).hexdigest()[:12]
                tools = data.get("tools", [])
                tools_names = sorted([t.get("name", "") for t in tools]) if tools else []
                tools_hash = hashlib.md5(json.dumps(tools_names).encode()).hexdigest()[:12]
            except Exception:
                pass

        # ログ出力
        log("⬆️ ", C.YELLOW, (
            f"{C.BOLD}REQUEST{C.RESET} → {flow.request.host}{flow.request.path} "
            f"| model={meta['model']} tools={meta['tools_count']} "
            f"msgs={meta['messages_count']} "
            f"sys_size={meta['system_prompt_size']} "
            f"thinking={meta['has_thinking']}"
        ))

        if meta["has_claude_md"]:
            log("📄", C.MAGENTA, "CLAUDE.md content detected in request")

        if meta["has_fake_tools"]:
            log("🎭", C.RED, "anti_distillation: fake_tools enabled")

        # 新しいシステムプロンプトを検出（サブエージェント判定に使う）
        if system_hash and system_hash not in self.seen_system_hashes:
            self.seen_system_hashes.add(system_hash)
            session_type = "main" if len(self.seen_system_hashes) == 1 else "subagent"
            log("🧠", C.CYAN, f"New session detected: {session_type} (sys_hash={system_hash}, tools_hash={tools_hash})")
            self.db.execute(
                "INSERT INTO sessions (flow_id, timestamp, session_type, model, system_prompt_hash, tools_hash) "
                "VALUES (?, ?, ?, ?, ?, ?)",
                (flow.id, datetime.now(timezone.utc).isoformat(), session_type,
                 meta["model"], system_hash, tools_hash)
            )

        # DB保存
        self.db.execute(
            "INSERT INTO requests "
            "(flow_id, timestamp, method, url, host, path, headers, body, body_size, "
            "model, system_prompt_size, tools_count, messages_count, has_thinking, "
            "thinking_budget, has_claude_md, has_fake_tools, stream) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (flow.id, datetime.now(timezone.utc).isoformat(),
             flow.request.method, flow.request.url, flow.request.host, flow.request.path,
             json.dumps(dict(flow.request.headers), ensure_ascii=False),
             body_str, len(body) if body else 0,
             meta["model"], meta["system_prompt_size"], meta["tools_count"],
             meta["messages_count"], meta["has_thinking"], meta["thinking_budget"],
             meta["has_claude_md"], meta["has_fake_tools"], meta["stream"])
        )

        # tool_resultの抽出
        tool_results = extract_tool_calls_from_request(body, flow.id)
        for tr in tool_results:
            self.db.execute(
                "INSERT INTO tool_calls "
                "(flow_id, timestamp, direction, tool_use_id, tool_name, tool_input, tool_result, is_error) "
                "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (tr["flow_id"], tr["timestamp"], tr["direction"], tr["tool_use_id"],
                 tr["tool_name"], tr["tool_input"], tr["tool_result"], tr["is_error"])
            )
            log("🔧", C.BLUE, f"tool_result → id={tr['tool_use_id']} err={tr['is_error']}")

        self.db.commit()

    def response(self, flow: http.HTTPFlow):
        """レスポンスをキャプチャ"""
        if not self.db:
            return
        if "anthropic" not in (flow.request.host or "") and "claude" not in (flow.request.host or ""):
            return

        resp = flow.response
        if not resp:
            return

        headers = dict(resp.headers)
        body = resp.get_content()

        # Usage情報（ヘッダーまたはSSE末尾から）
        cache_creation = None
        cache_read = None
        input_tokens = None
        output_tokens = None

        # レスポンスDB保存
        self.db.execute(
            "INSERT INTO responses "
            "(flow_id, timestamp, status_code, headers, body_size, "
            "cache_creation_input_tokens, cache_read_input_tokens, input_tokens, output_tokens) "
            "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
            (flow.id, datetime.now(timezone.utc).isoformat(),
             resp.status_code, json.dumps(headers, ensure_ascii=False),
             len(body) if body else 0,
             cache_creation, cache_read, input_tokens, output_tokens)
        )

        # SSEパース
        content_type = resp.headers.get("content-type", "")
        if "text/event-stream" in content_type and body:
            events = parse_sse_stream(body)
            log("⬇️ ", C.GREEN, f"{C.BOLD}RESPONSE{C.RESET} ← SSE stream: {len(events)} events")

            for i, event in enumerate(events):
                fields = extract_sse_fields(event)
                et = event["event_type"]

                # tool_useの抽出
                if et == "content_block_start" and fields["content_type"] == "tool_use":
                    cb = event["data"].get("content_block", {})
                    self.db.execute(
                        "INSERT INTO tool_calls "
                        "(flow_id, timestamp, direction, tool_use_id, tool_name, tool_input, tool_result, is_error) "
                        "VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                        (flow.id, datetime.now(timezone.utc).isoformat(), "response",
                         cb.get("id"), cb.get("name"), None, None, False)
                    )
                    log("🔨", C.MAGENTA, f"tool_use → {C.BOLD}{cb.get('name')}{C.RESET} id={cb.get('id')}")

                # Thinking検出
                if fields["content_type"] == "thinking" and fields["text_preview"]:
                    preview = fields["text_preview"][:80].replace("\n", " ")
                    log("💭", C.DIM, f"thinking: {preview}...")

                # テキスト出力検出
                if fields["content_type"] == "text" and fields["text_preview"]:
                    preview = fields["text_preview"][:100].replace("\n", " ")
                    log("💬", C.WHITE, f"text: {preview}")

                # Usage（message_delta内）
                if et == "message_delta":
                    usage = event["data"].get("usage", {})
                    if usage:
                        output_tokens = usage.get("output_tokens")
                        log("📊", C.CYAN, f"usage: output_tokens={output_tokens}")

                # message_start内のusage
                if et == "message_start":
                    msg = event["data"].get("message", {})
                    usage = msg.get("usage", {})
                    if usage:
                        input_tokens = usage.get("input_tokens")
                        cache_creation = usage.get("cache_creation_input_tokens")
                        cache_read = usage.get("cache_read_input_tokens")
                        log("📊", C.CYAN, (
                            f"usage: input={input_tokens} "
                            f"cache_create={cache_creation} cache_read={cache_read}"
                        ))

                # SSEイベントDB保存
                self.db.execute(
                    "INSERT INTO sse_events "
                    "(flow_id, timestamp, event_index, event_type, data, "
                    "content_type, tool_name, tool_input, text_preview) "
                    "VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                    (flow.id, datetime.now(timezone.utc).isoformat(), i,
                     et, json.dumps(event["data"], ensure_ascii=False)[:5000],
                     fields["content_type"], fields["tool_name"],
                     fields["tool_input"], fields["text_preview"])
                )

            # Usage更新
            if any(v is not None for v in [cache_creation, cache_read, input_tokens, output_tokens]):
                self.db.execute(
                    "UPDATE responses SET "
                    "cache_creation_input_tokens=?, cache_read_input_tokens=?, "
                    "input_tokens=?, output_tokens=? "
                    "WHERE flow_id=?",
                    (cache_creation, cache_read, input_tokens, output_tokens, flow.id)
                )
        else:
            log("⬇️ ", C.GREEN, f"{C.BOLD}RESPONSE{C.RESET} ← {resp.status_code} ({len(body) if body else 0} bytes)")

        self.db.commit()


addons = [ClaudeLogger()]
