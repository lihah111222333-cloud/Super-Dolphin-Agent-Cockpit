# Security Scan Report - feature-integration-20260617

日期：2026-06-17
分支：`codex/feature-integration-20260617`
基线提交：`809776e12182f1666b6f23c78f79c1870cd6fec2`
扫描 ID：`809776e1_20260617T153830+0800`
扫描产物目录：`D:\tmp\codex-security-scans\feature-integration-20260617\809776e1_20260617T153830+0800`

## 任务边界

本次扫描按 `codex-security:security-scan` 流程执行，结合子代理分片审查以下高风险面：

- 桌面 RPC、本地文件打开和代码打开链路。
- MCP shared file 读写删除和 workspace merge 链路。
- Provider skill/toolbridge 的 `command/exec` 能力边界。
- 前端 Markdown / HTML 渲染和本地文件链接打开行为。
- 启动脚本、Makefile、依赖安全和桌面运行路径。

本报告只总结可复现或已校准的安全问题，不把风格问题、普通可维护性问题或不可达路径计入漏洞。

## 结果总览

| ID | 风险级别（修复前） | 状态 | 问题 | 处理 |
| --- | --- | --- | --- | --- |
| `SD-DESKTOP-001` | Medium | 已修复 | Windows fallback 通过 `cmd /c start` 打开路径，路径文本中的 shell 元字符可被解释。 | 改为 shell-free `rundll32.exe url.dll,FileProtocolHandler` argv 调用，并增加回归测试。 |
| `SD-WORKSPACE-001` | Medium | 已修复 | `shared_file_read` 只做词法路径检查，项目控制的 symlink/junction 祖先可逃逸 shared-file sandbox。 | 新增 realpath 校验的读路径解析器，并覆盖 MCP 与内部 store。 |
| `SD-WORKSPACE-002` | High | 已修复 | `shared_file_write` 可通过 symlink/junction 祖先写到 sandbox 外部路径。 | 新增 realpath 校验的写路径解析器，写入前校验父目录真实路径。 |
| `SD-WORKSPACE-003` | Medium | 已修复 | workspace merge 可从 workspace 内 symlink/junction 指向的外部路径复制内容。 | merge 前校验源文件真实路径仍在 workspace root 内。 |
| `SD-PROVIDER-001` | High | 已修复 | `command/exec` 在 `allowShell=false` 时仍允许 `node`、`python`、`npm`、`go` 等代码运行时。 | 增加代码运行时 denylist，保持命令能力边界为有限命令执行而非任意代码执行。 |
| `SD-PROVIDER-002` | High | 已修复 | `command/exec` 环境继承允许 `OPENAI_`、`ANTHROPIC_`、`CODEX_`、`MCP_`、`APP_` 等敏感前缀泄露到子进程。 | 收紧环境 allowlist，仅保留测试和工具桥接所需前缀与 `LOG_LEVEL`。 |
| `SD-FRONTEND-001` | Low | 已修复 | Code preview Markdown 中无 scoped action 的 `file:` 链接会落到浏览器级打开逻辑。 | 无 scoped 文件处理器时本地文件链接仅渲染为文本。 |
| `SD-STARTUP-001` | Low / Deferred | 已校准，未改 | Makefile Frida 目标存在开发者构建变量注入面，但 checkout 中无 `build/frida-version.txt`，且目标为手动开发者操作。 | 记录为 deferred hardening，不作为本次漏洞修复项。 |

## 修复摘要

### `SD-DESKTOP-001`：Windows shell fallback 路径注入

攻击路径：

1. 前端通过 `ui/code/open` 或 `ui/path/open` 触发本地文件打开。
2. 后端已校验路径在注册项目根内，但旧 fallback 使用 `cmd /c start`。
3. 当 `code` CLI 不存在时，Windows shell 会解释路径参数里的 `&` 等元字符。

修复：

- `internal/ui/wails/code_preview.go` 改为 `rundll32.exe url.dll,FileProtocolHandler <path>`，避免 shell 解析。
- `internal/ui/wails/rpc_path_open_test.go` 增加 shell-free 命令构造测试。

### `SD-WORKSPACE-001` / `SD-WORKSPACE-002`：shared file symlink/junction sandbox escape

攻击路径：

1. 工具调用者请求 `shared_file_read` 或 `shared_file_write`。
2. 旧实现只检查清洗后的词法路径是否位于 `.agnet/shared` 下。
3. 如果 `.agnet/shared` 下某个祖先目录是 symlink/junction，真实读写目标可落到 sandbox 外。

修复：

- `internal/platform/sharedfilefs/disk.go` 新增 `ResolveReadAbs`、`ResolveWriteAbs`、`ResolveDeleteAbs`。
- 读路径在目标存在时校验真实路径；目标不存在时保留 DB fallback 行为。
- 写路径在创建文件前校验真实父目录仍位于 sandbox root 内。
- `cmd/mcp-orch/store/sharedfile/store.go`、`cmd/mcp-orch/store/sharedfile/importer.go`、`internal/store/sharedfile/store.go` 全部切换到新解析器。
- `internal/platform/sharedfilefs/disk_test.go` 增加读写 symlink escape 回归测试。

### `SD-WORKSPACE-003`：workspace merge symlink/junction source escape

攻击路径：

1. merge 任务从 workspace root 复制文件回 source root。
2. 旧逻辑对相对路径做词法限制，但复制源可经 symlink/junction 指向 workspace 外部。
3. merge 过程可能把外部文件内容写回 source root。

修复：

- `cmd/mcp-orch/workspace/service_merge.go` 增加 `ensureWorkspaceMergeSource`。
- 复制前校验源文件真实路径仍在 workspace root 内。
- `cmd/mcp-orch/workspace/service_merge_security_test.go` 增加 symlink escape 回归测试。

### `SD-PROVIDER-001` / `SD-PROVIDER-002`：`command/exec` 能力边界过宽

攻击路径：

1. toolbridge 暴露 `command/exec`。
2. 调用方即使不允许 shell，仍可直接运行代码解释器或包管理器。
3. 旧环境继承还会把 provider/API 相关敏感前缀带入子进程。

修复：

- `internal/module/skill/exec.go` 增加代码运行时 denylist。
- 环境继承改为显式 allowlist，移除 `OPENAI_`、`ANTHROPIC_`、`CODEX_`、`AGENT_`、`MCP_`、`APP_`、`MODEL` 等敏感前缀。
- `internal/module/skill/exec_test.go` 增加命令拒绝与敏感环境过滤测试。

### `SD-FRONTEND-001`：Code preview 本地文件链接打开范围不一致

攻击路径：

1. Code preview Markdown 渲染 `file:` 链接。
2. 当没有 scoped `onOpenPath` / `onFileRef` handler 时，旧逻辑会回落到浏览器打开逻辑。
3. 用户点击后可能触发不受项目 root 限制的本地文件打开。

修复：

- `frontend-app/src/pages/chat/components/MarkdownInline.jsx` 在无 scoped action 时把本地文件链接渲染为纯文本。
- `frontend-app/src/pages/chat/components/MarkdownMessage.test.jsx` 增加无 scoped action 不打开本地文件的回归测试。

## Deferred / Suppressed

### `SD-STARTUP-001`：Makefile Frida 开发者目标注入面

校准结论：

- 触发目标为开发者显式运行的 Frida 构建目标。
- 当前 checkout 中没有 `build/frida-version.txt`。
- 可利用性依赖攻击者先控制构建文件内容或 Make 变量。
- 该路径不在普通桌面启动、MCP tool 调用或前端远程输入链路上。

因此本次只记录为 deferred hardening，不改 Makefile，避免扩大当前安全修复范围。

## 验证

已运行并通过：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/platform/sharedfilefs ./internal/store/sharedfile ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-orch/workspace ./internal/ui/wails ./internal/module/skill ./internal/app ./cmd/agent-terminal -count=1
```

```powershell
cd frontend-app
npm test -- --run src/pages/chat/components/MarkdownMessage.test.jsx
npm run lint
npm audit --json
npm test
npm run build
```

验证结果：

- Go package guard/test 全部通过。
- 前端目标测试通过。
- 前端全量 lint、audit、test、build 全部通过。
- `npm audit --json` 报告 `total: 0`。
- `npm run build` 仅保留既有 Vite chunk size warning。

单文件守卫说明：

- 按仓库规则，修改 Go 文件后已先运行单文件守卫。
- 部分单文件测试入口因只编译单个文件而缺少同包符号，出现 `undefined` 编译错误；对应 package-level guard/test 已通过，作为最终有效验证。

## 启动证明

本分支已启动并可测试：

- 前端：`http://127.0.0.1:5175`
- 桌面后端 HTTP：`http://127.0.0.1:4512`
- 指标：`http://127.0.0.1:4512/metrics`
- 工具桥端口：`8092`

已验证：

- `http://127.0.0.1:5175` 返回 HTTP 200。
- `http://127.0.0.1:4512/metrics` 返回 HTTP 200。
- 浏览器页面 title 为 `Super Dolphin Agent`。
- 前端 console error/warning 计数为 0。

运行时说明：

- 默认用户目录触发 SQLite owner-only 权限检查，因此本次使用 owner-only 临时运行目录：
  `D:\tmp\super-dolphin-runtime-feature-integration-20260617`
- 当前监听进程：
  - Vite：端口 `5175`
  - `agent-terminal`：端口 `4512`、`8092`
