# 单文件守卫调用记录（2026-06-04）

## 结论

推荐 agent 每改完一个 Go 文件后，从仓库根目录运行：

```bash
./scripts/test_with_guard.sh <file.go>
```

只传入 Go 文件路径时，输出契约是：

- exit 0：无违规，不输出内容。
- exit 1：有违规，stderr 只输出具体违规项。

该模式是单文件级守卫，只覆盖文件级规则，例如读文件、Go parse、文件长度、函数长度、嵌套深度、CC、命名下划线等。它不跑全仓 baseline、包文件数、包行数、pattern guard 或 go test。

## 我实际怎么跑

### 干净文件验收

```bash
tmpdir="$(mktemp -d)"
printf 'package sample\n\nfunc ok() {}\n' > "$tmpdir/clean.go"
./scripts/test_with_guard.sh "$tmpdir/clean.go"
```

观察结果：

```text
status=0
stdout/stderr 为空
```

### 违规文件验收

```bash
tmpdir="$(mktemp -d)"
printf 'package sample\n\nfunc bad_identifier_with_too_many_underscores() {}\n' > "$tmpdir/violation.go"
./scripts/test_with_guard.sh "$tmpdir/violation.go"
```

观察结果：

```text
status=1
命名  /tmp/.../violation.go:3  'bad_identifier_with_too_many_underscores' 下划线超过 3 个
```

### 回归测试

```bash
./scripts/test_with_guard.sh ./scripts -run 'Test(CodeSizeGuardSingleGoFile|TestWithGuardSingleGoFile|AgentDocsRequireSingleFileGuard)' -count=1
```

### 仓库守卫

```bash
make guard
```

## 工具调用问题记录

### 1. 旧仓库命令不可用于当前仓库

不要使用：

```bash
go -C backend run ./cmd/code_guard ./cmd/code_guard/preventive_rules_test.go
```

原因：

- 当前仓库没有 `backend/` 子模块。
- 当前仓库没有 `cmd/code_guard` 入口。
- 当前真实入口是 `./scripts/test_with_guard.sh` 和 `./scripts/code_size_guard.go`。

### 2. 直接 `go run` 传外部 Go 文件会被 Go 工具误解

不要这样调用：

```bash
go run ./scripts/code_size_guard.go /tmp/file.go
```

Go 会把两个 `.go` 文件都当成参与编译的 named files，遇到跨目录文件时报：

```text
named files must all be in one directory
```

如果必须直接调用底层守卫，应使用 `--` 把后续路径作为程序参数：

```bash
go run ./scripts/code_size_guard.go -- /tmp/file.go
```

日常仍推荐使用 wrapper：

```bash
./scripts/test_with_guard.sh /tmp/file.go
```

### 3. 裸 `go run` 会追加 `exit status 1`

底层程序 exit 1 时，`go run` 会额外打印：

```text
exit status 1
```

这不是守卫自己的违规输出，会污染“只输出具体违规项”的契约。因此 `./scripts/test_with_guard.sh <file.go>` 在单文件模式下会过滤这行 Go 工具层噪声。

### 4. 脚本里捕获 exit 1 要关闭 `set -e`

验收正反例时，如果 shell 启用了 `set -e`，违规文件的 exit 1 会提前中断脚本。应使用：

```bash
set +e
out="$(./scripts/test_with_guard.sh "$file" 2>&1)"
status=$?
set -e
```

这样才能同时检查 status 和输出内容。

### 5. 启动项目时遇到的环境问题

在深层 worktree 下运行 `./run-new-ui-desktop.sh` 时，本地 PostgreSQL 默认 socket 目录过长，日志显示：

```text
Unix-domain socket path ".../.tmp/pgsocket/.s.PGSQL.55437" is too long
```

解决方式是使用短 runtime 目录：

```bash
SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR=/tmp/sd-pg-bw-20260604 ./run-new-ui-desktop.sh
```

另外，首次在新 worktree 启动桌面后端前，如果 `cmd/agent-terminal/frontend/dist` 缺失，会触发 Go embed 错误：

```text
cmd/agent-terminal/frontend.go:12:12: pattern all:frontend/dist: no matching files found
```

解决方式：

```bash
make frontend-build
```

该命令会构建 `frontend-app/dist` 并同步到 legacy embed 目录。生成内容是 gitignored 启动资源，不应混入提交。
