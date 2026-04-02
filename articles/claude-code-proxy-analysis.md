---
title: "Claude CodeがTodoアプリを作る様子を見守ってみた 〜HTTP_PROXYで通信を丸裸にする〜"
emoji: "🔬"
type: "tech"
topics: ["ClaudeCode", "AIエージェント", "mitmproxy", "tech"]
published: false
---

こんにちは、 [@Katonium0615](https://x.com/Katonium0615) です。

Claude Codeって便利ですよね。「Todoアプリ作って」と言えば、ファイルを読み、コードを書き、テストを走らせ、コミットまでしてくれる。

でも、ふと思ったんです。

**「こいつ、裏でなにやってんだ？」**

ツールを呼ぶとき、考えるとき、サブエージェントを生むとき——それぞれの瞬間にどんな通信が飛んで、なにが送られて、なにが返ってきているのか。Claudeの思考もコードも、結局はHTTPリクエストとレスポンスに乗っている。

というわけで、**mitmproxyでClaude Codeの全通信を傍受しながら、Todoアプリを作らせてみました**。初心者チュートリアル記事に見せかけた、Claude Codeの解剖記録です。

:::message
本記事ではClaude Codeが公式にサポートしているHTTP_PROXY機能を使用しています。不正なリバースエンジニアリングではなく、企業ネットワークでの利用を想定して提供されている正規の機能です。
:::

## 実験環境

今回の構成はこうです。

```
┌──────────────┐     HTTPS_PROXY      ┌──────────────┐     HTTPS      ┌────────────────┐
│  Claude Code │ ──────────────────→  │  mitmproxy   │ ────────────→ │ api.anthropic  │
│  (CLI)       │                      │  (localhost   │               │ .com           │
│              │ ←──────────────────  │   :8080)     │ ←──────────── │                │
└──────────────┘     SSE Stream       └──────┬───────┘               └────────────────┘
       │                                     │
       │  hooks (stdin/stdout)               │ SQLite
       ▼                                     ▼
┌──────────────┐                      ┌──────────────┐
│  Hook Logger │                      │  SQLite DB   │
│  (bash)      │ ─────────────────→  │  + Query     │
└──────────────┘     SQLite           │    Tool      │
                                      └──────────────┘
```

- **mitmproxy**: HTTPS通信の復号・記録。addonスクリプトでSQLiteに全データを保存
- **Hook Logger**: Claude Codeのフック機能で、ツール実行前後のイベントをキャプチャ
- **Query Tool**: SQLiteに溜まったログを横断的にクエリするPythonスクリプト

### セットアップ

```bash
# 1. mitmproxyのインストール
pip install mitmproxy

# 2. プロキシ起動（ターミナル1）
./start_proxy.sh

# 3. フック設定
./setup_hooks.sh ~/my-todo-app

# 4. Claude Code起動（ターミナル2）
HTTPS_PROXY=http://localhost:8080 \
NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem \
claude
```

ここまでやったら準備完了。では早速「Todoアプリを作って」とお願いしてみましょう。


## Chapter 1: 最初の一言でなにが起きるか

「Todoアプリを作って」——この一言を送信した瞬間、mitmproxyにはこんなリクエストが流れてきました。

```
⬆️  REQUEST → api.anthropic.com/v1/messages
    model=claude-opus-4-6  tools=XX  msgs=X  sys_size=XXXKB  thinking=true
```

### システムプロンプトの正体

まず驚くのが、**リクエストボディのサイズ**です。たった一言の「Todoアプリを作って」なのに、リクエスト全体は XXX KB にもなります。

なぜか？ それは**システムプロンプトとツール定義が毎回送られている**から。

```json
{
  "model": "claude-opus-4-6",
  "system": [
    {
      "type": "text",
      "text": "You are Claude Code, Anthropic's official CLI for Claude..."
      // ← ここに数万文字のシステムプロンプト
    }
  ],
  "tools": [
    // ← XX個のツール定義（JSON Schema形式）
  ],
  "messages": [...],
  "stream": true,
  "thinking": {
    "type": "enabled",
    "budget_tokens": XXXXX
  }
}
```

<!-- TODO: 実際のシステムプロンプトの冒頭を貼る -->
<!-- TODO: 実際のツール数を記載 -->
<!-- TODO: fake_toolsが混入しているかどうかを確認 -->

### ClineのXML vs Claude CodeのJSON Schema

ここで前回の記事との比較。ClineはツールをXML形式で定義していましたが、Claude Codeは**Anthropic Messages APIのネイティブ形式**——つまりJSON Schemaのtool定義を使っています。

```json
// Claude Code のツール定義（抜粋）
{
  "name": "Read",
  "description": "Reads a file from the local filesystem...",
  "input_schema": {
    "type": "object",
    "properties": {
      "file_path": {
        "type": "string",
        "description": "The absolute path to the file to read"
      }
    },
    "required": ["file_path"]
  }
}
```

ClineのようにXMLをプロンプトに埋め込む方法と比べて、API側でのバリデーションや型チェックが効くのがメリットですね。

### CLAUDE.mdはどこにいる？

プロジェクトに`.claude/CLAUDE.md`を置いている場合、その内容がリクエストに含まれます。

<!-- TODO: 実際にどこに注入されているかを確認。system配列内？messages内？ -->
<!-- TODO: 毎回含まれるか、キャッシュされるかを確認 -->

### Prompt Caching の痕跡

2回目以降のリクエストでは、レスポンスのusage情報にキャッシュの痕跡が：

```
📊 usage: input=XXXX cache_create=0 cache_read=XXXXX
```

<!-- TODO: 実際のキャッシュヒット率を記載 -->

システムプロンプトとツール定義は変わらないので、2回目以降は`cache_read_input_tokens`が大きくなるはずです。**毎回数万トークンのシステムプロンプトを送っているように見えて、実際には課金されていない**。


## Chapter 2: ツールが呼ばれるとき

Claudeが「ファイルを作ろう」と判断した瞬間、SSEストリームにこんなイベントが流れます。

```
🔨 tool_use → Write  id=toolu_01XXXXX
```

### tool_use → tool_result のラウンドトリップ

通信の流れはこうです：

1. **レスポンス（SSE）**: Claudeが`tool_use`ブロックを返す
2. **ローカル実行**: Claude Codeがツールを実行（API通信なし）
3. **リクエスト**: 実行結果を`tool_result`としてAPIに送信
4. **レスポンス（SSE）**: Claudeが結果を踏まえた次のアクションを返す

```
[T+0.0s] ← SSE: content_block_start (tool_use: Write)
[T+0.1s] ← SSE: content_block_delta (input_json_delta)
[T+0.5s] ← SSE: content_block_stop
[T+0.5s] 🪝 PreToolUse (tool=Write)     ← フック発火！
[T+0.6s] -- ローカルでファイル書き込み --
[T+0.6s] 🪝 PostToolUse (tool=Write)     ← フック発火！
[T+0.7s] ⬆️  REQUEST (tool_resultを含む)
```

ここで重要なのは、**フックはAPI通信とは完全に独立したローカルイベント**だということ。上りの通信ではないし、下りの通信のタイミングでもない。ツールのローカル実行の前後で発火する。

<!-- TODO: 実際のタイムラインをquery_logs.py timelineで取得して貼る -->


## Chapter 3: 考えている時（Extended Thinking）

Claude Codeに考えさせると、SSEストリームに`thinking`ブロックが現れます。

```
💭 thinking: Let me analyze this request...
```

### Thinking中の通信

重要な発見：**Thinking中、クライアント→サーバーの上り通信は一切ない**。

1. クライアントがリクエストを送信（`"thinking": {"type": "enabled"}`）
2. サーバーがSSEストリームで`thinking_delta`を少しずつ返す
3. Thinkingが終わると`text_delta`や`tool_use`が返る

つまり、Thinkingはサーバー側で完結する処理。Claude Code側は「SSEを待っているだけ」です。

<!-- TODO: Thinkingのトークン数とかかった時間を記載 -->


## Chapter 4: サブエージェントが生まれるとき

Claudeが「これは調査が必要だ」と判断すると、`Agent`ツールを呼び出してサブエージェントを生成します。

```
🔨 tool_use → Agent  id=toolu_01XXXXX
🧠 New session detected: subagent (sys_hash=abc123, tools_hash=def456)
```

### 独立セッションの証拠

サブエージェントは**完全に別のAPIリクエスト系列**です。

<!-- TODO: 以下を実データで確認 -->
- システムプロンプトのハッシュが異なる → 別のシステムプロンプト
- ツール定義のハッシュが異なる → 使えるツールが制限されている
- 親の会話履歴は含まれない → messagesが初回リクエスト相当

```
python query_logs.py sessions
```

```
Time      | Type     | Model          | SysHash | ToolsHash
──────────+──────────+────────────────+─────────+──────────
10:00:01  | main     | claude-opus-4-6 | abc123  | def456
10:00:15  | subagent | claude-opus-4-6 | xyz789  | uvw321
```

<!-- TODO: Exploreエージェントのツール定義を抜き出して、メインとの差分を見せる -->


## Chapter 5: Agent Teamsで開発してみる

:::message alert
Agent TeamsはExperimental機能です。`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`が必要。
:::

<!-- TODO: Agent Teamsの実験結果を記載 -->
<!-- TODO: 各エージェント間のメッセージ交換がどう表現されるか -->


## Chapter 6: フックで介入してみる

最後に、フックを使って**Claude Codeの動作に介入**してみましょう。

### ツール実行をブロックする

`exit 2`で返すフックを仕込むと、Claude Codeはそのツールの実行をスキップします。

```bash
# Bashツールだけブロックする
BLOCK_TOOLS="Bash" ./hook_blocker.sh
```

<!-- TODO: ブロック時のClaudeの反応を記載 -->
<!-- TODO: stderrのフィードバックがClaudeに届いているかAPI通信で確認 -->


## まとめ: すべてはHTTPリクエストの上に

Claude Codeを丸裸にしてわかったこと：

<!-- TODO: 実験結果に基づいてまとめを書く -->

1. **システムプロンプトはAPI経由で毎回送信される**。ただしPrompt Cachingにより2回目以降のコストは軽減
2. **ツール定義はJSON Schema形式**。ClineのXMLアプローチとは異なり、APIネイティブ
3. **CLAUDE.mdは○○に注入される**
4. **フックはローカルイベント**。API通信とは独立しており、通信のタイムラインとは別の軸で動く
5. **サブエージェントは独立セッション**。親のコンテキストは一切引き継がない
6. **Thinking中の上り通信はゼロ**。サーバー側で完結する
7. **Agent Teamsは○○**

前回のADK×A2Aハンズオンで「すべてはツール」という抽象化を学びました。Claude Codeもまた、サブエージェントもスキルもAgent Teamsも、LLMから見れば全て「ツール」です。

でもその「ツール」の裏側には、独立したセッション管理、Prompt Caching、ローカルフック、SSEストリーミングという、よく練られたアーキテクチャがありました。

**「すべてはツール」の裏側にある「すべてはHTTPリクエスト」**——これが今回の発見です。

## 実験コード

今回の実験に使ったコードはすべてGitHubに公開しています。

<!-- TODO: リポジトリURL -->

自分で試してみたい方は、以下の手順で：

```bash
git clone <repo_url>
cd samplecodes/claude-code-proxy-analysis
./start_proxy.sh  # ターミナル1
# ターミナル2で claude を起動
python queries/query_logs.py summary  # ログ確認
```
