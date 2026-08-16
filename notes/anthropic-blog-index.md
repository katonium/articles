# Anthropic Blog & Documentation Index for Claude Code Architecture Research

発表準備のための公式ソース一覧。自分で読んで面白かったものに ★ をつけていく。

## 必読 [CORE] — Claude Code設計思想の一次ソース

### Engineering Blog

| # | Title | URL | Summary |
|---|-------|-----|---------|
| 1 | Claude Code: Best Practices for Agentic Coding | https://www.anthropic.com/engineering/claude-code-best-practices | CC内部アーキテクチャの詳細。single-agent loop、コンテキスト管理、プロンプトエンジニアリング |
| 2 | Building Effective Agents | https://www.anthropic.com/research/building-effective-agents | エージェント設計の正典。workflows vs agents、augmented LLM、orchestrator-workers等のパターン |
| 3 | Effective Context Engineering for AI Agents | https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents | コンテキストウィンドウ管理の設計原則。Claude Codeの設計根拠を理解する鍵 |
| 4 | Effective Harnesses for Long-Running Agents | https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents | 長時間エージェントのハーネス設計。Opus lead + Sonnet subagentで90.2%向上のベンチマーク |
| 5 | Harness Design for Long-Running Application Development | https://www.anthropic.com/engineering/harness-design-long-running-apps | 「コンテキスト保持」より「外部状態への永続化」。Context resetは必須という設計判断 |
| 6 | Building Agents with the Claude Agent SDK | https://www.anthropic.com/engineering/building-agents-with-the-claude-agent-sdk | Agent SDKの設計思想。CC同等のtools/agent loop/context managementをプログラマブルに |
| 7 | Writing Effective Tools for AI Agents | https://www.anthropic.com/engineering/writing-tools-for-agents | ツール設計のベストプラクティス。「高信号情報」「セマンティック識別子」 |
| 8 | Building a C Compiler with a Team of Parallel Claudes | https://www.anthropic.com/engineering/building-c-compiler | Agent Teamsの実践ケーススタディ |
| 9 | How We Built Our Multi-Agent Research System | https://www.anthropic.com/engineering/multi-agent-research-system | Anthropic社内のマルチエージェントアーキテクチャ |
| 10 | Claude Code Auto Mode: A Safer Way to Skip Permissions | https://www.anthropic.com/engineering/claude-code-auto-mode | auto modeの分類器ベース設計。安全性とUXのトレードオフ |
| 11 | Making Claude Code More Secure and Autonomous (Sandboxing) | https://www.anthropic.com/engineering/claude-code-sandboxing | サンドボックスアーキテクチャ。ファイルシステム分離、ネットワーク分離 |
| 12 | Advanced Tool Use | https://www.anthropic.com/engineering/advanced-tool-use | Tool Search Tool、Programmatic Tool Calling、Tool Use Examples |
| 13 | The "Think" Tool | https://www.anthropic.com/engineering/claude-think-tool | Extended ThinkingとThinkツールの違い。航空業界ドメインで54%改善 |
| 14 | Equipping Agents with Agent Skills | https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills | スキルの設計思想。Progressive Disclosure |

### News / Announcements

| # | Title | URL | Summary |
|---|-------|-----|---------|
| 15 | Claude Code Launch (with 3.7 Sonnet) | https://www.anthropic.com/news/claude-3-7-sonnet | Claude Code初公開。Research Preview |
| 16 | Introducing Claude 4 | https://www.anthropic.com/news/claude-4 | CC GA化。GitHub Actions統合、SDK公開 |
| 17 | Introducing Claude Sonnet 4.5 | https://www.anthropic.com/news/claude-sonnet-4-5 | チェックポイント機能追加、Agent SDK公開 |
| 18 | Introducing Claude Opus 4.6 | https://www.anthropic.com/news/claude-opus-4-6 | Agent Teams対応、長時間タスク耐性向上 |
| 19 | Anthropic Acquires Bun / $1B Milestone | https://www.anthropic.com/news/anthropic-acquires-bun-as-claude-code-reaches-usd1b-milestone | Bunランタイム買収。CCが6ヶ月で$1B達成 |
| 20 | How Anthropic Teams Use Claude Code | https://www.anthropic.com/news/how-anthropic-teams-use-claude-code | Anthropic社内でのCC活用事例 |
| 21 | Model Context Protocol | https://www.anthropic.com/news/model-context-protocol | MCP発表。N×M問題の解決、LSPインスピレーション |
| 22 | Prompt Caching | https://www.anthropic.com/news/prompt-caching | Prompt Caching発表。90%コスト削減、85%レイテンシ削減 |
| 23 | Extended Thinking | https://www.anthropic.com/news/visible-extended-thinking | Extended Thinking発表 |
| 24 | Tool Use GA | https://www.anthropic.com/news/tool-use-ga | ツール使用のGA。tool_use/tool_resultプロトコル |
| 25 | Claude Code Plugins | https://www.anthropic.com/news/claude-code-plugins | プラグインシステム |
| 26 | Claude Code on the Web | https://www.anthropic.com/news/claude-code-on-the-web | Web版CC。クラウド管理インフラ |
| 27 | Enabling Claude Code to Work More Autonomously | https://www.anthropic.com/news/enabling-claude-code-to-work-more-autonomously | 自律性拡大の発表 |

### Webinars（動画で設計思想を語っているもの）

| # | Title | URL | Summary |
|---|-------|-----|---------|
| 28 | Claude Code Live: Origin Story, Live Demos | https://www.anthropic.com/webinars/claude-code-live | CCクリエイターによる開発経緯 |
| 29 | Claude Code Advanced Patterns | https://www.anthropic.com/webinars/claude-code-advanced-patterns | サブエージェント、MCP、大規模リポジトリ対応 |
| 30 | Claude Code for Service Delivery | https://www.anthropic.com/webinars/claude-code-service-delivery | Boris Cherny (Head of CC) のQ&A |

## 発表で引用したいキー文献（優先度順）

1. **Building Effective Agents** (#2) — 「なぜsingle-agent loopなのか」の根拠
2. **Effective Context Engineering** (#3) — Context Rotの公式見解
3. **Effective Harnesses for Long-Running Agents** (#4) — マルチエージェントのベンチマーク
4. **Prompt Caching** (#22) — prefix matchキャッシュの設計
5. **Claude Code: Best Practices** (#1) — CC自体のアーキテクチャ
6. **The "Think" Tool** (#13) — Thinking vs Think Toolの設計判断
7. **Building a C Compiler** (#8) — Agent Teamsの実例

## API / Documentation

| Title | URL | Summary |
|-------|-----|---------|
| Prompt Caching Docs | https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching | tools→system→messagesの順序、TTL、コスト計算 |
| Context Windows Guide | https://platform.claude.com/docs/en/build-with-claude/context-windows | Context Rot、Thinking Block Stripping |
| Tool Use Overview | https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview | content-blockアーキテクチャ |
| Hooks Guide | https://docs.anthropic.com/en/docs/claude-code/hooks-guide | フックのライフサイクルイベント |
| Subagents Docs | https://docs.anthropic.com/en/docs/claude-code/sub-agents | サブエージェントの仕組み |
| Security Docs | https://docs.anthropic.com/en/docs/claude-code/security | 権限モデル、サンドボックス |
| Claude Code Release Notes | https://docs.anthropic.com/en/release-notes/claude-code | 全リリース履歴 |
