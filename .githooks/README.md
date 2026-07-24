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
| `pre-commit` | `git commit` | 从 staged index 快照按计划刷新并 stage 代码地图生成物；拒绝 partial index、staged/worktree 不一致和代码提交夹带的额外 worktree 输入；再由 AI maintenance 按变更面执行后端快速守卫/包测试或前端 lint、架构门禁和定向测试 | 普通后端变更只测直接包和一级反向依赖；前端构建与性能验证延后到 push；可缓存绿色 gate 绑定同一 staged tree，空白检查不缓存 |
| `commit-msg` | `git commit` | 要求提交标题包含中文；提交正文如果存在也必须包含中文；提交主题属于 `fix` / `hotfix` / `bugfix` / `修复` 时，要求同一提交修改锁定 bug 的测试、fixture、golden 或 snapshot | <1 秒 |
| `pre-push` | `git push` | 只允许推送当前 `HEAD`；检查中文提交与 fix 测试；AI maintenance 补跑其余 archtest、nilness 和独立 `-race` 风险面；单提交且 tree 相同时可复用 pre-commit 的共同绿色 gate；skill 保留独立路径门禁 | 视变更面而定 |

`pre-commit` 先从 staged index 导出临时快照并生成 AI maintenance plan。只有命中生成输入时才刷新对应的 codemap、project-map 或 capability contract；project-map 的影响面与生成器索引根一致，路径或文件大小变化不会被漏掉。刷新成功的 codemap/project-map 在同一 staged tree 上标记为已预验证，不再紧接着重复跑等价 check。随后 hook 拒绝 partial commit 临时 index 与真实 index 不一致、任意 staged 非删除文件与 worktree 不一致，以及代码/门禁提交存在额外未暂存或未跟踪输入；Go 格式和受影响包预检完成后，AI maintenance 会在由最终 staged tree 展开的临时 linked worktree 中运行，原工作区在长门禁期间的编辑不会进入本轮验证输入。

绿色 gate 缓存位于 `.build-cache/ai-maintenance-gates/`，有效期 10 分钟。后端测试、推送级 archtest、race、nilness、LSP diagnostics、前端 lint/test/embed、project-map、capcontract、SQLC 和 AI maintenance 自测均可缓存；`codemap:check` 与空白检查仍每次真实执行。指纹绑定不可变 Git tree、隔离 index、变更计划（不含仅在 push 追加的 gate 名）、工具链和稳定环境；命中前以及发布 marker 前都会重新计算。pre-push 仅在恰好推送一个提交、tracked worktree/index 干净且 `HEAD^{tree}` 与 cache scope 一致时传入同一缓存，其他范围一律真实执行，不把不可比较输入当成绿色。

`commit-msg` 要求标题包含中文，正文如果存在也必须包含中文，并用提交主题识别 fix 类提交。普通后端 pre-commit 只运行代码守卫、三项规范架构事实测试、直接变更包及其一级生产/测试反向依赖；Go 模块、archtest 或代码守卫/wrapper 变更仍走完整面。`pre-push` 再追加其余 archtest、nilness 和登记并发包的独立 `-race` lane，不再把普通测试和 race 合并后重复执行。copylocks 也只覆盖本次命中的 provider/platform/thread 包。前端 pre-commit 按路径分类：生产脚本运行 lint、架构门禁、关键类型检查和定向测试；测试文件不触发 embed；样式和静态资源不触发 JS lint；普通文档不触发前端命令。Vite build、embed 和性能验证只在 push 范围命中其真实输入时运行。`frontend:embed-verify` 包含唯一一次 build，且 `npm run build` 是同步嵌入产物的唯一 owner，Make 不再重复同步。capcontract 由 AI plan 统一路由，skill mirror 保留独立路径门禁。两个 hook 都不执行 `gosec` 或前端 e2e；安全扫描保持在 hook 之外按需显式执行。

## 前端门禁健康检查

`make frontend-gate-health` 执行前端 gate-plan 路由矩阵、npm/Make/AI runner 调用图、死亡目标、直接/间接调用环和单次构建同步 owner 的回归检查。它是夜间完整巡检的快速防腐入口，不替代交付前的完整前端验证：

```bash
make frontend-gate-health
cd frontend-app
npm run lint
npm test
npm run build
```

调用图检查会解析 `package.json` 中的 `npm run` 边、Make target 依赖和 recipe 中的 npm/Make 边，并显式登记 AI frontend runner 的动态调用边；未知 npm 目标、死亡节点或任意环都会 Fail-Fast。

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

冷启时 Go/Node 仍需要重建缓存。普通提交优先走快速架构事实测试、直接包与一级反向依赖；10 分钟内同一不可变 tree 的可缓存绿色 gate 可复用。单提交 pre-push 可继承相同 tree 的共同结果，再只补推送级风险面；多提交、新分支或 tracked 状态不一致时不复用。任何实际执行的 codemap/project-map 非零退出仍会直接阻断，不存在软绿。

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
- **环境降噪**：pre-commit / pre-push 都固定清空 `GOFLAGS` 并清理 Git hook 环境，使同 tree 的 Go 门禁与缓存指纹可比较
- **CI 可短路 hook 检查提示**：`MAKE_HOOK_CHECK=0 make build` 可关闭 build 末尾的本地 hooksPath 提示，避免 CI 日志噪声
- **失败信息不自动进 agent 上下文**：你需要复制错误给 agent，让 agent 改

## 修改钩子内容

直接编辑 `.githooks/pre-commit`、`.githooks/commit-msg` 或 `.githooks/pre-push`，git 追踪它们，提交后所有装了 hook 的人下次 pull 自动生效。
