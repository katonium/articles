# A2A プロトコル実装の深堀り

ADK のソースコードから、A2A プロトコルの実装詳細を読み解く。

venv のパス: `.venv/lib/python3.12/site-packages/google/adk/`

---

## 関連ファイル一覧

| ファイル | 役割 |
|---------|------|
| `agents/remote_a2a_agent.py` | RemoteA2aAgent 本体。Agent Card 解決、A2A通信、レスポンス処理 |
| `a2a/converters/part_converter.py` | A2A Part ↔ GenAI Part の相互変換 |
| `a2a/converters/event_converter.py` | ADK Event ↔ A2A Message/Task の変換 |
| `a2a/executor/a2a_agent_executor.py` | A2Aサーバー側の実行エンジン。Artifact発行もここ |
| `a2a/converters/utils.py` | context_id 生成、metadata キー管理 |
| `flows/llm_flows/agent_transfer.py` | sub_agents の転送メカニズム（前回のノート参照） |
| `artifacts/artifact_util.py` | Artifact URI のパース・フォーマット |

---

## 1. ディスカバリと登録

### Agent Card の解決フロー

`RemoteA2aAgent` を初期化すると、`agent_card` 引数は3パターン受け付ける（`remote_a2a_agent.py` L115-189）：

1. **AgentCard オブジェクト** → そのまま保持
2. **URL 文字列** → `_agent_card_source` に保存、後で HTTP で取得
3. **ファイルパス文字列** → `_agent_card_source` に保存、後でファイルから読み込み

**初回の `_run_async_impl` 呼び出し時**に `_ensure_resolved()` が呼ばれ、実際の解決が行われる（遅延ロード）：

```python
# remote_a2a_agent.py L289-322
async def _ensure_resolved(self) -> None:
    if self._is_resolved and self._a2a_client:
        return

    # 1. Agent Card がまだなら解決
    self._agent_card = await self._resolve_agent_card()

    # 2. Agent Card のバリデーション（RPC URL があるか確認）
    await self._validate_agent_card(self._agent_card)

    # 3. description が空なら Agent Card から自動補完
    if not self.description and self._agent_card.description:
        self.description = self._agent_card.description

    # 4. A2A Client を初期化
    self._a2a_client = self._a2a_client_factory.create(self._agent_card)
    self._is_resolved = True
```

URL からの解決は `A2ACardResolver`（a2a-sdk のクラス）を使って HTTP GET する：

```python
# remote_a2a_agent.py L219-240
async def _resolve_agent_card_from_url(self, url: str) -> AgentCard:
    parsed_url = urlparse(url)
    base_url = f"{parsed_url.scheme}://{parsed_url.netloc}"
    relative_card_path = parsed_url.path

    resolver = A2ACardResolver(
        httpx_client=httpx_client,
        base_url=base_url,
    )
    return await resolver.get_agent_card(relative_card_path=relative_card_path)
```

### LLM への登録（sub_agents として使う場合）

`RemoteA2aAgent` を `sub_agents` に登録した場合、前回のノートで解説した `agent_transfer.py` の仕組みがそのまま動く。つまり：

1. `_get_transfer_targets()` が RemoteA2aAgent を転送先として含める
2. LLM に `transfer_to_agent` ツールと転送指示が注入される
3. LLM が `transfer_to_agent("command_center_agent")` を呼ぶ
4. `_run_async_impl` で `RemoteA2aAgent._run_async_impl` が呼ばれる

**重要**: RemoteA2aAgent は `BaseAgent` を継承しているので、ローカルの Agent と同じインターフェースで動く。親エージェントや `agent_transfer.py` からは区別がつかない。これが ADK の「ローカルもリモートも同じ」という抽象化の正体。

### RemoteA2aAgent の実行フロー（`_run_async_impl`）

```python
# remote_a2a_agent.py L517-592
async def _run_async_impl(self, ctx: InvocationContext):
    # 1. Agent Card 解決（初回のみ）
    await self._ensure_resolved()

    # 2. セッションの events からメッセージパーツを構築
    message_parts, context_id = self._construct_message_parts_from_session(ctx)

    # 3. A2A メッセージを作成
    a2a_request = A2AMessage(
        message_id=str(uuid.uuid4()),
        parts=message_parts,
        role="user",
        context_id=context_id,
    )

    # 4. JSON RPC で送信し、レスポンスを ADK Event に変換
    async for a2a_response in self._a2a_client.send_message(
        request=a2a_request,
        context=ClientCallContext(state=ctx.session.state),
    ):
        event = await self._handle_a2a_response(a2a_response, ctx)
        yield event
```

### Stateful vs Stateless エージェント

リモートエージェントには2種類ある（`remote_a2a_agent.py` L358-408）：

- **Stateful**: レスポンスに `context_id` が含まれる。次回以降は `context_id` を送れば、リモート側がセッション状態を保持してくれるので、**新しいメッセージだけ**送れば良い
- **Stateless**: `context_id` がない。毎回、セッション履歴から過去の会話を再構築して全部送る必要がある

```python
# remote_a2a_agent.py L370-408
def _construct_message_parts_from_session(self, ctx):
    for event in reversed(ctx.session.events):
        if self._is_remote_response(event):
            # 前回のリモートレスポンスを見つけた
            context_id = metadata.get(A2A_METADATA_PREFIX + "context_id")
            # stateful ならここで止める（以降はリモートが知ってる）
            if not self._full_history_when_stateless or context_id:
                break
        events_to_process.append(event)
```

---

## 2. JSON RPC における Part の扱い

### Part の種類

A2A プロトコルでは3種類の Part がある（`part_converter.py`）：

#### TextPart（テキスト）

```python
# A2A → GenAI
if isinstance(part, a2a_types.TextPart):
    return genai_types.Part(text=part.text)

# GenAI → A2A
if part.text:
    a2a_part = a2a_types.TextPart(text=part.text)
    if part.thought is not None:
        # LLM の思考（Thought）は metadata に格納
        a2a_part.metadata = {_get_adk_metadata_key('thought'): part.thought}
    return a2a_types.Part(root=a2a_part)
```

**ポイント**: GenAI の `thought`（LLMの内部思考）は A2A の TextPart の metadata に `adk_thought: true` として保持される。

#### FilePart（ファイル添付）

2つのバリアント：

```python
# FileWithUri: URIで参照（Cloud Storage 等）
a2a_types.FilePart(
    file=a2a_types.FileWithUri(
        uri="gs://bucket/file.png",
        mime_type="image/png"
    )
)

# FileWithBytes: Base64エンコードされたバイナリ
a2a_types.FilePart(
    file=a2a_types.FileWithBytes(
        bytes="base64encodeddata...",  # str (Base64)
        mime_type="image/png"
    )
)
```

GenAI の `file_data`（URI参照）は `FileWithUri` に、`inline_data`（バイナリ埋め込み）は `FileWithBytes` に変換される。

#### DataPart（構造化データ）

関数呼び出し、関数レスポンス、コード実行結果など、JSON データを運ぶ Part。`metadata` の `type` フィールドで種別を判定する：

```python
# part_converter.py L37-42
A2A_DATA_PART_METADATA_TYPE_FUNCTION_CALL = 'function_call'
A2A_DATA_PART_METADATA_TYPE_FUNCTION_RESPONSE = 'function_response'
A2A_DATA_PART_METADATA_TYPE_CODE_EXECUTION_RESULT = 'code_execution_result'
A2A_DATA_PART_METADATA_TYPE_EXECUTABLE_CODE = 'executable_code'
```

例えば、関数呼び出しの変換：

```python
# GenAI → A2A
if part.function_call:
    return a2a_types.Part(
        root=a2a_types.DataPart(
            data=part.function_call.model_dump(by_alias=True, exclude_none=True),
            metadata={
                _get_adk_metadata_key('type'): 'function_call'
            },
        )
    )

# A2A → GenAI
if type_value == 'function_call':
    return genai_types.Part(
        function_call=genai_types.FunctionCall.model_validate(part.data)
    )
```

**ポイント**: DataPart は A2A プロトコル自体にはまだ function_call 等の正式な定義がない（TODOコメントあり）。現状は metadata の type キーで ADK 独自に判別している。

```python
# part_converter.py L93-94 のコメント
# TODO once A2A defined how to service such information, migrate below
# logic accordingly
```

### メッセージ全体の構造

```python
# A2A Message の形
A2AMessage(
    message_id="uuid-string",
    parts=[TextPart(...), FilePart(...), DataPart(...)],  # Part のリスト
    role="user",          # "user" or "agent"
    context_id="ADK/app/user/session",  # stateful の場合
)
```

通信は `a2a_client.send_message()` で JSON RPC として送信される。レスポンスは2パターン：

1. **A2AMessage**: 直接メッセージ（シンプルな応答）
2. **A2AClientEvent (tuple)**: `(Task, Update)` — タスクベースの応答。ストリーミングやアーティファクト付き

---

## 3. Artifacts の扱い

### Artifact の構造

A2A における Artifact はタスクの成果物。`a2a_agent_executor.py` で発行される：

```python
# a2a_agent_executor.py L244-254
await event_queue.enqueue_event(
    TaskArtifactUpdateEvent(
        task_id=context.task_id,
        last_chunk=True,
        context_id=context.context_id,
        artifact=Artifact(
            artifact_id=str(uuid.uuid4()),
            parts=task_result_aggregator.task_status_message.parts,  # Part のリスト
        ),
    )
)
```

Artifact は Part のリストを持つ。つまりテキスト、ファイル、構造化データの組み合わせが成果物になれる。

### RemoteA2aAgent でのレスポンス処理

レスポンスの種類に応じた処理（`remote_a2a_agent.py` L410-515）：

```python
async def _handle_a2a_response(self, a2a_response, ctx):
    if isinstance(a2a_response, tuple):  # A2AClientEvent
        task, update = a2a_response

        if update is None:
            # 完全なタスク状態（初回 or 非ストリーミング）
            event = convert_a2a_task_to_event(task, ...)

        elif isinstance(update, A2ATaskStatusUpdateEvent):
            # ストリーミング中の状態更新（working, submitted）
            # → thought として扱う
            event = convert_a2a_message_to_event(update.status.message, ...)
            for part in event.content.parts:
                part.thought = True  # 途中経過は "思考" として表示

        elif isinstance(update, A2ATaskArtifactUpdateEvent):
            # アーティファクト更新
            if not update.append or update.last_chunk:
                # 完全なアーティファクト（部分更新は無視）
                event = convert_a2a_task_to_event(task, ...)

        # task_id と context_id を metadata に保存（次回リクエスト用）
        event.custom_metadata["a2a.task_id"] = task.id
        event.custom_metadata["a2a.context_id"] = task.context_id

    elif isinstance(a2a_response, A2AMessage):
        # 単純なメッセージレスポンス
        event = convert_a2a_message_to_event(a2a_response, ...)
```

### Task → Event 変換時の Artifact 優先順位

`event_converter.py` でタスクからメッセージを抽出する際の優先順位：

```python
# event_converter.py L201-265（概要）
def convert_a2a_task_to_event(a2a_task, ...):
    message = None

    # 1. artifacts（最新のもの）を優先
    if a2a_task.artifacts:
        message = Message(
            role=Role.agent,
            parts=a2a_task.artifacts[-1].parts  # 最新の artifact
        )

    # 2. task status のメッセージ
    elif a2a_task.status and a2a_task.status.message:
        message = a2a_task.status.message

    # 3. history の最後のメッセージ
    elif a2a_task.history:
        message = a2a_task.history[-1]
```

**ポイント**: Artifact が存在すれば、status message や history より優先される。Artifact がタスクの「最終的な成果物」という位置づけ。

### Artifact Update のストリーミング

Artifact はチャンクで送れる：

- `append=False, last_chunk=True` → 完全な1回限りの artifact
- `append=True, last_chunk=False` → 部分更新（ADK は現状無視する）
- `append=True, last_chunk=True` → 最後のチャンク（これで完了）

現状 ADK は部分更新をスキップし、`last_chunk=True` の完了時のみ処理する。

### ADK 内部の Artifact サービス（補足）

ADK 内部では Artifact は URI で管理される：

```
# セッションスコープ
artifact://apps/{app_name}/users/{user_id}/sessions/{session_id}/artifacts/{filename}/versions/{version}

# ユーザースコープ
artifact://apps/{app_name}/users/{user_id}/artifacts/{filename}/versions/{version}
```

バージョン管理されており、同じファイル名で保存するとバージョンがインクリメントされる。ただし、これは ADK 内部のストレージの話であり、A2A プロトコル自体の Artifact とは別レイヤー。A2A の Artifact はタスクレスポンスに含まれる Part のリストとして流れ、受け取り側の ADK がそれを Event に変換する。

---

## まとめ：A2A 通信の全体フロー

```
1. 親 Agent が transfer_to_agent("command_center_agent") を呼ぶ

2. RemoteA2aAgent._run_async_impl() が呼ばれる
   ├─ _ensure_resolved(): Agent Card を HTTP GET → バリデーション → A2A Client 初期化
   ├─ _construct_message_parts_from_session(): セッション events から Part リストを構築
   └─ A2AMessage 作成: { message_id, parts, role: "user", context_id }

3. a2a_client.send_message() で JSON RPC 送信
   ├─ リクエスト: POST to agent_card.url (RPC endpoint)
   └─ レスポンス: Task (with status, artifacts, history) or Message

4. _handle_a2a_response() でレスポンスを ADK Event に変換
   ├─ Task の場合: artifacts > status.message > history の優先順で Part を抽出
   ├─ working/submitted の status → thought (途中経過) として扱う
   └─ task_id, context_id を metadata に保存

5. Event が yield され、親 Agent のイベントストリームに流れる
```

---

## 3.5. Event / Message / Task / Artifact の関係

### レイヤーの違い

```
┌─────────────────────────────────────────────────────┐
│  A2A プロトコル層（エージェント間通信）                 │
│    - A2A Message : 会話のやりとり（1通のメッセージ）    │
│    - A2A Task    : 状態を持つ作業単位                  │
│      └─ Artifact : タスクの成果物                     │
├─────────────────────────────────────────────────────┤
│  ADK 内部層（フレームワーク内部）                       │
│    - Event : A2A の全レスポンスをこれに統一            │
└─────────────────────────────────────────────────────┘
```

- **A2A Message** = メール1通。シンプルな質問→回答のやりとり
- **A2A Task** = Jira チケット。状態遷移（submitted → working → completed/failed）を持つ
- **Artifact** = チケットの添付ファイル（納品物）。Task に紐づく成果物
- **ADK Event** = 社内の統一フォーマット。上記すべてを内部的にこれに変換する

### A2A Message と Task の使い分けは誰が決めるか？

**サーバー側（リモートエージェント）が決める**。クライアント側（呼び出し元）は制御できない。

`remote_a2a_agent.py` の `_handle_a2a_response()` は、レスポンスの型で分岐する：

```python
# remote_a2a_agent.py L424-493
async def _handle_a2a_response(self, a2a_response, ctx):
    if isinstance(a2a_response, tuple):
        # → Task ベースのレスポンス（A2AClientEvent = (Task, Update)）
        task, update = a2a_response
        ...
    elif isinstance(a2a_response, A2AMessage):
        # → シンプルなメッセージレスポンス
        ...
```

つまりクライアントは **常に `A2AMessage` としてリクエストを送り**、サーバーが Message で返すか Task で返すかを決める。ADK の `a2a_agent_executor.py` は**常に Task を作って返す**実装になっているので、ADK 同士の通信では実質 Task が使われる。

### Task のライフサイクル（TaskState）

```python
# a2a/types.py L989-1002
class TaskState(str, Enum):
    submitted       = 'submitted'        # タスク受付
    working         = 'working'          # 処理中
    input_required  = 'input-required'   # ユーザー入力待ち
    completed       = 'completed'        # 正常完了
    canceled        = 'canceled'         # キャンセル
    failed          = 'failed'           # 失敗
    rejected        = 'rejected'         # 拒否
    auth_required   = 'auth-required'    # 認証待ち
    unknown         = 'unknown'          # 不明
```

ADK サーバー（`a2a_agent_executor.py`）のフローは：

```
submitted → working → completed (+ Artifact)   ← 正常系
submitted → working → failed (+ error message)  ← 異常系
```

### Task が失敗した場合の挙動

**Artifact は来ない。代わりに `TaskState.failed` の status が来て、エラーメッセージが `status.message` に入る**。

```python
# a2a_agent_executor.py L156-175
except Exception as e:
    await event_queue.enqueue_event(
        TaskStatusUpdateEvent(
            task_id=context.task_id,
            status=TaskStatus(
                state=TaskState.failed,              # ← failed
                message=Message(
                    role=Role.agent,
                    parts=[TextPart(text=str(e))],   # ← エラー内容がテキストで入る
                ),
            ),
            context_id=context.context_id,
            final=True,                              # ← これで終了
        )
    )
```

つまり：
- **成功**: `completed` + Artifact（成果物）
- **失敗**: `failed` + status.message（エラー内容）。Artifact なし

クライアント側（`remote_a2a_agent.py`）は、failed の status update を受け取ると、それを Event に変換して親エージェントに返す。親の LLM がエラーメッセージを見て「別の方法を試そう」等と判断する。

### TaskResultAggregator の状態管理

サーバー側では `TaskResultAggregator` が状態の優先度を管理する（`task_result_aggregator.py`）：

```
failed > auth_required > input_required > working
```

一度 `failed` になると、後の status update で上書きされない。例えば途中で `auth_required` が来ても、既に `failed` ならそのまま `failed` を維持する。

### Subtask の概念はあるか？

**A2A プロトコルとしてはない**。Task はフラットで、親子関係の仕組みはない。

ただし、実質的なサブタスク相当は `context_id` で実現される：

```
context_id = "ADK/app/user/session"  ← 同じ context_id のタスクは関連がある
```

同じ `context_id` で複数の Task が発行されると、サーバー側は同じセッション（会話の文脈）として扱う。しかしこれは「同じ会話の続き」であって、階層的な「親タスク→子タスク」の関係ではない。

エージェント間の階層構造は **ADK 側で管理**される：
- 親エージェントが `transfer_to_agent` で子エージェントに転送
- 子エージェント（RemoteA2aAgent）が A2A 通信でリモートに Task を発行
- リモートが内部的にさらに sub_agents を持っていても、それは呼び出し元からは見えない

```
[親Agent] ──transfer──→ [RemoteA2aAgent] ──A2A Task──→ [リモートAgent]
                                                          ├─ sub_agent_1
                                                          └─ sub_agent_2
                                                             （これらは見えない）
```

---

## 4. Q&A：補足解説

### Q1. 「初回の `_run_async_impl` 呼び出し時」とは具体的にいつか？遅延ロードは誰がうれしい？

**いつ呼ばれるか**：親エージェントの LLM が `transfer_to_agent("command_center_agent")` を呼んだとき。具体的な呼び出しチェーンは：

```
ユーザーが「宝探し開始」→ 親 Agent の LLM が推論
→ LLM が transfer_to_agent("command_center_agent") を tool call
→ agent_transfer.py が対象の sub_agent を特定
→ llm_agent.py の _run_async_impl が RemoteA2aAgent._run_async_impl を呼ぶ
→ ここで初めて _ensure_resolved() → Agent Card を HTTP GET
```

つまり、`RemoteA2aAgent(agent_card="https://...")` でインスタンスを作った時点では何も起きない。**LLM が実際にそのエージェントを使うと決めたとき**に初めて解決が走る。

**遅延ロードの利点**：

1. **起動時間の短縮**: sub_agents に10個の RemoteA2aAgent を登録しても、アプリ起動時に10回の HTTP リクエストは走らない。使われるエージェントだけ、使われるタイミングで解決する
2. **障害の局所化**: 3つのリモートエージェントのうち1つがダウンしていても、残り2つは正常に使える。起動時に全部解決するとアプリ全体が起動失敗する
3. **description の自動補完**: Agent Card の description が取れたら `self.description` にセットする仕組みがある

**⚠️ 注意：遅延ロードと description の矛盾**

ここには設計上のギャップがある。`agent_transfer.py` の `_build_target_agents_info()` は LLM にプロンプトを送る**前**に `agent.description` を参照するが、Agent Card からの description 自動補完は `_ensure_resolved()` = transfer が決まった**後**に走る。

つまり、`RemoteA2aAgent` 作成時に `description` を明示的に設定しないと、**初回の転送判断時に LLM は description を見られない**（name だけになる）。

実用上は親エージェントの `instruction` に転送先の説明を書くことで回避できるが、ベストプラクティスとしては `description` を明示的に設定すべき：

```python
# ✅ 推奨: description を明示的に設定
RemoteA2aAgent(
    name="command_center_agent",
    description="ゲームの進行を管理する司令部エージェント",
    agent_card=os.getenv("COMMAND_AGENT_AGENT_CARD"),
)

# ⚠️ 非推奨: description なし → 初回 transfer 時に LLM が判断材料を持たない
RemoteA2aAgent(
    name="command_center_agent",
    agent_card=os.getenv("COMMAND_AGENT_AGENT_CARD"),
)
```

Agent Card からの自動補完は「2回目以降」や「書き忘れのフォールバック」と考えるのが妥当。

**検証結果（2026-02-20）**: 実際に存在しない URI を Agent Card に設定して `adk web` を起動したところ、**起動は正常に成功**し、`transfer_to_agent` が実行されるまで 404 エラーは発生しなかった。遅延ロードの挙動を実証。

**Agent Card の情報が必要になるタイミングの整理**:

| 情報 | 用途 | 必要タイミング |
|------|------|---------------|
| `name` | LLM の転送先 enum | インスタンス生成時（コードで指定済み） |
| `description` | LLM の判断材料 | システムプロンプト構築時（なくても instruction で代替可） |
| `url` | JSON RPC 送信先 | transfer 実行時（**AgentCard 必須**） |
| `security` | 認証ヘッダー | transfer 実行時（**AgentCard 必須**） |
| `capabilities` | ストリーミング判定 | transfer 実行時（**AgentCard 必須**） |

ADK はターンごとにシステムプロンプトを再構築するため、初回 transfer で `_ensure_resolved()` が description を補完すれば、2ターン目以降は `_build_target_agents_info()` に反映される。

### Q2. TextPart / FilePart / DataPart の違い

| Part | 運ぶもの | 具体例 | JSON RPC 上の表現 |
|------|---------|--------|------------------|
| **TextPart** | 人間が読めるテキスト | チャットメッセージ、説明文 | `{"text": "こんにちは"}` |
| **FilePart** | バイナリファイル | 画像、PDF、音声 | URI参照 (`FileWithUri`) or Base64埋め込み (`FileWithBytes`) |
| **DataPart** | 構造化された JSON データ | 関数呼び出し、コード実行結果 | `{"data": {...}, "metadata": {"type": "function_call"}}` |

要するに：
- **TextPart** = 「文字列」。一番シンプル
- **FilePart** = 「ファイル添付」。メールの添付ファイルのようなもの。URIで参照するか、Base64で直接送るかの2択
- **DataPart** = 「構造化データ」。LLM のツール呼び出し結果など、人間向けのテキストではなくプログラムが解釈するデータを運ぶ

### Q3. DataPart の function_call とは

LLM が「この関数を呼んで」と返す **ツール呼び出しリクエスト** のこと。

例：LLM が `treasure_tool(key="12345")` を呼びたいとき、GenAI の `FunctionCall` オブジェクトが生成される。これを A2A プロトコルで別エージェントに送る場合、DataPart に変換される：

```json
{
  "data": {
    "name": "treasure_tool",
    "args": {"key": "12345"}
  },
  "metadata": {
    "adk_type": "function_call"
  }
}
```

同様に、関数の実行結果は `function_response` として DataPart に格納される：

```json
{
  "data": {
    "name": "treasure_tool",
    "response": {"result": "おめでとう！宝を見つけました！"}
  },
  "metadata": {
    "adk_type": "function_response"
  }
}
```

A2A プロトコル自体にはまだ function_call の正式仕様がないため、ADK は `metadata.type` で独自に判別している（TODOコメント付き）。将来的に A2A 仕様に取り込まれる想定。

### Q4. Artifact が「Part リストを保持する」とは

Artifact の型定義（`a2a/types.py` の `Artifact` クラス）：

```python
class Artifact(BaseModel):
    artifact_id: str                     # UUID
    name: str | None = None              # 任意の名前（例: "analysis_report"）
    description: str | None = None       # 説明
    parts: list[Part]                    # ← ここが本体。Part のリスト
    metadata: dict[str, Any] | None = None
```

つまり、1つの Artifact は複数の Part を持てる。例えば「分析レポート」という Artifact なら：

```python
Artifact(
    artifact_id="uuid-123",
    name="analysis_report",
    parts=[
        TextPart(text="分析結果のサマリー..."),           # テキストの説明
        FilePart(file=FileWithUri(uri="gs://...", ...)),  # グラフの画像
        DataPart(data={"scores": [95, 87, 72]}, ...),    # 構造化データ
    ]
)
```

メッセージの中身（Part リスト）と Artifact の中身（Part リスト）は同じ構造。メッセージは会話のやりとり、Artifact はタスクの最終成果物、という意味の違いがある。

### Q5. ディスカバリはフレームワークが行ってシステムプロンプトに乗せるのか？

**半分 Yes、半分 No**。

1. **Agent Card の取得（ディスカバリ）** → フレームワーク（`RemoteA2aAgent._ensure_resolved()`）が HTTP GET で行う
2. **LLM への情報注入** → `agent_transfer.py` の `_build_target_agents_info()` が行う

ただし、**Agent Card の全情報がシステムプロンプトに入るわけではない**。

`agent_transfer.py` の `_build_target_agents_info()` は、転送先エージェントの情報を以下のフォーマットでシステムプロンプトに注入する：

```python
def _build_target_agents_info(transfer_targets):
    info = ""
    for agent in transfer_targets:
        info += f"- {agent.name}: {agent.description}\n"
    return info
```

つまり、**LLM に渡されるのは `name` と `description` だけ**。Agent Card に含まれる skills, capabilities, security, supported MIME types などの情報は LLM には渡されない。

これらの追加情報は **フレームワーク内部**で使われる：
- `capabilities.streaming` → ストリーミング通信するかどうか
- `default_input_modes` / `default_output_modes` → MIME type の判定
- `security` → 認証方式の選択
- `url` → JSON RPC の送信先

### Q6. Agent Card にはどんな情報が含まれるか

`a2a/types.py` の `AgentCard` クラスの全フィールド：

```python
class AgentCard(BaseModel):
    # === 基本情報 ===
    name: str                               # エージェント名（例: "command_center_agent"）
    description: str | None = None          # 説明文（LLM のシステムプロンプトにも使われる）
    url: str                                # JSON RPC エンドポイント URL

    # === 能力宣言 ===
    skills: list[AgentSkill] = []           # スキル一覧
    capabilities: AgentCapabilities | None  # ストリーミング対応、プッシュ通知対応など
    default_input_modes: list[str]          # 受け付ける MIME type（例: ["text/plain", "image/png"]）
    default_output_modes: list[str]         # 出力する MIME type

    # === プロトコル ===
    protocol_version: str | None = None     # A2A プロトコルバージョン
    preferred_transport: str | None = None  # 推奨トランスポート（例: "sse", "websocket"）

    # === セキュリティ ===
    security: list[dict] | None = None      # 認証方式（OAuth, API Key 等）
    security_schemes: dict | None = None    # セキュリティスキーム定義

    # === メタデータ ===
    provider: AgentProvider | None = None   # 提供者情報
    version: str | None = None              # エージェントバージョン
```

`AgentSkill` の構造：

```python
class AgentSkill(BaseModel):
    id: str                                 # スキルID
    name: str                               # スキル名
    description: str                        # スキルの説明
    examples: list[str] = []                # 入力例（例: ["2+3を計算して", "πの値は？"]）
    input_modes: list[str] = []             # このスキル固有の入力 MIME type
    output_modes: list[str] = []            # このスキル固有の出力 MIME type
    tags: list[str] = []                    # タグ（カテゴリ分け用）
```

`AgentCapabilities` の構造：

```python
class AgentCapabilities(BaseModel):
    streaming: bool = False                  # SSE ストリーミング対応か
    push_notifications: bool = False         # プッシュ通知対応か
    state_transition_history: bool = False   # タスク状態遷移の履歴を保持するか
    extensions: list[Extension] = []         # プロトコル拡張
```

### まとめ：Agent Card の情報の使われ方

```
Agent Card
├── name, description ──────────→ LLM のシステムプロンプト（agent_transfer.py）
├── url ────────────────────────→ JSON RPC 送信先（A2A Client）
├── capabilities.streaming ─────→ 通信方式の選択（SSE or 通常 HTTP）
├── default_input/output_modes ─→ Part の MIME type 判定
├── security, security_schemes ─→ 認証ヘッダーの付与
├── skills ─────────────────────→ ※現状 ADK では直接使われていない（将来用？）
└── protocol_version ───────────→ プロトコル互換性チェック
```

**LLM が知るのは name と description のみ**。LLM はそれだけを見て「このエージェントに転送するかどうか」を判断する。残りの技術的な詳細はフレームワークが裏で処理する。

---

## 現状の制限（TODOコメントから読み取れるもの）

1. **DataPart の型判別が ADK 独自**: A2A プロトコルに function_call 等の正式定義がまだない。metadata の type キーで暫定対応中
2. **Artifact の部分更新は無視**: `append=True` のチャンクはスキップされ、`last_chunk=True` のみ処理
3. **trace_id の伝播なし**: 前回ノートで指摘の通り、A2A エージェント間でトレースが繋がらない
