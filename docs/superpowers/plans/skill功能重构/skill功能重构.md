# Skill 功能重构计划

> 日期: 2026-05-16
> 状态: Draft，2026-05-18 按 V1 实际落地方向纠偏：Super-Dolphin 不打包 Claude/Codex CLI，默认使用用户自己安装并登录的 CLI；本项目只管理 canonical skill 并同步到 provider-native skill 目录。
> 范围: 只设计 V1-V3；V4 作为 Non-goal / Future

## 1. 总目标

Super-Dolphin 不再承担 Claude/Codex 的 skill 注入职责。项目只负责管理 canonical skill、发布 provider-native mirror，并让用户自己安装和登录的 Claude CLI / Codex CLI 按各自原生机制发现 skill。

最终边界：

- 不再给 Claude/Codex prompt 注入 Super-Dolphin skill manifest。
- 不再暴露 `skill_read_section` 这类 host-direct skill 读取工具给 Codex 作为运行时依赖。
- 不再启动时把 `.claude/skills` symlink 到 `~/.super-dolphin/skills-cache`。
- Super-Dolphin 管 canonical；provider-native 目录只是 mirror/output。
- Claude/Codex 是否调用 skill，由 provider 原生发现和调用机制决定。
- Super-Dolphin 的自进化是旁路维护 canonical，不是让 provider mirror 自己成为事实源。

## 2. 设计原则

1. Canonical 只有 Super-Dolphin 管理，mirror 只是生成物。
2. 项目级 skill 进入 git 和团队 review；个人级 skill 进入用户自己的 Super-Dolphin home。
3. 默认使用 provider 官方个人 skill 目录：Claude 写 `~/.claude/skills`，Codex 写 `~/.agents/skills`；Codex CLI 身份仍使用用户自己的 `~/.codex`。只有用户显式配置 provider home 时，才写该 home 下的 `skills`。
4. 没有 Super-Dolphin ownership marker 的 provider-native 目录，一律视为 unmanaged，不覆盖、不删除。
5. 同名冲突默认严格处理，不静默 shadow。
6. 写入、删除、迁移、drift 处理都要可解释、可回滚、可审计。
7. 自进化第一阶段只生成 proposal；自动应用只允许逐步放开到个人级低风险 patch。

## 3. 最终目录模型

### 3.1 项目级 canonical

项目级 skill 是团队资产，默认进入 git：

```text
<repo>/.agent/skills/<skill-name>/SKILL.md
<repo>/.agent/skills/<skill-name>/references/...
<repo>/.agent/skills/<skill-name>/templates/...
<repo>/.agent/skills/<skill-name>/scripts/...
```

项目级 canonical 删除，表示项目不再拥有该 skill。下次 publish 只会删除 Super-Dolphin owned 且 hash 仍匹配/未 drift 的 mirror；如果 mirror 已 drift，则进入 `canonical_deleted_with_drift` 冲突并在技能管理页提示用户处理，但不阻断 provider 正常启动。不会删除 unmanaged provider-native skill。

### 3.2 项目级 provider mirror

项目级 mirror 是生成物，不提交：

```text
<repo>/.claude/skills/<skill-name>/SKILL.md
<repo>/.agents/skills/<skill-name>/SKILL.md
```

要求：

- `.claude/` 已忽略。
- `.agents/` 需要加入 `.gitignore`。
- mirror 目录必须带 Super-Dolphin ownership marker 或 manifest。
- mirror 可以先允许编辑，但项目级 drift 必须进入冲突处理，不自动写回 canonical。

### 3.3 个人级 canonical

个人级 skill 用 Super-Dolphin 品牌目录，并按来源类型分层：

```text
~/.super-dolphin/skills/personal/user/<skill-name>/SKILL.md
~/.super-dolphin/skills/personal/agent/<skill-name>/SKILL.md
~/.super-dolphin/skills/personal/imported/<skill-name>/SKILL.md
```

类型语义：

- `user`: 用户手写或用户明确创建的个人 skill。
- `agent`: Super-Dolphin 后台维护模型创建或自动维护的个人 skill。
- `imported`: 从外部目录导入，例如系统 `~/.claude/skills`、`~/.agents/skills` 或共享 skill repo。
- `hub`: 未来 marketplace / registry 的目录来源，不是生效 personal canonical；安装后应写入项目共享或 `personal/imported`，不能直接参与扫描、mirror 或 provider 调用。

`user` 是稳定 wire enum，语义是 human-authored / user-created。UI 可以展示为“用户创建”或“人工创建”，但 V1-V3 不再另起 `human` 目录或 enum，避免 schema、manifest、audit、proposal 再次分裂。

个人级可以随意改，含义是“不需要团队 review”。它仍然要受路径、ownership、backup、audit、rollback 约束。

### 3.4 个人级 provider mirror

默认写入用户自己的 provider-native 个人 skill 目录：

```text
~/.claude/skills/<skill-name>/SKILL.md
~/.agents/skills/<skill-name>/SKILL.md
```

默认不把 `~/.codex` 当作 skill mirror；它只保留为 Codex CLI 的身份和配置目录。

显式 provider home 仍保留高级用法：用户配置后才写 `<provider-home>/skills`，并由 provider 启动路径使用该 home。

## 4. Effective Skill Set

启动 Claude/Codex 前，Super-Dolphin 计算 effective skill set：

```text
project canonical + personal canonical + policy -> provider-native mirror
```

默认优先级不做静默覆盖。出现同名 skill 时进入冲突：

- 项目级和个人级同名：提示用户选择项目覆盖个人、禁用个人、重命名个人。
- personal 不同类型同名：提示用户选择保留哪一个、合并、重命名。
- canonical 和 unmanaged provider-native 同名：提示查看原因、import、takeover；只查看或确认不是 resolution，冲突继续留在技能管理页，直到用户 import、takeover、删除/重命名 unmanaged 内容或改 canonical 名称；不阻断 provider 正常启动。

不建议 silent shadow，因为用户会误以为某个 skill 生效，但 provider 实际读到的是另一个版本。

## 5. Ownership 与 Manifest

每个 Super-Dolphin managed canonical 和 mirror 都需要可验证 ownership/provenance。

建议每个 mirror root 带 manifest：

```text
.super-dolphin-skill-mirror.json
```

建议字段：

```json
{
  "version": 1,
  "manager": "super-dolphin",
  "scope": "project",
  "provider": "claude",
  "canonical_root_id": "repo:fingerprint-or-owner-key",
  "generated_at": "2026-05-16T00:00:00Z",
  "skills": {
    "example-skill": {
      "canonical_id": "project/example-skill",
      "canonical_hash": "sha256:...",
      "mirror_hash": "sha256:...",
      "source_type": "project",
      "personal_type": "",
      "owned": true
    }
  }
}
```

Project entries use empty or omitted `personal_type`; active personal entries must store the exact type (`user`, `agent`, or `imported`) and must not infer it from `canonical_id`. Project mirror manifests may use a repo fingerprint in `canonical_root_id`; personal mirror manifests use the derived `owner_key` and home-relative canonical ids such as `personal/user/name`, never raw home/profile/uid paths. `personal/hub` is catalog-only and must not appear in runtime provider mirror manifests.

规则：

- manifest 中存在且 hash 匹配，mirror 可覆盖。
- manifest 中存在但 mirror hash 不匹配，进入 drift。
- mirror 中存在但 manifest 不存在，视为 unmanaged，不覆盖、不删除。
- manifest 指向的 canonical 不存在，按 canonical 删除流程处理。

## 6. 写入规则

### 6.1 项目级写入

项目级写入只写 `.agent/skills`。

允许入口：

- UI 新建/编辑项目 skill。
- 用户确认后的 proposal。
- 导入后用户明确选择“作为项目 skill 管理”。

禁止入口：

- 后台模型直接自动改项目级 canonical。
- provider mirror 自动写回项目级 canonical。
- 启动/发布流程把 unmanaged provider skill 写入项目级 canonical。

### 6.2 个人级写入

个人级写入只写 `~/.super-dolphin/skills/personal/...`。

允许入口：

- 用户直接创建/编辑。
- 用户确认后的 skill proposal。
- V3 中低风险 patch 自动应用到 `personal/agent`。
- 用户显式 import/takeover 外部 skill。

个人级 mirror 被编辑后，可以同步回 personal canonical；如果 canonical 和多个 mirror 同时变化，进入冲突，不猜测合并方向。

### 6.3 Provider mirror 写入

publish 只写 Super-Dolphin owned mirror。

项目级 mirror drift 时，用户选择：

- 同步回 canonical。
- canonical 覆盖 mirror。
- 另存为新 skill。

个人级 owned mirror drift 时，用户选择：

- mirror 同步回 personal canonical。
- personal canonical 覆盖 mirror。
- 另存为新 personal skill。

## 7. 删除规则

### 7.1 项目级 canonical 删除

删除 `.agent/skills/<name>` 表示项目不再拥有该 skill。

publish 行为：

- 删除 managed mirror 中对应 skill。
- 如果 mirror 有 drift，先进入冲突。
- 不删除 unmanaged provider-native 同名目录。

### 7.2 项目级 mirror 删除

canonical 仍存在时，mirror 删除不代表 canonical 删除。

下一次 reconcile：

- 如果 manifest 记录该 mirror owned，重新生成。
- 如果用户想删除 canonical，必须从 `.agent/skills` 或 UI 删除。

### 7.3 个人级 canonical 删除

个人级 canonical 删除默认进入 archive，不直接永久删除：

```text
~/.super-dolphin/skills/.archive/<timestamp>/<scope>/<type>/<skill-name>/
```

publish 行为：

- 删除 owned provider mirror。
- 写 audit log。
- 支持 restore。

### 7.4 个人级 mirror 删除

个人级 mirror 删除不直接推断为 canonical 删除。

下一次 reconcile 提示：

- 重新生成 mirror。
- 同步回 personal canonical。
- personal canonical 覆盖 mirror。
- 另存为新 personal skill。
- 删除 personal canonical 并归档。

V1 不允许把单个 provider mirror 从发布集中移除作为 drift 解决方案。用户不处理 drift 时，冲突继续显示在技能管理页，直到用户选择同步、覆盖、另存、删除 canonical，或手动恢复 mirror；普通 drift 不阻断 provider 正常启动。

## 8. Drift 与冲突处理

Drift 检测基于 canonical hash、mirror hash、manifest hash。

项目级 drift 策略：

- 默认不自动合并。
- UI/RPC 展示三选一：同步回 canonical、canonical 覆盖 mirror、另存为新 skill。
- 用户未选择前，不覆盖 mirror，不更新 canonical。

个人级 drift 策略：

- 如果只有一个 owned mirror drift，允许同步回 personal canonical。
- 如果 canonical 和多个 mirror 同时 drift，进入冲突。
- `personal/agent` 自动维护前必须确认没有未解决 drift。

同名冲突策略：

- 不静默 shadow。
- 不自动改名。
- 提示用户做显式选择。

## 9. 外部目录策略

V1 external import/takeover can read from several external directory classes, but V1 export is intentionally narrower.

外部目录包括：

```text
~/.claude/skills
~/.agents/skills
shared team skill repositories
third-party skill directories
```

默认行为：

- 只读扫描。
- 可 import 到 `personal/imported` 或项目 `.agent/skills`。
- V1 只允许显式 export 到 canonicalized provider roots `~/.claude/skills` 和 `~/.agents/skills`。
- shared team skill repositories 和 third-party skill directories 在 V1 可 import/takeover；export 到这些目录需要未来显式 external-root allowlist。
- 可 takeover，但 takeover 前必须显示将被管理的路径、hash、备份位置。

禁止行为：

- 自动覆盖外部目录。
- 自动删除外部目录。
- 因为同名就自动迁移到 canonical。

## 10. 自进化定义

Super-Dolphin 的自进化是 skill maintenance，不是 provider-native runtime 自己改 skill。

实际链路：

```text
用户任务完成
  -> Super-Dolphin 收集 transcript/events/errors/user corrections/git diff/skill hashes
  -> 内部规则判断是否需要 review
  -> 后台模型读取压缩后的证据和相关 canonical skills
  -> 输出结构化 proposal
  -> Super-Dolphin validator 校验
  -> 用户确认或策略自动应用
  -> 写 canonical
  -> publish provider mirror
```

第一版不用 MCP 工具实现。后台模型只输出结构化 JSON proposal；读写文件由 Super-Dolphin 内部 service 执行。

原因：

- 模型不能直接乱写 `.agent/skills`、`.claude/skills`、`.agents/skills`。
- 项目级 review 更清楚。
- backup/audit/rollback 更容易做。
- 不回到旧的 skill 注入和 host-direct tool 模式。

## 11. V1: Provider-Native Publish

### 11.1 目标

把旧 skill 注入链路切掉，建立 canonical -> provider-native mirror 的基础闭环。

### 11.2 范围

V1 包含：

- 保留项目级 canonical：`<repo>/.agent/skills`。
- 新增生效个人级 canonical：`~/.super-dolphin/skills/personal/{user,agent,imported}`。
- `personal/hub` 仅作为未来市场/目录来源，V1 不扫描、不镜像、不作为写入目标。
- 新增 provider-native personal mirror。
- 项目级 mirror：`<repo>/.claude/skills`、`<repo>/.agents/skills`。
- 删除 Codex skill manifest injection。
- 删除 Codex `skill_read_section` 运行时依赖。
- 停止 Claude `.claude/skills -> ~/.super-dolphin/skills-cache` symlink 注入。
- 添加 `.agents/` gitignore。
- ownership manifest。
- startup/open project reconcile。
- write-time publish。
- 基础 drift/conflict 检测。
- 基础 import/export/takeover UX 和 policy。

V1 不包含：

- 自进化。
- 后台模型 proposal。
- curator。
- marketplace/hub 安装。
- MCP 管理接口。

### 11.3 主要组件

建议内部组件：

- `SkillCanonicalStore`: 读写项目级和个人级 canonical。
- `SkillMirrorPublisher`: 根据 canonical 生成 provider-native mirror。
- `SkillMirrorManifest`: 记录 ownership、hash、source、provider。
- `SkillReconciler`: 启动和写入后的对账。
- `SkillConflictDetector`: 同名、drift、unmanaged 目录检测。
- `SkillMigration`: 旧 `.claude/skills` symlink、旧 cache/library 的迁移处理。

### 11.4 旧链路处理

需要移除或替换的旧行为：

- README 中旧 `~/.super-dolphin/skills-library`、`skills-cache`、`.claude/skills` symlink、Codex `skill_read_section` 描述。
- `internal/provider/codexapp/driver.go` 中 `RenderSkillManifest()` prepend。
- `internal/platform/toolbridge` 中 `skill_read_section` host tool 对 Codex skill 的必要性。
- `internal/provider/claudecli/driver.go` 中 startup-time `setupWorkspaceSkills` symlink。
- `internal/module/cliadapter/symlink.go` 的旧 cache symlink 策略。

旧 `.claude/skills` 迁移：

- 如果检测到 symlink 且目标精确等于旧 `~/.super-dolphin/skills-cache`，启动/发布必须 fail-closed，并通过 V1 resolution UI/RPC 显示 takeover/replace 操作；不能在启动路径自动替换。
- 用户确认后才按 `preview hash -> backup -> audit intent -> mutate -> audit finalize` 执行替换，并生成新的 owned mirror manifest。
- 如果不是这个精确旧 symlink，同样报冲突，不自动替换。

### 11.5 V1 验收

V1 完成时应满足：

- Codex 可发现 `<repo>/.agents/skills/<name>/SKILL.md`。
- Claude 可发现 `<repo>/.claude/skills/<name>/SKILL.md`。
- Super-Dolphin 不再在 Codex prompt 前追加 skill manifest。
- Codex 不再需要 `skill_read_section` 才能读取 Super-Dolphin skill。
- Claude 不再通过旧 cache symlink 获取 skill。
- `.agent/skills` 是项目级唯一 canonical。
- provider mirror 带 manifest，且不提交。
- 删除 canonical 会删除 owned mirror；删除 mirror 会重建。
- unmanaged provider skill 不会被覆盖或删除。
- drift 有明确冲突状态和用户选择。
- Skills 页面必须能把项目 `.agent/skills` 与生效个人 skill 的同名冲突作为可解决项展示，不能因为 `skill same-name conflict: ...` 这类冲突让 dashboard RPC 整页失败；未解决冲突也不能被静默发布到 provider mirror。`personal/hub` 是目录/市场来源，重复时应被忽略或清理，不进入冲突处理。

2026-05-17 手工 smoke 记录：桌面 debug 会话中创建项目 skill `sd_project_smoke` 后，canonical 成功写入 `.agent/skills`，Claude 与当时的 Codex 项目 mirror 都成功写入，两个 provider 都返回探针标记 `SD_PROJECT_SKILL_OK`。2026-05-18 纠偏后，Codex 官方项目 mirror 应改为 `.agents/skills`；后续验收以 `.agents/skills` 为准，旧 Codex mirror 记录只作为历史 smoke。

## 12. V2: Skill Proposal

### 12.1 目标

实现“能建议，不自动改”。Super-Dolphin 可以在会话后生成 skill 变更 proposal，但所有 scope 默认都需要用户确认。

### 12.2 范围

V2 包含：

- 会话证据 recorder。
- 相关 canonical skill 检索和 context packing。
- 后台辅助模型调用。
- 结构化 proposal schema。
- proposal validator。
- proposal diff UI/RPC 展示。
- 用户确认后写 personal canonical 或 project canonical。
- 写入前 backup。
- audit log。
- apply 后 publish mirror。

V2 不包含：

- 自动应用。
- 周期 curator。
- MCP 工具。
- 项目级自动写入。

### 12.3 触发信号

确定性触发信号：

- 用户明确说“记住”“以后这样”“写成 skill”。
- 工具调用数超过阈值。
- 多次失败后成功。
- 用户纠正流程、风格、命令、边界。
- 本轮对 skill canonical/mirror 有操作。
- provider mirror drift 被解决。

这些信号只决定是否 review，不决定具体写什么。

### 12.4 后台模型输入

模型输入由 Super-Dolphin 内部组装：

- 压缩后的 transcript。
- 用户明确纠正和最终决策摘要。
- 相关 event：错误、重试、验证命令、文件变更摘要。
- 相关 canonical skill 摘要。
- 最多少量相关 skill 全文。
- 当前 policy：项目级只 proposal，个人级需确认。

### 12.5 Proposal Schema

建议结构：

```json
{
  "version": 1,
  "scope": "personal",
  "personal_type": "agent",
  "action": "patch_skill",
  "target": "skill-name",
  "confidence": "high",
  "reason": "用户明确纠正了该类任务的执行流程。",
  "changes": [
    {
      "file": "SKILL.md",
      "operation": "replace",
      "old": "旧文本片段",
      "new": "新文本片段"
    }
  ],
  "safety": {
    "requires_user_confirmation": true,
    "touches_project_scope": false,
    "touches_provider_mirror": false
  }
}
```

允许 action：

- `create_skill`
- `patch_skill`
- `write_support_file`
- `archive_skill`
- `rename_skill`

禁止 action：

- 直接写 provider mirror。
- 删除 unmanaged 外部目录。
- 修改项目级 canonical 且绕过用户确认。

### 12.6 V2 验收

V2 完成时应满足：

- 明确触发后能生成结构化 proposal。
- V2 release acceptance 必须配置 proposal model 并跑真实触发生成 smoke；未配置模型只算显式关闭/环境故障负向路径，不算 V2 完成态。
- proposal 不能越权写 mirror。
- 项目级 proposal 只展示 diff，确认后才写 `.agent/skills`。
- 个人级 proposal 确认后写 personal canonical。
- apply 前有 backup，apply 后有 audit。
- apply 后自动刷新 provider mirror。
- proposal 无法通过 validator 时不会落盘。

## 13. V3: Personal Auto-Maintenance

### 13.1 目标

个人级进入可控自动维护。项目级仍保持 proposal/review，不自动应用。

### 13.2 范围

V3 包含：

- 个人级低风险 patch 自动应用。
- pin / unpin。
- archive / restore。
- personal usage metadata。
- curator dry-run。
- curator real-run。
- skill 合并、去重、过期归档 proposal。
- 只对 `personal/agent` 自动应用。
- 对 `personal/user`、`personal/imported` 默认只建议；`personal/hub` 不属于生效 personal canonical，不进入自动维护。

V3 不包含：

- 项目级自动修改。
- 直接自动修改 provider mirror 后再回写 canonical。
- 自动 takeover 系统 `~/.claude/skills`、`~/.agents/skills`。
- MCP 管理接口。

### 13.3 自动应用条件

V3 自动应用只允许满足全部条件的 personal patch：

- scope 是 `personal`。
- type 是 `agent`。
- action 是 `patch_skill` 或 `write_support_file`。
- 不涉及 rename/delete。
- 不涉及 scripts 可执行文件。高风险自动化开关不属于 V3 默认范围，除非后续被明确批准为 future work。
- canonical 无 drift。
- provider mirror 无未解决冲突。
- validator 能精确匹配 old/new。
- backup 成功。
- policy 允许 auto apply。

不满足条件时降级为 proposal。

### 13.4 Usage Metadata

个人级维护 metadata 不写入 `SKILL.md`，使用 sidecar：

```text
~/.super-dolphin/skills/.usage.json
```

建议字段：

```json
{
  "skill-name": {
    "scope": "personal",
    "type": "agent",
    "created_by": "super-dolphin-agent",
    "use_count": 0,
    "proposal_count": 0,
    "patch_count": 0,
    "last_used_at": null,
    "last_patched_at": null,
    "state": "active",
    "pinned": false,
    "archived_at": null
  }
}
```

注意：在 provider-native 路线下，Super-Dolphin 不一定知道 Claude/Codex 是否真的调用了某个 skill。`use_count` 只能来自可观测事件，例如显式选择、UI 操作、provider 事件里暴露的 skill 使用痕迹；不可观测时不能伪造。

### 13.5 Curator 策略

Curator 只处理 `personal/agent`。

默认行为：

- dry-run 先展示会做什么，并持久化可验证 dry-run record；不改 canonical/mirror。
- real-run 前 snapshot。
- 自动归档只 move 到 archive，不永久删除。
- pinned skill 不自动归档、不自动合并。
- 合并窄 skill 时，优先形成 class-level umbrella skill。
- session-specific 细节写入 `references/`，模板写入 `templates/`，可复用脚本写入 `scripts/`。

### 13.6 V3 验收

V3 完成时应满足：

- 用户可以开启/关闭 personal auto-maintenance。
- `personal/agent` 低风险 patch 可自动应用。
- `personal/user` 默认不被自动改。
- 项目级永远不自动改。
- curator dry-run 不改 canonical/mirror，但会落服务端 dry-run record 供 real-run 验证。
- curator real-run 前有 snapshot。
- archive 可 restore。
- pin 后不被自动归档或合并。
- 所有自动写入都有 audit。
- 自动写入后刷新 mirror。

## 14. V4: Non-goal / Future

以下能力不进入 V1-V3，只作为未来方向：

- MCP/API control plane，让外部 agent 管理 Super-Dolphin skills。
- skill marketplace / registry 的完整搜索、安装、升级、卸载。
- 签名校验、trust policy、第三方 skill 安全扫描。
- 项目级自动生成 PR。
- 多机器同步。
- 团队级 central skill server。
- 更复杂的 provider adapter matrix。
- provider-native skill 调用事件的跨 provider 标准化。

## 15. 验证策略

V1 验证：

- 本地构造 `.agent/skills/probe/SKILL.md`，publish 后 Claude/Codex mirror 均存在。
- 用 Codex native debug 能看到 `.agents/skills/probe`。
- 用 Claude CLI 能看到 `.claude/skills/probe`。
- release 验收必须跑 Super-Dolphin driver-level provider-native smoke；只有文件级 mirror 存在不能证明 provider-native discover 成功。Claude/Codex CLI 由用户自行安装和登录，验收应检查未安装/未登录时给出清晰错误。
- 删除 canonical 后仅 hash 匹配/未 drift 的 owned mirror 删除；已 drift mirror 进入 `canonical_deleted_with_drift` 并在技能管理页提示用户处理。
- 删除 mirror 后 canonical 存在则重建。
- unmanaged mirror 不被覆盖。
- `.agents/` 不进入 git。
- Codex prompt 中不再出现 Super-Dolphin skill manifest prepend。
- `skill_read_section` 不再是 Codex skill 读取必需链路。
- `rg "turnSkillPrompt|SkillPrompt|SkillReplacementAggregator|ReplacedNativeTools|skilllibrary|skillforge|fbsd" internal cmd sql` 只允许命中删除态测试、迁移/历史文档、显式兼容拒绝测试，不能命中生产注入链路。
- 旧 `~/.super-dolphin/skills` 被一次性迁移/导入到 `personal/user` 后，不再作为 live runtime root 扫描。
- 聊天页不再保留启动 skill picker；create/edit/import/delete、summary、resolution 和 provider-start payload 不再发送或展示 live `system` scope。
- old `skills/candidate/*` 入口不再作为生产 skill pipeline 存活；V2 用 `skill_proposals/*` 替代。

V2 验证：

- 明确“记住这个流程”的会话能生成 proposal。
- 配置 auxiliary proposal model 后，明确“记住这个流程”的会话能生成 validated pending proposal。
- auxiliary proposal model 未配置但 evidence capture 开启时，只落 durable redacted evidence，不生成 pending proposal；这是负向/关闭场景验证，不作为 V2 release acceptance。
- 无相关信息的简单会话不生成 proposal。
- 项目级 proposal 未确认不写 `.agent/skills`。
- 个人级 proposal 确认后写 personal canonical。
- validator 拒绝路径穿越、provider mirror 写入、unmanaged 删除。
- backup/audit 文件存在。

V3 验证：

- `personal/agent` 低风险 patch 自动应用。
- `personal/user` 同类 patch 只生成 proposal。
- pinned skill 不被 curator 改。
- dry-run 不产生 canonical diff，但会保存 dry-run record/action hash，且 action hash 覆盖 canonical hash 和 usage record hash/version。
- real-run 在用户 pin/use/archive 状态变更后拒绝执行，即使 canonical bytes 未变。
- real-run 产生 snapshot 和 audit。
- archive 后可 restore。

## 16. 实施顺序

推荐按以下顺序实施：

1. V1A: 建 canonical store、personal type、effective set、manifest、ownership、hash、mirror publisher。
2. V1B: 接入现有 create/edit/import/delete/read/turn hydration 路径，迁移旧 `~/.super-dolphin/skills`，移除 live `scope=system` 输入，并清理聊天页 launch auto-match / picker 旧入口。
3. V1C: 做 resolution list/preview/apply/export-preview/export UI/RPC，覆盖 same-name、drift、multi-provider drift、unmanaged、canonical-deleted-with-drift。
4. V1D/E: 全量切 provider runtime，同时落地 startup/open project reconcile、write-time publish、provider-native personal mirror、Claude/Codex driver-level native discovery smoke，并删除旧注入链路、`skill_read_section` runtime dependency、旧 `.claude/skills` symlink 注入，迁移 prompt/uistate/app 中旧 skilllibrary/skillforge/fbsd 消费者和 old candidate 生产入口。这一组不能拆成可单独验收的中间态；缺少任一项都不算 V1 full landing。
5. V1F: 补 README / docs / 测试。
6. V2A: recorder 和 evidence packer。
7. V2B: proposal schema、validator、diff 展示。
8. V2C: 后台模型调用和 proposal 生成。
9. V2D: 用户确认后 apply、backup、audit、publish。
10. V3A: personal usage metadata、pin、archive、restore。
11. V3B: personal auto-apply policy。
12. V3C: curator dry-run。
13. V3D: curator real-run、snapshot、rollback。

## 17. 关键风险

1. Provider native skill 发现规则会变。应通过版本探测和 provider binary smoke test 保护。
2. Mirror 被用户或 provider 改动。必须依赖 manifest 和 drift，不凭目录存在推断所有权。
3. 同名 skill 静默覆盖会造成错误调用。默认严格冲突。
4. 后台模型 proposal 可能过度沉淀一次性经验。V2 只建议，V3 只允许低风险 personal/agent 自动 patch。
5. 项目级自动写入会破坏团队 review。V1-V3 禁止项目级自动应用。
6. 旧 cache/library 迁移可能误删用户数据。迁移只处理精确识别的旧 symlink，其余报冲突。

## 18. 当前决策清单

已定：

- 保留项目级和个人级。
- 项目级 canonical 是 `.agent/skills`。
- 个人级 canonical 使用 `~/.super-dolphin/skills/personal/...`。
- 生效个人级类型目录是 `user`、`agent`、`imported`；`hub` 仅保留为未来目录/市场来源，不参与运行时发现。
- mirror 是生成物，不提交。
- Claude/Codex CLI 由用户自行安装和登录；Super-Dolphin 默认同步个人 skill 到官方个人 skill 目录 `~/.claude/skills`、`~/.agents/skills`，不会把 `~/.codex` 当作 Codex skill mirror。
- 项目级 mirror 可编辑，但 drift 必须用户选择处理。
- 个人级可随意改，但仍要 backup/audit/rollback。
- V1 不做自进化。
- V2 做 proposal，不自动改。
- V3 只对 `personal/agent` 放开低风险自动维护。
- 同名冲突默认按钮顺序采用 D3。
- personal auto-maintenance 默认关闭。
- V1-V3 不做 archive purge，只做 restore/rollback。
- proposal 模型使用 Super-Dolphin owned auxiliary config，不默认复用当前会话 provider。
- V4 只作为 future。

变更这些默认值需要 owner 明确 override；实现计划不再停在二次确认上。
