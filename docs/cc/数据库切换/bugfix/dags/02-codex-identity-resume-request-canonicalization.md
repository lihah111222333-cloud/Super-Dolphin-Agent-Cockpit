# Task 02: CodexIdentity resume 请求 canonicalization

## Agent Prompt

你负责修复 MR !52 P2 的第一段：thread resume 请求在进入 provider 前必须把 Codex identity 收敛到 `contract.ResolveCodexIdentity()` 的 canonical realpath。不要修改 provider driver 的 pool identity 规则，不要修 binding 持久化收敛，那是 Task 03。

## Scope

依赖：无。

可并行：可与 `01-sqlite-open-failfast-diagnostics.md` 并行。

解锁：

- `03-codex-identity-thread-persistence-convergence.md`
- `04-codex-identity-binding-backfill-convergence.md`

建议 task worktree：`.worktrees/mr52-bugfix-task-02-codex-resume-request`

## 源码追溯

- `internal/contract/codex_identity.go` 要求 `CodexIdentity.Home` 是 canonical realpath。
- `internal/module/thread/start_session.go` 的 `hydrateResumeCodexIdentity()` 只取首个非空值，没有 canonicalize。
- `internal/module/thread/start_session.go` 的 `resolveResumeRequest()` 在调用 provider 前没有规范化 `req.CodexHome`。
- `internal/module/thread/start_session_helpers.go` 的 `hydrateResumeSessionRequest()` 也会构造 provider resume 请求，需要同样收敛。

## 修改点

- Create: `internal/module/thread/codex_identity_canonical.go`
  - 新增 thread 侧共享 helper，集中复用 contract 层。Task 03 和 Task 04 只能复用这里的 helper，不再扩展同一个文件，避免任务间抢同一文件。

```go
func canonicalizeCodexIdentityFields(provider, home, instanceKey, modelProvider string) (contract.CodexIdentity, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return contract.CodexIdentity{}, false, nil
	}
	home = strings.TrimSpace(home)
	instanceKey = strings.TrimSpace(instanceKey)
	modelProvider = strings.TrimSpace(modelProvider)
	if home == "" && instanceKey == "" && modelProvider == "" {
		return contract.CodexIdentity{}, false, nil
	}
	identity, err := contract.ResolveCodexIdentity(map[string]any{
		contract.CodexHomeKey:          home,
		contract.CodexInstanceKeyKey:   instanceKey,
		contract.CodexModelProviderKey: modelProvider,
	})
	if err != nil {
		return contract.CodexIdentity{}, false, err
	}
	return identity, true, nil
}

func canonicalizeResumeCodexIdentity(req ResumeRequest) (ResumeRequest, error) {
	identity, ok, err := canonicalizeCodexIdentityFields(req.Provider, req.CodexHome, req.CodexInstanceKey, req.CodexModelProvider)
	if err != nil {
		return ResumeRequest{}, err
	}
	if !ok {
		return req, nil
	}
	req.CodexHome = identity.Home
	req.CodexInstanceKey = identity.InstanceKey
	req.CodexModelProvider = identity.ModelProvider
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.Config[contract.CodexHomeKey] = identity.Home
	req.Config[contract.CodexInstanceKeyKey] = identity.InstanceKey
	req.Config[contract.CodexModelProviderKey] = identity.ModelProvider
	return req, nil
}
```

- Modify: `internal/module/thread/start_session.go`
  - 在 `resolveResumeRequest()` 中，`injectDefaultCodexIdentityForResume()` 和 `mergeRuntimeConfig()` 之后调用 `canonicalizeResumeCodexIdentity(req)`。
  - 确保 `resumeResolvedSession()` 传给 provider 的 `dto.ResumeSessionRequest.CodexHome` 已是 canonical。

- Modify: `internal/module/thread/start_session_helpers.go`
  - 在 `hydrateResumeSessionRequest()` 同一阶段调用同一个 helper，避免两个 resume 入口漂移。

- Test: `internal/module/thread/resume_test.go` 或现有 codex identity resume 测试文件
  - 新增 symlink alias 测试：binding/runtime/request 提供 symlink `codexHome`，starter 捕获到的 request 必须是 realpath。
  - Windows 下 `os.Symlink` 不可用时 skip，skip 原因写明权限限制。
  - 新增部分 identity 测试：有 `CodexHome` 但缺 instance key/model provider 时必须返回 contract sentinel error，不允许默认补成错误组合。

## 不允许改

- 不要修改 `internal/provider/codexapp/**` 的 provider canonicalization。
- 不要修改 `bindingNeedsCodexIdentityUpdate()`，该收敛在 Task 03。
- 不要修改 `internal/module/thread/lifecycle.go` 的持久化选择逻辑，该收敛在 Task 03。
- 不要 fallback 到 `CanonicalAppManagedCodexHome()` 覆盖显式 invalid home。
- 不要在 history/message read 热路径做 `EvalSymlinks`。

## 性能要求

- 每次 resume 请求最多 canonicalize 一次完整 identity。
- 不能对每条消息、每个历史文件候选或每个 binding 列表项做 realpath。

## 验收方案

1. 每个 Go 文件修改后运行单文件 guard：

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/thread/codex_identity_canonical.go
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/thread/start_session.go
pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/thread/start_session_helpers.go
```

2. focused tests：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/thread -run 'CodexIdentity|Resume' -count=1
```

3. 验收标准：

- provider starter 捕获到的 `CodexHome` 是 realpath。
- `req.Config["codexHome"]` 同步为 realpath。
- 部分 identity fail-fast。
- 非 codex provider 行为不变。

## Review Checklist

- 生产就绪性：显式 invalid identity 不被默认值掩盖。
- 性能：canonicalization 不进入热路径。
- 风险：两个 resume 入口都调用同一 helper。
- 可维护性：只依赖 `contract.ResolveCodexIdentity()`。
- 测试充分性：symlink alias、partial identity、非 codex 都覆盖。
