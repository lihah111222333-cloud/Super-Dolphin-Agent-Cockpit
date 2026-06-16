# Round 064 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:58:09 KST
- 结束：2026-05-17 09:00:46 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 skill expand、artifact approval、resource 读取和 host/RPC 审批桥接。重点看候选技能落盘后，首次读取、资源读取、跨项目审批缓存和 trusted 判定是否能被绕过。

- `internal/module/skill/skills_fs.go`
- `internal/module/skill/skills_expand.go`
- `internal/module/skill/service.go`
- `internal/module/skill/approval.go`
- `internal/module/skill/trust.go`
- `internal/module/skill/rpc.go`
- `internal/module/skill/approval_flow_test.go`
- `internal/module/skill/approval_test.go`
- `internal/module/skill/cwd_scope_test.go`
- `internal/platform/toolbridge/handler_host_tools.go`
- `internal/contract/approval.go`
- `internal/contract/errors.go`

## Findings

1. **[critical] project skill 可通过 `trust: user` 直接绕过 expand 审批**
   - 证据：`ensureExpandApproved()` 首行只要 `prepared.record.info.Trust.Trusted()` 就直接返回，不再请求审批（`internal/module/skill/service.go:345-347`）。frontmatter 的 `trust` 会被 `applyMetaTrust()` 写入 `info.Trust`（`internal/module/skill/skills_meta.go:315-323`），`parseTrustScope()` 接受 `user/trusted/signed/verified`（`internal/module/skill/trust.go:160-171`）。测试明确锁定了项目 root 下写入 `trust: user` 时 `expandWithApproval()` 不弹审批（`internal/module/skill/approval_flow_test.go:89-98`）。
   - 风险：仓库内 `.agent/skills` 本应是 `TrustProject`，但自动生成或恶意提交的 SKILL.md 可以自声明 trusted，随后 `skill/expand` 直接把完整内容或资源返回给上层模型。对量化反馈链路来说，候选生成内容一旦带上该 frontmatter，就绕过首次人工确认。
   - 建议：project root 下忽略高于 root 默认信任域的 frontmatter trust；`TrustSigned` 必须由签名验证结果产生，不能由文本声明产生。

2. **[major] `skill/expand` 仍使用 legacy name+hash 审批缓存，未使用 repo/artifact 维度**
   - 证据：`ApprovalCache` 已支持 `RepoFingerprint + Name + ArtifactKind + ArtifactLocator + ContentHash` 五元组（`internal/module/skill/approval.go:71-80`，`internal/module/skill/approval.go:159-179`），并有 repo 隔离测试（`internal/module/skill/approval_test.go:332-359`）。但 expand 缓存命中走 `s.approval.Lookup(name, contentHash)`，持久化走 `s.approval.Approve(name, contentHash, trust, approvedBy)`，都没有传 repo fingerprint、artifact kind 或 locator（`internal/module/skill/service.go:455-483`）。
   - 风险：一个项目批准过的同名同 hash 技能，会在另一个项目中命中全局 legacy 缓存；同时 section/resource 的 artifact-specific 审批记录不会被 `skill/expand` 使用。审批系统表面上支持细粒度隔离，实际执行入口仍按旧口径运行。
   - 建议：`expandWithApproval` 构造 `ApprovalRequest` 时使用 caller cwd 计算 repo fingerprint，并按 body/section/resource 设置 artifact kind 与 locator；废弃或迁移 legacy `Lookup/Approve`。

3. **[major] expand 审批弹窗使用 service 初始化的 `projectRoot`，不是本次请求的 cwd**
   - 证据：RPC handler 把请求 `cwd` 注入 context 后调用 expand（`internal/module/skill/rpc.go:291-301`），`prepareSkillExpand()` 也是从 context 取 cwd 扫描技能（`internal/module/skill/skills_fs.go:60-68`）。但 `buildSkillExpandApprovalRequest()` 的 payload 中 `project_root` 来自 `s.projectRoot`，不是请求 cwd（`internal/module/skill/service.go:415-424`）。仓库已有多 CWD 列表测试，说明同一 service 可按请求 cwd 切换项目技能根（`internal/module/skill/cwd_scope_test.go:12-41`）。
   - 风险：在多项目或 worktree 场景，用户看到的审批项目可能是 service 初始化项目，而实际读取的是另一个 cwd 的技能。审批上下文错位会导致误批，尤其是同名技能或共享 system skill 混用时。
   - 建议：把 cwd 纳入 `skillExpandPrepared` 或 `buildSkillExpandApprovalRequest` 入参；审批 payload 同时带 caller cwd、repo fingerprint、skill dir 和 artifact locator。

4. **[major] `SKILL.md` 本体读取不做 symlink containment，expand 可读取技能目录外的正文**
   - 证据：`expandSkillFile()` 和 `expandSkillSection()` 直接对 `record.path` 调用 `readSkillExpandBytes()`（`internal/module/skill/skills_fs.go:120-145`）。`readSkillExpandBytes()` 用 `os.Stat` 和 `os.ReadFile`，都会跟随 symlink（`internal/module/skill/skills_fs.go:177-188`）。相对资源读取的 `resolveResourceTarget()` 会 `EvalSymlinks` 并验证仍在 skill 目录内（`internal/module/skill/skills_expand.go:116-133`），但 `SKILL.md` 本体没有同等保护。
   - 风险：项目技能目录内的 `SKILL.md` symlink 可指向 repo 外文件，`skill/expand` 会把外部文件当技能正文读取并计算审批 hash。若该外部文件含 trusted frontmatter，还会叠加 Finding 1 的审批绕过。
   - 建议：扫描和 expand 读取 `SKILL.md` 时拒绝 symlink，或解析真实路径后要求仍位于 skill dir/root 内；补充 `SKILL.md` symlink 逃逸测试。

5. **[moderate] section/resource 每次都弹审批且不落 artifact cache，细粒度审批能力处于断层**
   - 证据：`prepareSkillExpand()` 只有完整技能 `section == ""` 时 `cacheable: true`，section/resource 都返回 `cacheable: false`（`internal/module/skill/skills_fs.go:73-93`）。`ensureExpandApproved()` 对非 cacheable 强制 `approvalScopeSession`，且审批通过后直接返回，不写缓存（`internal/module/skill/service.go:380-391`，`internal/module/skill/service.go:368-377`）。测试明确要求 section/resource 两次调用产生两次审批且 persistent cache 为 0（`internal/module/skill/approval_flow_test.go:149-180`）。
   - 风险：用户批准过某个 resource 后无法形成稳定记录；外部 `ApprovalSource` 和 turn 的 artifact fresh 状态无法与实际 expand 入口闭环。长期运行的量化反馈审查会产生重复审批噪声，也会让审计日志难以证明“哪个 artifact 被批准过”。
   - 建议：section/resource 也按 artifact key 缓存；若不想持久化，至少在 session scope 内按 artifact locator/hash 记账。

## 误报与已覆盖项

- resource 路径的 `../` 和 symlink 逃逸已有专门保护：locator 会拒绝上跳，真实路径也会要求仍在 skill dir 内（`internal/module/skill/trust.go:129-145`，`internal/module/skill/skills_expand.go:116-133`）。
- approval cache 底层已经支持 artifact kind/locator/repo fingerprint，问题在 expand 执行入口没有使用这些维度（`internal/module/skill/approval_test.go:293-411`）。
- RPC 层要求 `cwd`，缺 cwd 会被 `ErrMissingCWD` 拒绝，基础作用域入口不是空的（`internal/module/skill/rpc.go:291-301`，`internal/module/skill/cwd_scope_test.go:98-130`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/platform/toolbridge -count=1
```

结果：通过。

## 下一轮建议

- Round 065 审查 turn/prompt 技能注入链路：selected hydration、expanded artifact state、approval source 与 prompt 缓存，确认 expand 审批结果是否正确影响后续模型输入。
