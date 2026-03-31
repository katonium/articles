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

```
| Item     | Value              |
|----------|--------------------|
| Branch   | feature/xxx        |
| Issue(s) | #N                 |
| PR(s)    | #N (status)        |
| Tasks    | X/Y completed      |
```

Follow with the task list details if tasks exist.
