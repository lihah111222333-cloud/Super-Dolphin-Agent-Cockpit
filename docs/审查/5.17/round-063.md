# Round 063 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:54:36 KST
- 结束：2026-05-17 08:56:30 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 skill 文件系统读写、导入复制、路径解析与项目/system scope 隔离。重点看自动生成或导入的技能是否可覆盖既有技能、绕过根目录约束，或把不可信内容提升到更高信任域。

- `internal/module/skill/skills_fs.go`
- `internal/module/skill/skills_import.go`
- `internal/module/skill/service.go`
- `internal/module/skill/skills_meta.go`
- `internal/module/skill/system_review.go`
- `internal/module/skill/skills_fs_test.go`
- `internal/module/skill/skills_import_test.go`
- `internal/module/skill/scope_test.go`
- `internal/module/skill/create_skill_test.go`
- `internal/module/skill/skill_name_test.go`
- `.agent/skills/安全工程师/SKILL.md`
- `.agent/skills/完成前验证/SKILL.md`

## Findings

1. **[critical] 相对路径写入没有 canonical containment 复核，技能根内 symlink 可把 `WriteLocal` 写到根外**
   - 证据：`WriteLocal()` 先用 `resolveSkillPath()` 得到目标，再直接 `os.MkdirAll(filepath.Dir(path))` 和 `os.WriteFile(path, ...)`（`internal/module/skill/skills_fs.go:280-311`）。相对路径分支 `resolveScopedSkillPath()` 只 `filepath.Clean` 并拒绝 `..`，随后 `filepath.Join(root, cleaned)`；没有对最终路径 `EvalSymlinks` 或 `pathEscapesRoot` 复核（`internal/module/skill/service.go:316-331`）。已有逃逸测试只覆盖绝对根外路径（`internal/module/skill/skills_fs_test.go:53-101`），未覆盖根内 symlink。
   - 风险：只要项目 `.agent/skills` 内存在指向根外目录的 symlink，调用 `skills/local/write` 或候选 promote 经由相对路径就可能覆盖项目外文件。量化反馈链路如果接受自动候选内容，最终落盘点会继承这个文件系统逃逸风险。
   - 建议：相对路径解析后也走 canonical containment；写入前对父目录和目标文件做 `Lstat`/`EvalSymlinks` 检查，拒绝任何路径段 symlink 指向根外。补充 symlink 逃逸回归测试。

2. **[major] `ReadLocal`/`ListLocalFiles` 相对路径同样可通过根内 symlink 读取项目外文件**
   - 证据：`ReadLocal()` 与 `ListLocalFiles()` 都调用 `resolveSkillPath(path, cwd, "")` 后直接 `os.Stat`/`os.ReadFile` 或 `os.ReadDir`（`internal/module/skill/skills_fs.go:211-258`）。绝对路径分支会 canonical 后用 `pathEscapesRoot` 校验（`internal/module/skill/service.go:291-314`），相对路径分支没有同等校验（`internal/module/skill/service.go:316-331`）。
   - 风险：根内 symlink 可把读取接口变成项目外文件读取入口，泄露 repo 外配置、临时文件或用户级技能内容。对“量化引擎”来说，这会污染后续候选生成和审查上下文，也可能把非技能文件内容暴露给上层模型。
   - 建议：所有读接口在打开文件或目录前使用同一套“最终真实路径必须仍在 skill root 内”的校验；目录枚举要拒绝 symlink 目录。

3. **[major] `CreateSkill`/`WriteLocal` 默认覆盖已有技能，没有 CAS、确认或冲突保护**
   - 证据：`CreateSkill()` 只校验 name/content 后委托 `WriteLocal(ctx, name, content, project)`（`internal/module/skill/skills_fs.go:261-278`）。`WriteLocal()` 在目标存在时通过 `writableSkillFileMode()` 取旧权限并 `os.WriteFile` 原地覆盖（`internal/module/skill/service.go:262-277`，`internal/module/skill/skills_fs.go:300-308`）。scope 测试只确认 project/system 路由，没有覆盖“已有技能不可被自动覆盖”的语义（`internal/module/skill/scope_test.go:49-84`）。
   - 风险：自动学习或候选审批产生同名 slug 时，会静默替换项目技能。上一轮已发现候选 slug 可能由 turn id 派生；如果未来改成语义化 slug，同名冲突会直接损坏人工维护技能。
   - 建议：CreateSkill 默认使用 create-only；覆盖必须携带 expected content hash 或明确 overwrite flag，并在审计事件中记录旧 hash/新 hash。

4. **[moderate] import 复制无文件数/总字节限制，且 content hash 阶段静默跳过读错文件**
   - 证据：`importSkillUnit()` 先用 `skillDirContentHash(resolvedSource)` 做 review key，再调用 `copySkillDir()` 复制（`internal/module/skill/skills_import.go:165-200`）。`copySkillDir()` 对每个文件 `os.ReadFile` 到内存并累加 files/bytes，但没有上限（`internal/module/skill/skills_import.go:255-283`）。`skillDirContentHash()` 遇到 walk/read 错误直接跳过或仅 warn，仍返回某个 hash（`internal/module/skill/system_review.go:58-82`）。
   - 风险：导入巨量文件会造成内存/磁盘放大；review hash 可能不覆盖实际随后复制的所有内容，形成审批对象与落盘对象不一致。
   - 建议：导入前强制每文件、总文件数、总字节数上限；hash 阶段遇到 read/walk 错误应 fail closed，并与复制阶段使用同一遍 manifest。

5. **[moderate] project skill 可以在 frontmatter 中自声明 `trust: user`，扫描时覆盖默认不可信域**
   - 证据：项目 root 默认 `TrustProject`，system root 默认 `TrustUser`（`internal/module/skill/skills_meta.go:85-88`，`internal/module/skill/skills_meta.go:130-133`）。但 `applyMetaTrust()` 会把 frontmatter 的 trust 值写入 `info.Trust`（`internal/module/skill/skills_meta.go:315-323`），`parseSkillInfo()` 只有在 `TrustUnknown` 时才回退默认 trust（`internal/module/skill/skills_meta.go:184-192`）。
   - 风险：仓库内 `.agent/skills` 的不可信技能可自声明为 trusted/user，从而影响 expand 审批和事件 scope。若量化反馈生成的技能携带该 frontmatter，可能绕过项目技能应有的人工确认。
   - 建议：project root 扫描时忽略或降级 frontmatter 中高于 root 默认信任域的 trust；仅 system root 或签名验证后允许 `TrustUser/TrustSigned`。

## 误报与已覆盖项

- 绝对路径访问已有 containment 校验，测试覆盖了显式根外 `ReadLocal`、`ListLocalFiles`、`WriteLocal`（`internal/module/skill/skills_fs_test.go:53-101`）。
- system scope 写入会触发 `ErrSkillSystemReviewRequired`，默认 project scope 不会直接写 user/global root（`internal/module/skill/scope_test.go:49-154`）。
- import 会拒绝导入源目录内的 symlink 文件，并在失败后删除目标目录，覆盖了部分 symlink 注入场景（`internal/module/skill/skills_import.go:188-191`，`internal/module/skill/skills_import_test.go:67-93`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/skill -count=1
```

结果：通过。

## 下一轮建议

- Round 064 审查 skill expand/approval/resource 读取链路：artifact locator、resource target、approval cache key、trusted/untrusted 语义，确认候选技能落盘后是否能绕过首次执行审批。
