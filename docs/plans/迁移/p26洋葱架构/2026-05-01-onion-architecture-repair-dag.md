# Onion Architecture Dependency Repair Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 完全消除 `internal/platform` 和 `internal/module` 层对 `mcpserver` 与 `provider` 层的倒置依赖，修复服务发现的路径，并添加严格的架构断言。

**Architecture:** 依据洋葱架构与 V3 契约，遵循依赖倒置原则。将原本处于 `mcpserver/common` 的服务发现机制下沉至纯粹的 `internal/platform/discovery`。将 `toolbridge` 内部对 `provider/codexapp` 的硬编码调用抽象为中立的协议层。

**Tech Stack:** Go 1.22+, `go list` 依赖分析, `go test` 架构守护测试。

---

### Task 1: 验证与修复 Discovery 下沉的编译与测试

在早期的重构中，我们已将 `mcpserver/common/discovery.go` 移动到了 `internal/platform/discovery/discovery.go`，并通过 `sed` 替换了全局引用。此任务负责修复可能遗留的编译错误，确保系统可以正常构建。

**Files:**
- Modify: `internal/platform/discovery/discovery.go` (如果需要)
- Modify: `cmd/mcp-lsp/fx.go` (等涉及引用替换的组装点)

- [x] **Step 1: 运行构建以确认当前失败点**
```bash
go build ./cmd/mcp-lsp/... ./cmd/mcp-orch/... ./cmd/mcp-ida/...
```
Expected: FAIL 伴随一些包未找到或命名空间未解决的错误（例如某些 `common` 未被正确替换）。

- [x] **Step 2: 修复遗留的命名空间问题**
修复所有受影响包中的 `common.Xxx` 调用为 `discovery.Xxx`，并确保 `import` 指向正确的 `github.com/anthropic-ai/super-agent-v3/internal/platform/discovery`。

- [x] **Step 3: 运行测试以验证修复**
```bash
go test ./internal/platform/discovery/... ./internal/module/turn/... ./internal/mcpserver/...
```
Expected: PASS

- [x] **Step 4: Commit**
```bash
git add -A
git commit -m "refactor: sink discovery logic to platform layer and fix imports"
```

---

### Task 2: 添加严格的架构守护测试 (TDD)

在修复 `toolbridge` 之前，先编写必定失败的架构断言测试，以暴露现存的违规点。

**Files:**
- Modify: `internal/archtest/dependency_direction_test.go`

- [x] **Step 1: 编写失败的架构测试**
修改 `dependency_direction_test.go`，在 `assertPlatformIsolationRules` 和 `assertCoreDependencyRules` 中添加新的规则：
```go
	t.Run("rule16_platform_no_mcpserver_or_provider", func(t *testing.T) {
		if !dirExists(root, "internal/platform") {
			t.Skip("directory not yet created")
		}
		forbidden := []string{
			internalPrefix("internal/mcpserver/"),
			internalPrefix("internal/provider/"),
		}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/platform"), forbidden)
	})

	t.Run("rule17_module_no_mcpserver", func(t *testing.T) {
		if !dirExists(root, "internal/module") {
			t.Skip("directory not yet created")
		}
		forbidden := []string{internalPrefix("internal/mcpserver/")}
		assertNoImportPrefixes(t, parseImportFiles(t, root, "internal/module"), forbidden)
	})
```

- [x] **Step 2: 运行测试以确认失败**
```bash
go test ./internal/archtest -run 'TestDependencyDirection/rule16|rule17' -v -count=1
```
Expected: FAIL 伴随 `toolbridge imports mcpserver` 和 `toolbridge imports provider` 等错误。

- [x] **Step 3: Commit**
```bash
git add internal/archtest/dependency_direction_test.go
git commit -m "test(arch): add failing dependency direction guards for platform and module"
```

---

### Task 3: 解耦 Toolbridge 与 CodexApp 协议

移除 `internal/platform/toolbridge` 对 `internal/provider/codexapp/protocol` 及 `mcpserver/common` 的依赖，将通用常量下沉或接口化。

**Files:**
- Create: `internal/contract/mcp/proxy_protocol.go`
- Modify: `internal/platform/toolbridge/handler_peer_decode.go`
- Modify: `internal/platform/toolbridge/handler_host_tools.go`
- Modify: `internal/platform/toolbridge/module.go`
- Modify: `internal/provider/codexapp/protocol/xxx.go`

- [x] **Step 1: 提取常量至契约层**
在 `internal/contract/mcp/proxy_protocol.go` 中定义被 toolbridge 依赖的常量或接口，替换原有的 `codexprotocol`。
```go
package mcp

const (
    // 移动原本在 codexapp/protocol 或 mcpserver/common 中被 toolbridge 复用的常量
)
```

- [x] **Step 2: 修复 Toolbridge 中的引用**
在 `toolbridge` 包内，删除所有指向 `internal/provider` 和 `internal/mcpserver` 的 `import`。改用新创建的 `mcp` 契约或泛型参数。

- [x] **Step 3: 运行架构测试以验证解耦成功**
```bash
go test ./internal/archtest -run 'TestDependencyDirection/rule16|rule17' -v -count=1
```
Expected: PASS

- [x] **Step 4: Commit**
```bash
git add internal/platform/toolbridge internal/contract/mcp internal/provider/codexapp
git commit -m "refactor: decouple toolbridge from specific providers and mcpserver"
```

---

### Task 4: 全局回归验证与原子推送

进行全量测试以确保没有附带破坏，然后执行推送。

- [x] **Step 1: 运行全量单元与架构测试**
```bash
go test ./... -v -short
```
Expected: PASS

- [x] **Step 2: 进行全量原子提交与推送**
```bash
git add -A
git commit -m "chore: complete P26 onion architecture dependency inversion repair"
git push
```
