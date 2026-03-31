---
title: "シェル開きすぎて迷子になったのでClaude Codeに教えてもらうSKILL作った"
emoji: "☁️"
type: "idea" # tech: 技術記事 / idea: アイデア
topics: ["ai", "aiエージェント", "claudecode"]
published: true
---

Vibe Codingのために複数シェルを開いて並行開発をしていたらどこでなにをやっているのか完全にわからなくなってしまったので、Claude Codeに教えてもらいました。

## 背景

最近の休日ではClaude Codeのおかげでコーディングの時間は減り、変わりにこんな感じの真っ黒な画面と向き合い、祈る時間が増えています。

![真っ黒な画面](/images/i-don-know-where-u-are-inoru.png)
*真っ黒な画面と向き合う時間が増えている*

たくさんのシェル、たくさんのIssue、たくさんのバグ、、、向き合いすぎてどこでなにをやっているのか完全にわからなくなってしまいました。

## 解決策

`/where-you-are` というSKILLを作ってClaude Codeに教えてもらうことにしました。

作成したスキル

```markdown
---
name: where-you-are
description: Summarize current work context. Use when the user asks "where are you", "what are you working on", "status", or invokes /where-you-are. Reports current issue numbers, PR numbers, branch names, and task progress.
---

# Where You Are

Report current work context by gathering and displaying:

1. **Current branch**: Run `git branch --show-current`
2. **Task list**: Use `TaskList` to show all tasks and their status
3. **Related issues/PRs**: Run `gh pr list --repo katonium/infra --state open --head $(git branch --show-current)` to find PRs for the current branch, and extract linked issue numbers from PR bodies
4. **Git worktrees**: Run `git worktree list` to show active worktrees

Present a concise summary table:

\```
| Item     | Value              |
|----------|--------------------|
| Branch   | feature/xxx        |
| Issue(s) | #N                 |
| PR(s)    | #N (status)        |
| Tasks    | X/Y completed      |
\```

Follow with the task list details if tasks exist.

```

実際に `/where-you-are` を呼び出すと、こんな感じで現在のブランチ、関連するIssueやPR、タスクの進捗などをまとめて教えてくれるようになりました。

```markdown
  現在の作業状況                                                           
                                                                           
  ┌──────────┬──────────────────────────────────┐                          
  │   Item   │              Value               │                          
  ├──────────┼──────────────────────────────────┤                          
  │ Branch   │ feature/update-worktree-guidance │                          
  ├──────────┼──────────────────────────────────┤                          
  │ Issue(s) │ #23                              │                          
  ├──────────┼──────────────────────────────────┤                          
  │ PR(s)    │ #23 (open, review requested)     │                          
  ├──────────┼──────────────────────────────────┤                          
  │ Tasks    │ 2/3 completed                    │                 
  └──────────┴──────────────────────────────────┘                          
                                                                  
  タスク一覧                                                               
                                                                  
  ┌─────┬─────────────┬────────────────────────────────────────────────┐   
  │ ID  │   Status    │                      Task                      │ 
  ├─────┼─────────────┼────────────────────────────────────────────────┤   
  │ 1   │ ✅          │ CLAUDE.mdのWorktreeセクションをisolation:      │   
  │     │ completed   │ "worktree"方式に更新                           │ 
  ├─────┼─────────────┼────────────────────────────────────────────────┤   
  │ 2   │ ✅          │ 手動ブランチ作成の手順を削除し、Agent          │
  │     │ completed   │ toolの自動ブランチ作成に統一                   │   
  ├─────┼─────────────┼────────────────────────────────────────────────┤
  │ 3   │ 🔄          │ PRを作成してレビュー依頼                       │   
  │     │ in_progress │                                                │   
  └─────┴─────────────┴────────────────────────────────────────────────┘
                                                                           
  アクティブなWorktree                                                     
  
  /workspace/infra                                           
  4ff7cf2 [main]                                                           
  /workspace/infra/.claude/worktrees/agent-b7e21f3a
  a1c9d4e [feature/update-worktree-guidance]      
```

## 使ってみたい人向け

[SKILL.md](https://github.com/katonium/articles/blob/zenn/main/.claude/skills/where-you-are/SKILL.md)をGitHubに置きました。これをいい感じにコピペしてもらえればと思います。

Claude Codeに下記のように頼むとやってくれると思います。（たぶん。未検証です。）

```
`/where-you-are` スキルをこのワークスペースで利用できるようにしてください。
下記のリンクのSKILL.mdを参考に、現在の作業状況をまとめて報告するスキルを作成してください。
https://github.com/katonium/articles/blob/zenn/main/.claude/skills/where-you-are/SKILL.md
```


## まとめ

完全にVibeで作成したSkillなので改善点はかなりあると思うのですが、頭のリソースを割かずに今の状況を把握できるようになったので、かなり助かっています。

1. 繰り返し作業はSKILLにすることを意識する
2. 自分のリソースは有限なので、単純作業は極力AIに任せて、スケール可能な方法があればそちらを選ぶ

のあたりは大事だなと改めて思いました！

では！

