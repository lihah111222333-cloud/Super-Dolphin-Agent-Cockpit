# Round 009 - 第二梯队：skill mirror + fs 兜底

## 来源

Round-002 扫雷 agent 报告：skill/mirror_* 5 条 + skill/fs 5 条。

## Findings

### 1. [major] skill/mirror_manifest.go:444 — providerPersonalMirrorRoot 吞 UserHomeDir error

**证据**：`os.UserHomeDir()` 失败时返回 `""`，下游用空路径构建 mirror target。
**精修**：签名改为 `(string, error)`，caller 处理。

### 2. [major] skill/mirror_reconciler_actions.go:245 — os.Stat error 全部当 not-exist

**证据**：`canonicalSkillDirExists` 把权限错误也当"不存在"。
**精修**：`if os.IsNotExist(err) { return false } return false, err`。

### 3. [major] skill/skills_fs.go:300, :422 — requireCWD 错误丢弃（已在 round-005 #7 确认）

### 4. [moderate] skill/mirror_publisher.go:146 — unchecked type assertion `value.(*sync.Mutex)`

**证据**：sync.Map 中存的值如果不是 *sync.Mutex，直接 panic。
**精修**：comma-ok 检查。

### 5. [moderate] skill/mirror_reconciler_external_personal.go:136 — rollback rename error 丢弃

**证据**：`_ = os.Rename(tempDir, sourceDir)` 失败时 swap 回滚失败，文件系统处于不一致状态。
**精修**：log.Error + 返回 wrapped error。

### 6. [moderate] skill/skills_meta.go:169 — 越界索引（已在 round-003 #2 确认）

### 7. [moderate] skill/contract.go:226 — canonicalProjectPath 吞错误返回 raw cwd

**证据**：路径规范化失败时返回原始 cwd，下游可能用非规范路径做 key。
**精修**：返回 error。

### 8. [moderate] skill/mirrorpath/path.go:53 — 硬编码 darwin 路径

**证据**：`allowedRootSymlinkAncestor` 只处理 darwin，其他 OS 静默 pass。
**影响**：Linux/Windows 上 symlink 攻击不被检测。
**精修**：按 runtime.GOOS 分支处理，未知 OS 返回 error。
