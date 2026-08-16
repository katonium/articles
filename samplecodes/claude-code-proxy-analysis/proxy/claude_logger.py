"""
mitmproxy addon: Claude Code API通信ロガー

Claude Codeの全API通信をJSONLファイルに記録し、SSEストリームをイベント単位でパースする。
リアルタイムでターミナルにも見やすく出力する。

書き込み: JSONL (1行1JSON、appendのみ)
分析:    DuckDBでread_json_auto()して横断クエリ

Usage:
    mitmdump -s claude_logger.py --set claude_log_dir=./logs
"""

import json
import hashlib
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

from mitmproxy import ctx, http


DEFAULT_LOG_DIR = str(Path(__file__).parent.parent / "logs")


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


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def log(icon: str, color: str, msg: str):
    ctx.log.info(f"{C.DIM}{ts()}{C.RESET} {color}{icon}{C.RESET} {msg}")


# ─── JSONL書き込み ───

class JsonlWriter:
    """ファイル別のJSONLライター。appendモードで1行ずつ書き込む。"""

    def __init__(self, log_dir: str):
        self.log_dir = Path(log_dir)
        self.log_dir.mkdir(parents=True, exist_ok=True)
        self._files: dict[str, Path] = {}

    def _path(self, name: str) -> Path:
        if name not in self._files:
            self._files[name] = self.log_dir / f"{name}.jsonl"
        return self._files[name]

    def write(self, name: str, record: dict):
        """JSONLファイルに1レコード追記"""
        with open(self._path(name), "a", encoding="utf-8") as f:
            f.write(json.dumps(record, ensure_ascii=False, default=str) + "\n")


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
        "tools_names": None,
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
            system_text = json.dumps(system)
            if "CLAUDE.md" in system_text or "claude.md" in system_text:
                result["has_claude_md"] = True

    # ツール定義
    tools = data.get("tools")
    if tools:
        result["tools_count"] = len(tools)
        result["tools_names"] = [t.get("name", "?") for t in tools]

    # メッセージ
    messages = data.get("messages")
    if messages:
        result["messages_count"] = len(messages)
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


# ─── mitmproxy Addon ───

class ClaudeLogger:
    def __init__(self):
        self.writer: Optional[JsonlWriter] = None
        self.seen_system_hashes: set[str] = set()

    def load(self, loader):
        loader.add_option(
            "claude_log_dir", str, DEFAULT_LOG_DIR,
            "Directory for JSONL log files"
        )

    def configure(self, updated):
        if "claude_log_dir" in updated:
            log_dir = ctx.options.claude_log_dir
            self.writer = JsonlWriter(log_dir)
            log("📁", C.GREEN, f"Log directory: {log_dir}")

    def running(self):
        if not self.writer:
            self.writer = JsonlWriter(DEFAULT_LOG_DIR)
        log("🚀", C.GREEN, f"{C.BOLD}Claude Code Proxy Logger started{C.RESET}")
        log("📡", C.CYAN, "Waiting for Claude Code traffic...")

    def request(self, flow: http.HTTPFlow):
        """リクエストをキャプチャ"""
        if not self.writer:
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

        # 新しいセッション検出
        session_type = None
        if system_hash and system_hash not in self.seen_system_hashes:
            self.seen_system_hashes.add(system_hash)
            session_type = "main" if len(self.seen_system_hashes) == 1 else "subagent"
            log("🧠", C.CYAN, f"New session detected: {session_type} (sys_hash={system_hash}, tools_hash={tools_hash})")
            self.writer.write("sessions", {
                "timestamp": now_iso(),
                "flow_id": flow.id,
                "session_type": session_type,
                "model": meta["model"],
                "system_prompt_hash": system_hash,
                "tools_hash": tools_hash,
                "tools_names": meta["tools_names"],
            })

        # リクエスト記録
        self.writer.write("requests", {
            "timestamp": now_iso(),
            "flow_id": flow.id,
            "method": flow.request.method,
            "url": flow.request.url,
            "host": flow.request.host,
            "path": flow.request.path,
            "headers": dict(flow.request.headers),
            "body": body_str,
            "body_size": len(body) if body else 0,
            "model": meta["model"],
            "system_prompt_size": meta["system_prompt_size"],
            "tools_count": meta["tools_count"],
            "tools_names": meta["tools_names"],
            "messages_count": meta["messages_count"],
            "has_thinking": meta["has_thinking"],
            "thinking_budget": meta["thinking_budget"],
            "has_claude_md": meta["has_claude_md"],
            "has_fake_tools": meta["has_fake_tools"],
            "stream": meta["stream"],
            "system_prompt_hash": system_hash,
            "tools_hash": tools_hash,
        })

        # tool_result抽出
        if body:
            try:
                data = json.loads(body)
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
                            self.writer.write("tool_calls", {
                                "timestamp": now_iso(),
                                "flow_id": flow.id,
                                "direction": "request",
                                "tool_use_id": block.get("tool_use_id"),
                                "tool_name": None,
                                "tool_input": None,
                                "tool_result": str(result_content)[:2000],
                                "is_error": block.get("is_error", False),
                            })
                            log("🔧", C.BLUE, f"tool_result → id={block.get('tool_use_id')} err={block.get('is_error', False)}")
            except (json.JSONDecodeError, UnicodeDecodeError):
                pass

    def response(self, flow: http.HTTPFlow):
        """レスポンスをキャプチャ"""
        if not self.writer:
            return
        if "anthropic" not in (flow.request.host or "") and "claude" not in (flow.request.host or ""):
            return

        resp = flow.response
        if not resp:
            return

        headers = dict(resp.headers)
        body = resp.get_content()

        # Usage情報
        usage_data = {
            "cache_creation_input_tokens": None,
            "cache_read_input_tokens": None,
            "input_tokens": None,
            "output_tokens": None,
        }

        # SSEパース
        content_type = resp.headers.get("content-type", "")
        sse_event_count = 0

        if "text/event-stream" in content_type and body:
            events = parse_sse_stream(body)
            sse_event_count = len(events)
            log("⬇️ ", C.GREEN, f"{C.BOLD}RESPONSE{C.RESET} ← SSE stream: {len(events)} events")

            for i, event in enumerate(events):
                fields = extract_sse_fields(event)
                et = event["event_type"]

                # tool_useの抽出
                if et == "content_block_start" and fields["content_type"] == "tool_use":
                    cb = event["data"].get("content_block", {})
                    self.writer.write("tool_calls", {
                        "timestamp": now_iso(),
                        "flow_id": flow.id,
                        "direction": "response",
                        "tool_use_id": cb.get("id"),
                        "tool_name": cb.get("name"),
                        "tool_input": None,
                        "tool_result": None,
                        "is_error": False,
                    })
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
                        usage_data["output_tokens"] = usage.get("output_tokens")
                        log("📊", C.CYAN, f"usage: output_tokens={usage_data['output_tokens']}")

                # message_start内のusage
                if et == "message_start":
                    msg = event["data"].get("message", {})
                    usage = msg.get("usage", {})
                    if usage:
                        usage_data["input_tokens"] = usage.get("input_tokens")
                        usage_data["cache_creation_input_tokens"] = usage.get("cache_creation_input_tokens")
                        usage_data["cache_read_input_tokens"] = usage.get("cache_read_input_tokens")
                        log("📊", C.CYAN, (
                            f"usage: input={usage_data['input_tokens']} "
                            f"cache_create={usage_data['cache_creation_input_tokens']} "
                            f"cache_read={usage_data['cache_read_input_tokens']}"
                        ))

                # SSEイベント記録
                self.writer.write("sse_events", {
                    "timestamp": now_iso(),
                    "flow_id": flow.id,
                    "event_index": i,
                    "event_type": et,
                    "data": event["data"],
                    "content_type": fields["content_type"],
                    "tool_name": fields["tool_name"],
                    "tool_input": fields["tool_input"],
                    "text_preview": fields["text_preview"],
                })
        else:
            log("⬇️ ", C.GREEN, f"{C.BOLD}RESPONSE{C.RESET} ← {resp.status_code} ({len(body) if body else 0} bytes)")

        # レスポンス記録
        self.writer.write("responses", {
            "timestamp": now_iso(),
            "flow_id": flow.id,
            "status_code": resp.status_code,
            "headers": headers,
            "body_size": len(body) if body else 0,
            "sse_event_count": sse_event_count,
            **usage_data,
        })


addons = [ClaudeLogger()]
