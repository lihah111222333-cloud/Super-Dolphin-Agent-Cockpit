# .githooks — 项目级 git client hook

仓库共享的 git 客户端钩子，在**本地** `git commit` / `git push` 时自动跑代码检查。
跟 GitHub Actions / 任何远端 CI 完全无关，纯本地工作流约束。

## 一次性激活

clone 仓库后必须跑一次：

```bash
make install-hooks
```

底层做的事：`git config core.hooksPath .githooks`（仅本仓库 local 配置，不影响别仓）。

> 用**相对路径**是为了让 `git worktree add` 创建的 linked worktree 在各自工作区根目录解析 `.githooks`。
> 如果旧配置仍指向主仓绝对路径，进 linked worktree 后重跑 `make install-hooks`。

之后所有 `git commit` / `git push` 自动经过对应 hook。

## 钩子清单

| Hook | 触发 | 做什么 | 大约耗时 |
|---|---|---|---|
| `pre-commit` | `git commit` | 从 staged index 快照刷新并 stage 代码地图生成物；拒绝 partial index、staged/worktree 不一致和代码提交夹带的额外 worktree 输入；再由 AI maintenance 按变更面执行唯一一轮后端守卫/包测试或前端 lint/test/build；不执行全量 LSP diagnostics。只有 project-map 检查可按严格输入指纹缓存 10 分钟 | 相同 project-map 输入可命中缓存；codemap、代码测试和前端检查每次真实执行；空白检查绑定 staged index |
| `commit-msg` | `git commit` | 要求提交标题包含中文；提交正文如果存在也必须包含中文；提交主题属于 `fix` / `hotfix` / `bugfix` / `修复` 时，要求同一提交修改锁定 bug 的测试、fixture、golden 或 snapshot | <1 秒 |
| `pre-push` | `git push` | 只允许推送当前 `HEAD`；检查中文提交与 fix 测试；AI maintenance 运行受影响包、nilness，并对登记的并发面运行 `-race`；capcontract 和 skill 保留独立路径门禁；不执行全量 LSP diagnostics，也不运行 `gosec` | 视变更面而定 |

`pre-commit` 每次提交都会先从 staged index 导出临时快照，运行 `make codemap-refresh` 和 `make project-map-refresh PROJECT_MAP_ARGS=--filesystem-scan`，再将根 `README.md`、`docs/doc/codemap/13-archtest-boundaries.md`、`docs/doc/codemap/README.md`、`docs/doc/codemap/ai-index.json` 和整个 `docs/doc/codemap/project-map/` 精确 `git add -A` 回当前提交。随后 hook 拒绝 partial commit 临时 index 与真实 index 不一致、任意 staged 非删除文件与 worktree 不一致，以及代码/门禁提交存在额外未暂存或未跟踪输入；Go 格式和受影响包预检完成后，AI maintenance 会在由最终 staged tree 展开的临时 linked worktree 中运行，原工作区在长门禁期间的编辑不会进入本轮验证输入。

pre-commit 的绿色 gate 缓存位于 `.build-cache/ai-maintenance-gates/`，有效期 10 分钟，并采用显式白名单：当前只有 `project-map:check` 可以缓存；会遍历 ignored 源码的 `codemap:check` 每次真实执行。hook 先从 staged tree 创建与真实 index 隔离的私有 index，再把同一 tree 展开到临时 linked worktree；缓存构造器会校验该 index 不是实时 index 且 `write-tree` 与 cache-scope 完全一致。project-map 指纹只包含其真实输入闭包：staged tree、隔离 index 的 `git ls-files -s -z`、UTC 日期、临时 staged worktree 的 tracked 差异、gate plan、工具版本和稳定环境变量；不再无界读取与 project-map 无关的 untracked 文件。规则 override 必须是仓库内普通文件，symlink 会 fail-fast。命中返回前与绿色 marker 发布前都会重新计算指纹。行为测试、前端 lint/test/build、ignored embed 产物验证和 SQLC 再生成检查每次真实执行。缓存只用于 pre-commit；pre-push 不传缓存参数，并对每个实际 push range 执行空白检查；无共同祖先的新分支按 empty tree 到 HEAD 的完整文件面路由 gate。

`commit-msg` 要求标题包含中文，正文如果存在也必须包含中文，并用提交主题识别 fix 类提交。`pre-push` 从本次 push range 计算变更路径：先校验中文要求和 fix-test 规则，再以显式 `--push-gates` 调用一次 `scripts/ai_maintenance_gates.sh`；受影响 Go 包增加 nilness，登记并发面增加 `-race`，普通 pre-commit 计划不含这两项。capcontract 和 skill mirror 仍是独立路径门禁。`pre-commit` / `pre-push` 都不把 changed files 扩展成全量 LSP diagnostics 执行；LSP 定位、理解、影响面和诊断仍由实现/审查流程按变更面定向取得。两个 hook 都设置 `SUPER_DOLPHIN_GITHOOK_SKIP_GOSEC=1`，不执行 `gosec`，也不执行前端 e2e；安全扫描保持在 hook 之外按需显式执行。

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

### staged / worktree 不一致拦下

```
❌ 以下 staged Go 影响包内的 .go 文件还有未暂存或未跟踪 worktree 改动：
  internal/foo/bar.go

  请先 git add 这些文件，或还原/删除未暂存改动。
  否则 gofmt/go vet/go test 检查的不是将被提交的内容。
```

先 `git add internal/foo/bar.go`，或还原未暂存改动后重新 commit。

如果看到“staged .go 文件在 worktree 中不存在”，通常是 `git add new.go` 后又 `rm new.go` 的 AD 状态；请 `git restore --staged new.go` 或恢复文件后重新 `git add`。

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

给同一个提交补上能复现并锁定该 bug 的 `*_test.go`、`*.test.ts`、`*.test.mjs`、`tests/**`、`testdata/**`、`fixtures/**`、`golden/**` 或 snapshot 变更后重新 commit。
`frontend-app/src/**` 的 UI 修复也可以用同提交的 `frontend-app/scripts/*.test.mjs` 集成/契约测试锁定。

### pre-push（AI maintenance 变更面门禁失败）

```
[pre-push] AI maintenance gates...
FAIL  github.com/.../internal/app    0.5s

ai-maintenance: execute gate plan: exit status 1
```

`pre-push` 会拒绝 `local_sha != HEAD` 的显式 ref 推送；本地未提交、未暂存或未跟踪内容不属于本次 push range，不会阻断 pre-push。失败时查看 AI maintenance 输出中的首个失败 gate。

## 诊断

| 现象 | 检查命令 |
|---|---|
| commit 一直被拒看不懂错误 | `bash .githooks/pre-commit` 直接跑一遍看完整输出；fix-test 规则可用 `bash .githooks/commit-msg .git/COMMIT_EDITMSG` 复现 |
| 想确认 hook 装没装 | `git config --get core.hooksPath`（应输出 `.githooks`） |
| 怀疑 hook 没执行 | `git commit -v` 看完整流程；或临时 `git commit --no-verify` 比对 |
| build/test 时看到"git hooks 未装"提示 | 跑一次 `make install-hooks` |

## FAQ

### IDE / GUI 提交会不会跑？

会。Git hook 是 Git 客户端行为，只要 IDE / GUI 最终调用的是这个仓库的 `git commit` / `git push`，就会走 `core.hooksPath` 指向的脚本。若 IDE 内置 Git 工作目录不同，请先在 IDE 终端里确认：

```bash
git config --get core.hooksPath
```

### 为什么第一次很慢？

冷启时 Go 需要重建测试缓存。`pre-commit` 的行为测试、codemap 和前端检查每次真实执行；10 分钟内完全相同的 staged tree、隔离 index、临时 staged worktree、UTC 日期、工具链和环境只复用 project-map 绿色检查。`pre-push` 由 AI maintenance 单次执行变更面门禁；codemap/project-map 非零退出会直接阻断，不存在软绿。

### 清了 GOCACHE 会怎样？

`go clean -cache -testcache` 或系统清理缓存后，下一次 hook 会回到冷启耗时，这是正常现象，不代表 hook 卡死。

### rebase / cherry-pick / merge / revert 中间提交会怎样？

`pre-commit` / `commit-msg` 不覆盖所有 sequencer 自动产生的中间提交；这是 Git 客户端 hook 的结构性限制。`pre-push` 会在最终 push 前要求每个非删除 ref 的 `local_sha` 等于当前 `HEAD`，再检查本次 push 范围内所有 fix commits 都带锁定 bug 的测试，最后由 AI maintenance 单次执行受影响变更面的门禁。

### Linux 能用吗？

脚本只依赖 bash、git、go、make 和 POSIX 常见工具；没有用 `flock` / `timeout`。当前主验证环境是 macOS bash 3.2 + BSD `mktemp`，Linux bash 一般可用。路径枚举使用 `git diff --cached --name-status -z`，可正确处理空格/中文路径；首次接入请先跑 `make install-hooks && bash .githooks/pre-commit` 自检。

## 卸载

```bash
git config --unset core.hooksPath
```

重新激活就 `make install-hooks`。

## 设计要点

- **本地工作流约束**：只在你的电脑上跑，不影响远端仓库或他人
- **同事不装即裸推**：core.hooksPath 仅本机生效。要让所有人都用，需要每个人各自 `make install-hooks`
- **fix 必须带回归测试**：`fix` / `hotfix` / `bugfix` / `修复` 提交必须在同一提交修改测试、fixture、golden 或 snapshot；commit-msg 拦当前提交，pre-push 拦历史补推
- **rebase / amend 也跑**：交互式人工提交会跑；sequencer 自动中间提交不保证由 pre-commit / commit-msg 拦截，最终由 pre-push 兜底
- **环境降噪**：pre-commit 会清空 `GOFLAGS`；pre-push 会清理 Git hook 环境后运行 AI maintenance 变更面门禁
- **CI 可短路 hook 检查提示**：`MAKE_HOOK_CHECK=0 make build` 可关闭 build 末尾的本地 hooksPath 提示，避免 CI 日志噪声
- **失败信息不自动进 agent 上下文**：你需要复制错误给 agent，让 agent 改

## 修改钩子内容

直接编辑 `.githooks/pre-commit`、`.githooks/commit-msg` 或 `.githooks/pre-push`，git 追踪它们，提交后所有装了 hook 的人下次 pull 自动生效。
