Worktree 上下文：
- 你当前在 git worktree 中工作，不是主仓库目录。在执行任何破坏性或跨分支操作（push、force-push、删分支、reset --hard）之前，先和用户确认分支和 worktree 路径。commit 只针对当前 worktree 所在的分支。
