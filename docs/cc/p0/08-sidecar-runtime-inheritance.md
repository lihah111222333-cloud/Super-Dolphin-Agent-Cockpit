# A08: Sidecar Runtime Inheritance

**Goal:** `mcp-orch`、`mcp-lsp`、`mcp-ida` 作为 sidecar 时只继承 owner 传入的 runtime mode 和资源路径，不自行 auto-detect packaged，也不 default dev。

**Files:**
- Modify: `cmd/mcp-orch/main.go`
- Modify: `cmd/mcp-lsp/main.go`
- Modify: `cmd/mcp-ida/main.go`
- Modify: sidecar spawn/env code in provider/runtime packages
- Test: `cmd/mcp-orch/*runtime*_test.go`
- Test: `cmd/mcp-lsp/*runtime*_test.go`
- Test: `cmd/mcp-ida/*runtime*_test.go`

**Boundary:**
- Sidecar process role is not a second runtime owner. It may inherit `RuntimeMode=dev|packaged`, but it cannot resolve that mode from its own executable path.
- Missing parent mode/resource env is a parent launch contract error and must fail fast. It must not fall through to owner/default dev resolution.
- Sidecar must not start owner-only embedded resources such as embedded PostgreSQL or packaged resource verification flows unless explicitly delegated by parent contract.

**Steps:**
- [ ] Write red test: `mcp-orch` binary under package `Resources/bin` without parent mode/resource env fails with parent contract error and does not resolve dev or packaged.
- [ ] Write red test: `mcp-lsp` binary under package `Resources/bin` without parent mode/resource env fails with parent contract error and does not resolve dev or packaged.
- [ ] Write red test: `mcp-ida` binary under package `Resources/bin` without parent mode/resource env fails with parent contract error and does not resolve dev or packaged.
- [ ] Write red test: packaged owner passes mode/resource env and each sidecar (`mcp-orch`, `mcp-lsp`, `mcp-ida`) consumes it without re-detecting.
- [ ] Write red test: dev owner passes dev mode to each sidecar and residual packaged env does not cause sidecar packaged auto-detect.
- [ ] Remove sidecar path-shape packaged detection.
- [ ] Fail-fast messages must say parent launch contract is missing, not ask users to configure bundle paths.

**Validation:**
```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch ./cmd/mcp-lsp ./cmd/mcp-ida -run 'Test.*(Runtime|Sidecar|Inheritance)' -count=1
```

If sidecar spawn/env code in provider/runtime packages is changed, add the exact affected package gate before marking A08 complete, for example:

```bash
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/platform/runtimeenv -count=1
```
