# A02: RuntimeMode Sentinel

**Goal:** 建立唯一 runtime 判定源，防止 DB、provider、preflight、sidecar 各自猜 packaged，同时保留 packaged fail-fast。

**Files:**
- Modify: `internal/platform/runtimeenv/**`
- Modify: `cmd/agent-terminal/main.go`
- Test: `internal/platform/runtimeenv/*_test.go`

**Boundary model:**
- `ProcessRole=owner|sidecar` 与 `RuntimeMode=dev|packaged` 必须分开表达。
- owner 进程可以解析 dev/packaged；sidecar 进程只能消费父进程传入的 mode/resource contract，不能自行 default dev 或 auto-detect packaged。
- explicit dev 只接受可信 dev entrypoint 注入：`run-debug.sh`、`run-debug.ps1`、`make run-agent-terminal-debug*`。普通 ambient env 不能把 packaged launcher/root 或有效 packaged sentinel 降级成 dev。
- packaged intent/root 与 packaged sentinel 是强边界：显式 packaged launcher/root 存在时，manifest 缺失、损坏或不归属于 package root 必须 fail-fast，不能降级 dev。
- debug `.app` 没有 packaged intent/root 且没有有效 bundle-bound manifest 时才 resolves to owner/dev。

**Resolution order:**
1. If `ProcessRole=sidecar`, require parent mode/resource env. Missing or invalid parent contract returns a parent launch contract error; no default dev fallback.
2. If owner has explicit packaged launcher/root, verify manifest and resources. Valid manifest resolves to `RuntimeMode=packaged`; missing/invalid manifest returns error.
3. If owner has trusted dev entrypoint marker and no packaged launcher/root, resolve to `RuntimeMode=dev` and ignore packaged-only residual env.
4. If owner has a valid packaged sentinel bound to the current macOS bundle or explicit Linux package root, resolve to `RuntimeMode=packaged`.
5. Otherwise resolve owner to `RuntimeMode=dev` without materializing packaged defaults.

**Steps:**
- [ ] Write red test: debug `.app/Contents/MacOS` with no packaged intent/root and no valid `Contents/Resources/runtime-manifest.json` resolves to `ProcessRole=owner`, `RuntimeMode=dev`.
- [ ] Write red test: explicit macOS packaged launcher/root with missing `Contents/Resources/runtime-manifest.json` fails fast instead of resolving to dev.
- [ ] Write red test: explicit packaged launcher/root with malformed or package-root-escaping manifest fails fast instead of resolving to dev.
- [ ] Write red test: `.app` with valid manifest bound to current bundle resolves to owner/packaged.
- [ ] Write red test: Linux package root explicitly passed by package launcher with valid manifest resolves to owner/packaged.
- [ ] Write red test: Linux explicit package root with missing or malformed manifest fails fast instead of resolving to dev.
- [ ] Write red test: repo-root `runtime-manifest.json` does not trigger packaged for a dev executable.
- [ ] Write red test: ambient runtime env cannot force `dev` when explicit packaged launcher/root or valid packaged sentinel is present.
- [ ] Write red test: sidecar role missing parent mode/resource env returns parent launch contract error and never defaults to dev.
- [ ] Write red test: sidecar role with parent mode/resource env consumes the inherited contract without packaged auto-detect.
- [ ] Implement a single resolver entrypoint that returns both `ProcessRole` and `RuntimeMode` plus package resources or an explicit error.
- [ ] Replace path-shape checks in directly touched runtime code with the resolver output.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
```
