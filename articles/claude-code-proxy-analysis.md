---
title: "Claude Codeの通信を傍受して、Anthropicの設計判断を逆読みする"
emoji: "🔬"
type: "tech"
topics: ["ClaudeCode", "AIエージェント", "アーキテクチャ", "mitmproxy"]
published: false
---

こんにちは、 [@Katonium0615](https://x.com/Katonium0615) です。[Japan Google Cloud Usergroup for Enterprise (Jagu'e'r)](https://jaguer.jp/) でエバンジェリストをしています。

## はじめに

Claude Codeって便利ですよね。「Todoアプリ作って」と言えば、ファイルを読み、コードを書き、テストを走らせ、コミットまでしてくれる。

でも、ふと思ったんです。

**「こいつ、裏でどんな設計判断の上に立っているんだ？」**

ツールを呼ぶとき、考えるとき、サブエージェントを生むとき——それぞれの瞬間にどんな通信が飛び、どんな情報がやりとりされているのか。そしてその通信パターンの背後には、Anthropicのエンジニアたちのどんな設計判断が隠れているのか。

というわけで、**mitmproxyでClaude Codeの全通信を傍受し、フックで内部イベントをキャプチャし、セッションファイルを解析して、Anthropicの設計判断を逆読みする**——そんな実験をしてみました。

:::message
本記事ではClaude Codeが公式にサポートしているHTTP_PROXY機能を使用しています。企業ネットワークでの利用を想定して提供されている正規の機能です。
:::

## 観測手法

3つの角度からClaude Codeを観測します。

```
┌──────────────┐     HTTPS_PROXY      ┌──────────────┐     HTTPS      ┌────────────────┐
│  Claude Code │ ──────────────────→  │  mitmproxy   │ ────────────→ │ api.anthropic  │
│  (CLI)       │                      │  (localhost   │               │ .com           │
│              │ ←──────────────────  │   :8080)     │ ←──────────── │                │
└──────┬───────┘     SSE Stream       └──────┬───────┘               └────────────────┘
       │                                     │
       │ ① hooks (stdin→stdout)              │ ② JSONL logs
       ▼                                     ▼
┌──────────────┐                      ┌──────────────┐
│  Hook Logger │──→ hooks.jsonl       │  requests    │
│  (bash)      │                      │  responses   │   → DuckDBで
└──────────────┘                      │  sse_events  │     横断クエリ
                                      │  tool_calls  │
       ③ Session Files                │  sessions    │
       ~/.claude/projects/            └──────────────┘
         └── <session-id>.jsonl
```

1. **mitmproxy**: 全API通信をJSONLに記録。SSEストリームをイベント単位でパース
2. **Hook Logger**: フックイベント（ツール実行前後、セッション開始等）をJSONLに記録
3. **Session Files**: `~/.claude/projects/` に保存されるセッション履歴（JSONL形式）を分析

分析には**DuckDB**を使用。全JOINLファイルを`read_json_auto()`で横断クエリできます。


## 設計判断①: なぜREST + SSEなのか——プロトコル選択の意味

<!-- TODO: 実際のリクエスト/レスポンスヘッダーを貼る -->

まず目を引くのが、Claude APIの通信プロトコルです。

| 選択肢 | 特徴 | Anthropicの判断 |
|---|---|---|
| **gRPC** | HTTP/2必須、バイナリ、高スループット | ❌ 不採用 |
| **WebSocket** | 双方向、ステートフル接続 | ❌ 不採用 |
| **REST + SSE** | HTTPベース、ステートレス、テキスト | ✅ 採用 |

Google CloudのVertex AI APIがgRPCを採用しているのとは**真逆の設計判断**です。

なぜか？ mitmproxyを通すとその理由が身をもってわかります——**HTTP_PROXYを設定するだけで全通信が傍受できる**。gRPCやWebSocketではこうはいきません。企業ネットワークでの到達性（reachability）を最優先した設計です。

<!-- TODO: HTTP/1.1 vs HTTP/2のどちらで通信しているか確認 -->
<!-- TODO: keep-aliveの挙動を確認 -->
<!-- TODO: TCPコネクションの再利用パターンを確認 -->

> **アーキテクト的に言い換えると**: CDNのキャッシュキー設計と同じ思想。「エンドポイントまでの経路上にある全てのインフラと互換性を持つ」ことを最優先している。


## 設計判断②: なぜ毎回数万トークンを送るのか——Prompt Cachingの設計

「Todoアプリを作って」——この一言を送信した瞬間、リクエスト全体は **XXX KB** にもなります。

```json
{
  "model": "claude-opus-4-6",
  "system": [...],        // 数万文字のシステムプロンプト
  "tools": [...],         // XX個のツール定義（JSON Schema）
  "messages": [...],      // 会話履歴
  "stream": true,
  "thinking": { "type": "enabled", "budget_tokens": XXXXX }
}
```

<!-- TODO: 実際のリクエストサイズ、ツール数、システムプロンプトサイズを記載 -->
<!-- TODO: fake_tools / cchハッシュの有無を確認 -->

たった一言のプロンプトに数万トークンの「荷物」がついてくる。REST APIはステートレスだから、**毎回全部送るしかない**。

しかし2回目以降のリクエストでは：

```
📊 usage: input=XXXX cache_create=0 cache_read=XXXXX
```

<!-- TODO: 実際のキャッシュヒット率を記載 -->
<!-- TODO: cache-analysisコマンドの結果を貼る -->

**Prompt Cachingが効いている**。公式ドキュメントによれば、キャッシュは`tools → system → messages`の順に先頭一致で評価される[^1]。つまりシステムプロンプトとツール定義を先頭に固定することで、後続のmessagesが変わってもキャッシュがヒットする。

> **アーキテクト的に言い換えると**: これはまさにCDNのprefix matchキャッシュと同じ発想。URL（≒リクエスト先頭）を安定させて、クエリパラメータ（≒メッセージ）が変わってもキャッシュヒットさせる。「ステートレスAPIのコスト問題をキャッシュ層で解決する」というのは、Webアーキテクチャで何十年もやってきたことと同じです。

[^1]: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching


## 設計判断③: なぜサブエージェントは親のコンテキストを引き継がないのか

Claudeが「これは調査が必要だ」と判断すると、`Agent`ツールを呼び出してサブエージェントを生成します。

```
🔨 tool_use → Agent  id=toolu_01XXXXX
🧠 New session detected: subagent (sys_hash=abc123, tools_hash=def456)
```

<!-- TODO: sessions / session-diff コマンドの結果を貼る -->
<!-- TODO: メインとサブエージェントのツール定義差分を見せる -->
<!-- TODO: サブエージェントのmessages_countが1であることを確認 -->

サブエージェントは**完全に独立したAPIリクエスト系列**です。親の会話履歴は一切含まれない。

なぜ？ Anthropicはこの判断を公式ブログで明確に語っています：

> "As token count grows, accuracy and recall degrade" [^2]

**Context Rot**——コンテキストが長いほど、モデルの精度は下がる。親の10万トークンの会話をコピーしたら、サブエージェントの精度が落ちる上にコストも跳ね上がる。だから切り捨てる。

さらに、Anthropicの内部ベンチマークでは：

> Multi-agent system (Claude Opus 4 lead + Claude Sonnet 4 subagents) outperformed single-agent by **90.2%** [^3]

「コンテキストを分離した方が性能が上がる」——これは直感に反するかもしれませんが、データが裏付けている設計判断です。

> **アーキテクト的に言い換えると**: マイクロサービスの境界設計と同じ。サービス間でDBを共有すると一見効率的だが、結合度が上がって保守性が下がる。コンテキスト分離は「疎結合による品質向上」そのもの。

[^2]: https://platform.claude.com/docs/en/build-with-claude/context-windows
[^3]: https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents


## 設計判断④: なぜフックはローカルイベントなのか

mitmproxyのログとフックロガーのログをDuckDBでJOINすると、面白いことがわかります。

```
python query_logs.py timeline
```

<!-- TODO: 実際のtimelineの結果を貼る -->

```
Time         | Type        | Detail
─────────────+─────────────+─────────────────────────
10:00:01.000 | << REQ      | model=claude-opus-4-6 tools=XX msgs=XX
10:00:03.500 | .. TOOL>    | Write id=toolu_01XXX
10:00:03.501 | ** HOOK     | PreToolUse tool=Write       ← API通信なし！
10:00:03.550 |             | (ローカルでファイル書き込み)
10:00:03.551 | ** HOOK     | PostToolUse tool=Write      ← API通信なし！
10:00:03.600 | >> REQ      | model=claude-opus-4-6 tools=XX msgs=XX
```

**フックはAPI通信と完全に独立したローカルイベント**。上りの通信でも下りの通信のタイミングでもない。ツールのローカル実行の前後で発火する。

さらに重要なのは、**チェックポイントもツール経由のファイル操作しか追跡しない**こと。Bashコマンドで変更したファイルはチェックポイントの対象外。

> **アーキテクト的に言い換えると**: 「ツール」というインターフェースを通した操作だけが「管理された操作」であり、それ以外は「制御外」と割り切っている。これはORMを通したDB操作だけをトランザクション管理の対象にするのと同じ考え方。

<!-- TODO: フックブロック時（exit 2）のClaude側挙動を確認 -->
<!-- TODO: stderrフィードバックがAPI通信にどう反映されるか確認 -->


## 設計判断⑤: なぜチェックポイントはgitではないのか

Claude Codeのチェックポイント（`/rewind`）は、ファイルスナップショット + セッションJSONLで実装されています。gitではない。

<!-- TODO: /rewind実行前後のセッションJSONLの差分を見せる -->
<!-- TODO: チェックポイント前後でPrompt Cachingが壊れるか確認 -->

そしてAgent Teamsでリーダーが巻き戻しても、**チームメイトはロールバックされない**。

> **アーキテクト的に言い換えると**: これは分散システムの「結果整合性（eventual consistency）」そのもの。各エージェントが独立プロセスだからこそ、リーダーの巻き戻しはチームメイトに伝播しない。CAP定理でいえば、Availability（各エージェントの独立動作）を優先し、Consistency（全エージェントの状態一致）を犠牲にしている。


## 設計判断⑥: なぜMEMORYはMarkdownなのか

Claude Codeには、セッションを跨いで知識を蓄積するAuto Memory機能があります。

- 保存先: `~/.claude/projects/<project>/memory/MEMORY.md`
- Claudeが自分で「覚えておくべきこと」を判断して書く
- セッション開始時に先頭200行 or 25KB だけロード
- マシンローカル。バージョン管理対象外

<!-- TODO: MEMORY.mdの内容をプロキシログのタイムラインと突き合わせて、書き込みタイミングを特定 -->
<!-- TODO: MEMORY.mdがリクエストのどこに注入されているか確認 -->
<!-- TODO: CLAUDE.mdとMEMORY.mdの注入位置の違いを確認 -->

なぜSQLiteでもベクトルDBでもなくMarkdownなのか？

1. **人間が読めて編集できる**。SQLiteにしたら透明性が失われる
2. **200行制限はContext Rotへの対策**。全部入れたら逆に精度が下がる
3. **マシンローカルはセキュリティ判断**。デバッグ知見やAPIキーの痕跡が共有されるリスクを回避

> **アーキテクト的に言い換えると**: 「人間にとっての可読性」を「機械にとっての効率性」より優先している。これはログをJSONにするかテキストにするかの議論と同じ。最終的に人間がデバッグするなら、人間が読める形式が正義。


## 設計判断⑦: Thinking中になにが起きているか

<!-- TODO: Extended Thinkingの実測データ -->
<!-- TODO: Thinking中の上り通信がゼロであることの確認 -->
<!-- TODO: thinkingブロックのトークン数と所要時間 -->
<!-- TODO: thinking_budgetの設定値と実際の使用量の関係 -->

重要な発見：**Thinking中、クライアント→サーバーの上り通信は一切ない**。

1. クライアントがリクエストを送信（`"thinking": {"type": "enabled"}`）
2. サーバーがSSEストリームで`thinking_delta`を返す
3. Thinkingが終わると`text_delta`や`tool_use`が返る

そしてAnthropicは公式に、Extended Thinking（応答前の推論）と"Think" Tool（応答中の一時停止）を**明確に別物**として設計しています[^4]。

> **アーキテクト的に言い換えると**: SSEの単方向ストリームでThinkingが成立するということは、**推論は完全にサーバーサイドの処理**であり、クライアントは観察者に過ぎない。これはサーバーサイドレンダリング（SSR）と同じ構造——クライアントは結果を受け取るだけ。

[^4]: https://www.anthropic.com/engineering/claude-think-tool


## 結論: 我々の設計知識はAIの時代にも通用する

Claude Codeの通信を丸裸にして見えてきたのは、**AIエージェントの設計判断が、従来のソフトウェアアーキテクチャの延長線上にある**という事実でした。

| Claude Codeの設計判断 | 従来のアーキテクチャでの対応物 |
|---|---|
| REST + SSE（gRPC不採用） | CDNとの互換性を優先したプロトコル選択 |
| Prompt Caching（prefix match） | CDNのキャッシュキー設計 |
| サブエージェントのコンテキスト分離 | マイクロサービスの境界設計 |
| フック = ローカルイベント | ORMのトランザクション境界 |
| チェックポイント ≠ git | 結果整合性（Eventual Consistency） |
| MEMORY = Markdown | 人間可読性優先のログ設計 |
| Thinking = サーバーサイド完結 | SSR（Server-Side Rendering） |

「AIエージェント」は新しいパラダイムに見えますが、その裏側にある設計判断は、我々が長年取り組んできた分散システム、キャッシュ設計、プロトコル選択と**本質的に同じ問題**です。

私たちアーキテクトの知識は、AIの時代にも**そのまま武器になる**。

前回のADK×A2Aハンズオンで「すべてはツール」という抽象化を学びました。今回わかったのは、**「すべてはツール」の裏側にある「すべてはHTTPリクエスト」**、そしてその設計判断は**我々がすでに知っている原則の上に成り立っている**ということです。

## 実験コード

今回の実験に使ったコードはGitHubに公開しています。

https://github.com/katonium/articles/tree/main/samplecodes/claude-code-proxy-analysis

自分で試してみたい方は：

```bash
# 依存: mitmproxy, DuckDB (pip install mitmproxy duckdb)

# 1. プロキシ起動（ターミナル1）
./start_proxy.sh

# 2. フック設定
./setup_hooks.sh ~/your-project

# 3. Claude Code起動（ターミナル2）
HTTPS_PROXY=http://localhost:8080 \
NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem \
claude

# 4. 分析
python queries/query_logs.py summary
python queries/query_logs.py timeline
python queries/query_logs.py cache-analysis
```

## 参考文献

- [Building Effective Agents](https://www.anthropic.com/research/building-effective-agents) - Anthropic
- [Claude Code: Best Practices for Agentic Coding](https://www.anthropic.com/engineering/claude-code-best-practices) - Anthropic Engineering
- [Effective Context Engineering for AI Agents](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents) - Anthropic Engineering
- [Effective Harnesses for Long-Running Agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents) - Anthropic Engineering
- [Prompt Caching](https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching) - Anthropic Docs
- [Context Windows](https://platform.claude.com/docs/en/build-with-claude/context-windows) - Anthropic Docs
- [The "Think" Tool](https://www.anthropic.com/engineering/claude-think-tool) - Anthropic Engineering
