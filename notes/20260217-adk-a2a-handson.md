# ADK 内部実装メモ: sub_agents vs AgentTool

ADK のソースコードを読んで、sub_agents と AgentTool の内部実装の違いを整理する。

venv のパス: `.venv/lib/python3.12/site-packages/google/adk/`

---

## 関連ファイル一覧

| ファイル | 役割 |
|---------|------|
| `agents/base_agent.py` | 全エージェントの基底クラス。`sub_agents`, `parent_agent` を定義 |
| `agents/llm_agent.py` | LLM を使うエージェント。`_run_async_impl` でサブエージェント転送を処理 |
| `flows/llm_flows/agent_transfer.py` | sub_agents 用の転送指示を LLM リクエストに注入 |
| `tools/transfer_to_agent_tool.py` | `transfer_to_agent` ツールの定義。LLM が呼ぶ関数 |
| `tools/agent_tool.py` | AgentTool。エージェントをツールとしてラップ |
| `tools/base_tool.py` | ツールの基底クラス |
| `runners.py` | Runner。エージェントの実行環境 |

---

## sub_agents の呼び出しフロー

### 1. エージェントツリーの構築 (`base_agent.py`)

```python
# base_agent.py L133
class BaseAgent(BaseModel):
    sub_agents: list[BaseAgent] = Field(default_factory=list)
    parent_agent: Optional[BaseAgent] = Field(default=None, init=False)
```

`Agent()` を初期化すると `__set_parent_agent_for_sub_agents` が呼ばれ、子の `parent_agent` に自分をセットする。つまりエージェント同士が双方向に参照を持つツリー構造になる。

### 2. Flow の選択 (`llm_agent.py` L694-703)

```python
@property
def _llm_flow(self) -> BaseLlmFlow:
    if (
        self.disallow_transfer_to_parent
        and self.disallow_transfer_to_peers
        and not self.sub_agents
    ):
        return SingleFlow()  # 転送先がない → シンプルなフロー
    else:
        return AutoFlow()    # 転送先がある → 転送機能付きフロー
```

`sub_agents` が存在する場合、`AutoFlow` が選ばれる。`AutoFlow` の中で `agent_transfer.py` の `request_processor` が動く。

### 3. LLM リクエストへの転送指示の注入 (`agent_transfer.py`)

```python
# agent_transfer.py L36-67
class _AgentTransferLlmRequestProcessor(BaseLlmRequestProcessor):
    async def run_async(self, invocation_context, llm_request):
        transfer_targets = _get_transfer_targets(invocation_context.agent)

        # TransferToAgentTool を作成（enum で転送先を制限）
        transfer_to_agent_tool = TransferToAgentTool(
            agent_names=[agent.name for agent in transfer_targets]
        )

        # LLM への指示に転送ルールを追加
        llm_request.append_instructions([
            _build_transfer_instructions(...)
        ])
```

LLM に送られる指示（自動追加）:
```
You have a list of other agents to transfer to:
Agent name: math_agent
Agent description: 計算を行うエージェントです

If another agent is better for answering the question according to its
description, call `transfer_to_agent` function to transfer the question
to that agent. When transferring, do not generate any text other than the
function call.
```

### 4. 転送先の取得 (`agent_transfer.py` L141-160)

```python
def _get_transfer_targets(agent: LlmAgent) -> list[BaseAgent]:
    result = []
    result.extend(agent.sub_agents)  # 子エージェント

    if not agent.disallow_transfer_to_parent:
        result.append(agent.parent_agent)  # 親エージェント

    if not agent.disallow_transfer_to_peers:
        result.extend([...])  # 兄弟エージェント

    return result
```

**重要**: sub_agents だけでなく、親エージェントや兄弟エージェントにも転送可能。デフォルトでは双方向に行き来できる。

### 5. `transfer_to_agent` ツール本体 (`transfer_to_agent_tool.py`)

```python
# transfer_to_agent_tool.py L26-40
def transfer_to_agent(agent_name: str, tool_context: ToolContext) -> None:
    tool_context.actions.transfer_to_agent = agent_name
```

ツール自体は非常にシンプル。`tool_context.actions.transfer_to_agent` にエージェント名をセットするだけ。

`TransferToAgentTool` は `FunctionTool` を継承し、`agent_name` パラメータに enum 制約を追加している。これにより LLM が存在しないエージェント名をハルシネーションするのを防ぐ。

### 6. 転送の実行 (`llm_agent.py` L448-464)

```python
# llm_agent.py L448-464
async def _run_async_impl(self, ctx: InvocationContext):
    agent_state = self._load_agent_state(ctx, BaseAgentState)

    # 前回の呼び出しで転送が発生していた場合、そのサブエージェントを再開
    if agent_state is not None and (
        agent_to_transfer := self._get_subagent_to_resume(ctx)
    ):
        # 同じ InvocationContext でサブエージェントを実行
        async with Aclosing(agent_to_transfer.run_async(ctx)) as agen:
            async for event in agen:
                yield event  # イベントをそのまま親に流す
        return

    # 通常の LLM フロー実行
    async with Aclosing(self._llm_flow.run_async(ctx)) as agen:
        async for event in agen:
            yield event
```

**ポイント**: `agent_to_transfer.run_async(ctx)` — 同じ `ctx`（InvocationContext）を渡している。セッション、状態、メモリがすべて共有される。

### 7. サブエージェント再開の判定 (`llm_agent.py` L705-748)

```python
def _get_subagent_to_resume(self, ctx):
    events = ctx._get_events(current_invocation=True, current_branch=True)
    last_event = events[-1]

    if last_event.author == self.name:
        # 自分の最後のイベントに transfer_to_agent があるか
        return self.__get_transfer_to_agent_or_none(last_event, self.name)

    # 他のエージェントやユーザーからのイベントの場合
    # 過去のイベントを遡って最後の転送先を見つける
    for event in reversed(events):
        if agent := self.__get_transfer_to_agent_or_none(event, self.name):
            return agent
```

イベント履歴を見て `transfer_to_agent` のレスポンスがあれば、その先のエージェントに制御を渡す。マルチターンの会話でも正しく再開できる仕組み。

---

## AgentTool の呼び出しフロー

### 1. ツールとしての宣言 (`agent_tool.py` L127-185)

```python
# agent_tool.py L128-185
def _get_declaration(self) -> types.FunctionDeclaration:
    input_schema = _get_input_schema(self.agent)

    if input_schema:
        # エージェントに input_schema がある場合はそれを使う
        result = build_function_declaration(func=input_schema)
    else:
        # ない場合は汎用的な { request: string } を使う
        result = types.FunctionDeclaration(
            name=self.name,
            description=self.agent.description,
            parameters=types.Schema(
                type=types.Type.OBJECT,
                properties={
                    'request': types.Schema(type=types.Type.STRING),
                },
                required=['request'],
            ),
        )
```

LLM から見ると、AgentTool は `{ request: "..." }` を受け取る普通の関数ツール。`transfer_to_agent` のような特殊な指示は追加されない。

### 2. 実行 (`agent_tool.py` L188-276)

```python
# agent_tool.py L188-276
async def run_async(self, *, args, tool_context):
    # 1. 入力をContent形式に変換
    content = types.Content(
        role='user',
        parts=[types.Part.from_text(text=args['request'])],
    )

    # 2. ★ 新しい Runner を作成（隔離環境）
    runner = Runner(
        app_name=child_app_name,
        agent=self.agent,
        artifact_service=ForwardingArtifactService(tool_context),
        session_service=InMemorySessionService(),   # ← 新しいセッション
        memory_service=InMemoryMemoryService(),      # ← 新しいメモリ
        credential_service=tool_context._invocation_context.credential_service,
        plugins=plugins,
    )

    # 3. 新しいセッションを作成（親の状態をコピー）
    state_dict = {
        k: v for k, v in tool_context.state.to_dict().items()
        if not k.startswith('_adk')  # ADK内部状態はフィルタ
    }
    session = await runner.session_service.create_session(
        app_name=child_app_name,
        user_id=tool_context._invocation_context.user_id,
        state=state_dict,
    )

    # 4. エージェントを独立して実行
    last_content = None
    async with Aclosing(
        runner.run_async(user_id=session.user_id, session_id=session.id, new_message=content)
    ) as agen:
        async for event in agen:
            # 状態変更は親に転送
            if event.actions.state_delta:
                tool_context.state.update(event.actions.state_delta)
            if event.content:
                last_content = event.content

    # 5. Runner のクリーンアップ
    await runner.close()

    # 6. 最終出力をテキストとして返す
    merged_text = '\n'.join(
        p.text for p in last_content.parts if p.text and not p.thought
    )
    return merged_text  # ← これがツールの戻り値になる
```

**ポイント**:
- `InMemorySessionService()`, `InMemoryMemoryService()` — 毎回新しいセッションとメモリを作る
- 子エージェントの出力はテキストとして返され、親 LLM がそれを元に回答を生成する
- `state_delta` だけは親に転送される（明示的に）
- `thought` パーツはフィルタされ、最終応答のテキストだけが返る

---

## 比較まとめ

### 実行コンテキスト

```
sub_agents の場合:
┌─────────────────────────────────────────────┐
│ InvocationContext (共有)                      │
│ ┌─────────────┐    transfer    ┌───────────┐ │
│ │ parent_agent │ ──────────→  │ sub_agent  │ │
│ │              │ ←──────────  │            │ │
│ └─────────────┘    transfer    └───────────┘ │
│ Session: 共有  |  Memory: 共有  |  State: 共有 │
└─────────────────────────────────────────────┘

AgentTool の場合:
┌──────────────────────┐     ┌──────────────────────┐
│ InvocationContext     │     │ NEW Runner            │
│ ┌──────────────┐     │     │ ┌──────────────┐     │
│ │ parent_agent  │─────│─→  │ │ child_agent   │     │
│ │ (tools=[...]) │     │  text │              │     │
│ │               │←────│─── │ │              │     │
│ └──────────────┘     │     │ └──────────────┘     │
│ Session: 親の        │     │ Session: 新規(コピー) │
│ Memory: 親の         │     │ Memory: 新規(空)      │
└──────────────────────┘     └──────────────────────┘
```

### LLM から見た違い

**sub_agents**: LLM のシステムプロンプトに転送指示が注入され、`transfer_to_agent(agent_name)` を呼べる。呼ぶと会話の制御が移る。

**AgentTool**: 通常のツール `math_agent(request="100+200を計算して")` として呼べる。結果のテキストが返ってくる。会話の制御は移らない。

### イベントの流れ

**sub_agents**:
1. parent_agent が LLM を呼ぶ
2. LLM が `transfer_to_agent("math_agent")` を返す
3. `tool_context.actions.transfer_to_agent = "math_agent"` がセットされる
4. 次の `_run_async_impl` で `_get_subagent_to_resume` が math_agent を返す
5. `math_agent.run_async(同じctx)` が呼ばれる
6. math_agent のイベントが直接 yield される → ユーザーに直接届く

**AgentTool**:
1. parent_agent が LLM を呼ぶ
2. LLM が `math_agent(request="100+200を計算して")` を返す（通常のツール呼び出し）
3. `AgentTool.run_async()` が呼ばれる
4. 新しい Runner + Session が作られ、math_agent が独立実行される
5. math_agent の最終テキスト出力が返される
6. parent_agent の LLM がそのテキストを受け取り、ユーザーへの回答を生成する

### 状態の共有

- **sub_agents**: 同じ InvocationContext なので `ctx.session.state` がそのまま共有される
- **AgentTool**: 親の state をコピーして新 session を作る。子の `state_delta` は明示的に親に転送される。ただし子の session 自体は独立しており、子が終わったら破棄される

### 親エージェント・兄弟エージェントへの転送

- **sub_agents**: `disallow_transfer_to_parent` と `disallow_transfer_to_peers` で制御。デフォルトでは親や兄弟にも転送可能
- **AgentTool**: そもそも転送という概念がない。ツールとして呼ばれて結果を返すだけ


