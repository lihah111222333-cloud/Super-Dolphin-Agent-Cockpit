# .githooks — 项目级 git client hook

仓库共享的 git 客户端钩子，在**本地** `git commit` / `git push` 时自动跑代码检查。
跟 GitHub Actions / 任何远端 CI 完全无关，纯本地工作流约束。

## 一次性激活

clone 仓库后必须跑一次：

```bash
make install-hooks
```

底层做的事：`git config core.hooksPath /仓库绝对路径/.githooks`（仅本仓库 local 配置，不影响别仓）。

> 用**绝对路径**是为了让 `git worktree add` 创建的 linked worktree 也能正确找到 hook。
> 仓库被重命名/移动后需重跑 `make install-hooks` 更新路径。

之后所有 `git commit` / `git push` 自动经过对应 hook。

## 钩子清单

| Hook | 触发 | 做什么 | 大约耗时 |
|---|---|---|---|
| `pre-commit` | `git commit` | 基于 **index 快照**检查 staged 代码：后端 Go 变更跑 `gofmt`、后端代码守卫、`go vet`、变更包及受影响包测试；前端变更跑对应前端守卫/测试；纯文档不跑代码守卫 | 视影响面而定 |
| `commit-msg` | `git commit` | 要求提交标题包含中文；提交正文如果存在也必须包含中文；提交主题属于 `fix` / `hotfix` / `bugfix` / `修复` 时，要求同一提交修改锁定 bug 的测试、fixture、golden 或 snapshot | <1 秒 |
| `pre-merge-commit` | 自动 merge commit | 基于 merge 后的 **index 快照**跑与 `pre-commit` 相同的代码检查，防止 merge 引入未过守卫的已有提交 | 视影响面而定 |
| `pre-push` | `git push` | 不要求 worktree 干净；基于将要推送的 commit 快照检查 push range：标题/正文中文、fix-test、后端守卫、变更包及受影响包测试、前端守卫/测试；纯文档不跑代码守卫 | 视影响面而定 |

`pre-commit` 和 `pre-merge-commit` 不读取脏 worktree 内容，而是把当前 index 写成临时快照后检查，所以工作区存在未提交改动不会提前拦截提交，也不会污染将要提交的内容。`commit-msg` 要求标题包含中文，正文如果存在也必须包含中文，并用提交主题识别 fix 类提交。`pre-push` 不要求 worktree/index/untracked 干净；它从 push range 计算变更路径，并在临时 worktree 中 checkout 将要推送的 commit 后运行代码检查。后端 Go 代码会跑守卫、`go vet`、变更包及反向依赖受影响包测试；前端代码会按包运行守卫/测试；只有文档/其它非代码变更时不跑代码守卫。所有 hook 都**不**做格式自动修复，只拦下不通过的提交/推送。

CI 也会在 `.github/workflows/ci.yml` 的 `commit-guard` job 中运行 `scripts/ci_commit_guard.sh`：它按 GitHub `pull_request` / `push` 事件解析提交范围，先复用 `scripts/guard_commit_titles.sh --range` 要求范围内每个 commit 的标题包含中文，且非空正文也包含中文，再复用 `scripts/guard_fix_commits_have_tests.sh --range` 拦截未安装 hook 或绕过 hook 后进入 PR / main 的 fix 类提交。正文为空允许；正文一旦存在，纯英文正文会失败。

## 跳过钩子（仅限紧急）

> ⚠️ **仓库规约 `docs/1/会话习惯.md` §10.12«禁止 bypass pre-commit hook»明文禁止常态 bypass。**
> `--no-verify` 是 git 自带的逃生口，仅允许在以下场景使用：
>
> 1. 机器坏了 / hook 本身有 bug 拦不住你（此时也请同步报告 hook 作者）
> 2. 紧急 hot-fix 必须立刻 push，上线后补上错过的检查
> 3. WIP 分支推 fork 让同事看，然后马上修
>
> **常态用 `--no-verify` = 违反仓库规约**。

```bash
git commit --no-verify -m "..."   # 跳 pre-commit
git push --no-verify              # 跳 pre-push
```

## 失败示例

### gofmt 拦下

```
[pre-commit] gofmt...
❌ 以下文件未格式化：
internal/foo/bar.go

  一键修复（自动处理空格/中文路径）：
  gofmt -w internal/foo/bar.go

  ⚠️  紧急 bypass（违反仓库规约 docs/1/会话习惯.md §10.12«禁止 bypass pre-commit hook»，需事后补检查）：git commit --no-verify
```

直接复制底下的命令跑，再 `git add -u` 重新 commit。

### staged 快照检查

`pre-commit` 检查的是 index 快照，不要求 worktree 干净。修改了文件但没有 `git add` 的内容不会进入本次提交检查；已 `git add` 的内容会被写入临时 worktree 后运行 gofmt、守卫、vet 和测试。

### go vet 拦下

```
[pre-commit] go vet...
./bar.go:42:2: unreachable code
❌ go vet 失败
  ⚠️  紧急 bypass（违反仓库规约 docs/1/会话习惯.md §10.12«禁止 bypass pre-commit hook»、需事后补检查）：git commit --no-verify
```

按报告改代码，重新 commit。

### fix 缺少锁定 bug 测试

```
[commit-msg] fix-test guard...
❌ fix 提交缺少锁定 bug 的测试
  subject: fix: repair parser panic
  规则: fix/hotfix/bugfix/修复 提交必须在同一提交修改测试、fixture、golden 或 snapshot。
```

给同一个提交补上能复现并锁定该 bug 的 `*_test.go`、`*.test.ts`、`tests/**`、`testdata/**`、`fixtures/**`、`golden/**` 或 snapshot 变更后重新 commit。

### pre-push（包级测试失败）

```
[pre-push] go package tests: ./internal/app
FAIL  github.com/.../internal/app    0.5s

❌ Go 包级测试未通过，push 已拒绝
  ⚠️  紧急 bypass（违反仓库规约 docs/1/会话习惯.md §10.12«禁止 bypass pre-commit hook»、需事后补检查）：git push --no-verify
```

`pre-push` 不要求工作区干净。它会 checkout 将要推送的 commit 到临时 worktree 后运行检查，避免未提交改动污染 push 检查对象。

## 诊断

| 现象 | 检查命令 |
|---|---|
| commit 一直被拒看不懂错误 | `bash .githooks/pre-commit` 直接跑一遍看完整输出；fix-test 规则可用 `bash .githooks/commit-msg .git/COMMIT_EDITMSG` 复现 |
| 想确认 hook 装没装 | `git config --get core.hooksPath`（应输出本仓库的绝对路径 `.githooks`） |
| 怀疑 hook 没执行 | `git commit -v` 看完整流程；或临时 `git commit --no-verify` 比对 |
| build/test 时看到"git hooks 未装"提示 | 跑一次 `make install-hooks` |

## FAQ

### IDE / GUI 提交会不会跑？

会。Git hook 是 Git 客户端行为，只要 IDE / GUI 最终调用的是这个仓库的 `git commit` / `git push`，就会走 `core.hooksPath` 指向的脚本。若 IDE 内置 Git 工作目录不同，请先在 IDE 终端里确认：

```bash
git config --get core.hooksPath
```

### 为什么第一次很慢？

冷启时 Go 需要重建测试缓存，`pre-push` 的 Go 包级测试耗时取决于本次 push 涉及的变更包和受影响包数量。`pre-commit` 检查 staged 代码的变更包和受影响包。前端只在前端包代码变更时跑对应包的守卫/测试。

### 清了 GOCACHE 会怎样？

`go clean -cache -testcache` 或系统清理缓存后，下一次 hook 会回到冷启耗时，这是正常现象，不代表 hook 卡死。

### rebase / cherry-pick / merge / revert 中间提交会怎样？

`pre-commit` / `commit-msg` 不覆盖所有 sequencer 自动产生的中间提交；这是 Git 客户端 hook 的结构性限制。`pre-merge-commit` 会覆盖自动 merge commit 的代码检查；`pre-push` 会在最终 push 前检查本次 push 范围内所有 commit 的中文标题/正文和 fix-test 规则，并基于将要推送的 commit 快照运行后端/前端检查，确保兜底检查的是将要推送的内容。

### Linux 能用吗？

脚本只依赖 bash、git、go、make 和 POSIX 常见工具；没有用 `flock` / `timeout`。当前主验证环境是 macOS bash 3.2 + BSD `mktemp`，Linux bash 一般可用。路径枚举使用 NUL 分隔的 `git diff --name-status -z` / `git diff-tree -z`，可正确处理空格/中文路径；首次接入请先跑 `make install-hooks && bash .githooks/pre-commit` 自检。

## 卸载

```bash
git config --unset core.hooksPath
```

重新激活就 `make install-hooks`。

## 设计要点

- **本地工作流约束**：只在你的电脑上跑，不影响远端仓库或他人
- **同事不装即裸推**：core.hooksPath 仅本机生效。要让所有人都用，需要每个人各自 `make install-hooks`
- **fix 必须带回归测试**：`fix` / `hotfix` / `bugfix` / `修复` 提交必须在同一提交修改测试、fixture、golden 或 snapshot；commit-msg 拦当前提交，pre-push 拦历史补推
- **rebase / amend 也跑**：交互式人工提交会跑；sequencer 自动中间提交不保证由 pre-commit / commit-msg 拦截，自动 merge commit 由 pre-merge-commit 检查代码，最终由 pre-push 兜底
- **环境降噪**：pre-commit / pre-merge-commit 会清空 `GOFLAGS`；pre-push 会清理 Git hook 环境后跑对应检查
- **CI 可短路 hook 检查提示**：`MAKE_HOOK_CHECK=0 make build` 可关闭 build 末尾的本地 hooksPath 提示，避免 CI 日志噪声
- **失败信息不自动进 agent 上下文**：你需要复制错误给 agent，让 agent 改

## 修改钩子内容

直接编辑 `.githooks/pre-commit`、`.githooks/commit-msg`、`.githooks/pre-merge-commit`、`.githooks/pre-push` 或 `scripts/hook_code_checks.sh`，git 追踪它们，提交后所有装了 hook 的人下次 pull 自动生效。
