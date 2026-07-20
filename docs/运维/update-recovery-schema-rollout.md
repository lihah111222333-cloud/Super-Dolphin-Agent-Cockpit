# Update recovery schema 灰度发布与停止条件

本文定义 update recovery schema 变更的唯一灰度顺序、证据边界和停止条件。发布阶梯固定为：内部 20 台 -> 10% -> 30% -> 100%。任何阶段都不能由前一阶段自动触发。

## 发布授权边界

- workflow_dispatch 每次只能选择并执行一个阶段。要进入下一阶段，必须重新发起 workflow、重新填写输入并通过该阶段独立的 GitHub Environment 审批。
- 五个发布元数据 `version`、`build_commit`、`signing_public_key_fingerprint`、`previous_version`、`monitoring_window_hours` 都必填；缺失或全空白立即失败。`stage` 是必选阶段，`predecessor_evidence` 是额外的阶段前驱证据输入，`macos_upgrade_matrix_evidence` 与 `windows_upgrade_matrix_evidence` 是额外的版本升级矩阵证据输入；全部都禁止空白。
- `build_commit` 必须是完整 commit SHA。签名公钥指纹定义为：对去除首尾空白后的公钥配置值做 SHA-256，使用小写十六进制比较。
- 内部阶段必须覆盖真实 macOS arm64 与 Windows arm64，并提供同一 commit 的原生打包验证、升级与回滚测试矩阵证据。
- 10%、30%、100% 每阶段观察窗口不得少于 8 小时。观察窗口结束且六项指标全部未命中停止条件，才允许申请下一阶段。
- 四个 Environment 分别为 `update-recovery-internal-20`、`update-recovery-10-percent`、`update-recovery-30-percent`、`update-recovery-100-percent`；审批人必须核对当前阶段、前驱 run、候选版本、回滚版本、commit、签名公钥指纹和观察窗口。
- 四个 Environment 必须在首次运行 workflow 前预先创建，并逐一配置 Required reviewers；平台支持时还必须启用 Prevent self-review，并禁止管理员 bypass。GitHub 可能为未预先创建的名称自动建立无保护 Environment，因此任何环境未创建、Required reviewers 未配置或适用的保护未启用都视为发布 blocker，禁止触发 workflow。
- 只允许从仓库默认分支触发该 workflow；`validate-inputs` 必须在动态 Environment job 之前校验 default branch 和四阶段 allowlist，防止 API 绕过 choice 输入或从任意分支替换 workflow。

## 原生证据与升级矩阵

每次执行都必须通过组织级 runner group `update-recovery-release`，并在其中选择带 `self-hosted`、平台、`ARM64` 和专用 `update-recovery-release` 标签的真实 runner。该 runner group 必须仅授权本仓库和默认分支的 `.github/workflows/release.yml`；未配置仓库与 workflow 访问边界视为发布 blocker。runner 缺失、架构不符、打包或测试失败都会阻断阶段审批，不允许 fallback 或 `continue-on-error`。macOS 打包 job 绑定所选阶段的受保护 Environment，在审批前不能取得该 Environment 的签名与公证秘密；Windows job 必须等待该审批和 macOS 证据成功后才能执行。

| 平台 | 必须执行 | 证据 |
| --- | --- | --- |
| macOS arm64 | `make release-update-gate`；`scripts/package_macos.sh` 的签名、打包与包内验证 | 当前 commit 的 DMG、原生测试日志、打包日志以及当前 commit 的版本无关恢复状态矩阵 metadata |
| Windows arm64 | updater/appupdate/failure 与 Guard 原生矩阵；`scripts/package_windows.ps1 -Artifact zip` | 当前 commit 的 ZIP、原生测试日志、打包日志以及当前 commit 的版本无关恢复状态矩阵 metadata |

`release-update-gate` 只证明当前 commit 的版本无关恢复状态矩阵，不证明指定 `previous_version -> version` 或 `version -> previous_version` 已真实执行。两种方向必须此前已在真实 macOS arm64 与 Windows arm64 外部 runner 上执行，并分别以不同的、可定位且不可混淆的本仓库 GitHub Actions run 或 artifact URL 填入 `macos_upgrade_matrix_evidence` 与 `windows_upgrade_matrix_evidence`。任一证据缺失、URL 不属于本仓库 Actions run、两 URL 相同，或证据未同时绑定相同 `version`、`build_commit`、`signing_public_key_fingerprint`、`previous_version`，都视为发布 blocker。

Windows 当前只支持本地 package/verify 证据；`package_windows_github_release.ps1` 对 package-owned publishing 明确 fail-fast unsupported。该限制不得被包装成发布成功，也不得用 macOS 证据替代 Windows 原生证据。

阶段前驱要求：内部 20 台将 `predecessor_evidence` 固定为 `native-arm64-current-run`，并使用本次 macOS/Windows ARM64 artifacts；10% 引用内部阶段完成且观察结束的 GitHub Actions run URL；30% 引用 10% 完成 run；100% 引用 30% 完成 run。Environment reviewer 必须打开百分比阶段的 `predecessor_evidence`，并逐一核对两平台 upgrade matrix URL 均对应相同 `version`、`build_commit`、`signing_public_key_fingerprint` 和 `previous_version`；缺一即阻断审批。

## 六项停止条件

六项指标在完整观察窗口内持续计算；任一条件命中即判定当前阶段失败。

| 指标 | 命中阈值 | 强制动作 |
| --- | --- | --- |
| schema_helper_reap_failed_total | > 0 | 立即停止扩量 |
| schema_helper_capacity_exhausted_rate | > 0.1% | 停止扩量 |
| schema_helper_duration_p95 | > 1.5s | 停止扩量 |
| recovery_transaction_age_max | > 60s | 停止扩量并保留 journal |
| sqlite_mcp_first_start_failure_rate | > 1% | 停止扩量 |
| rollback_convergence_failure_total | > 0 | 回撤当前版本 |

任何停止条件命中后禁止自动进入下一阶段。操作者必须取消当前阶段授权、保留 workflow artifacts 和监控快照，并按表中动作处置；只有新候选版本重新从内部 20 台开始，禁止在原 run 内重试晋级。

## 阶段完成记录

每阶段的审批记录至少包含：workflow run URL、Environment 审批人和时间、五个发布元数据、前驱证据、两平台原生 artifacts、六项指标的完整窗口截图或查询链接、停止条件结论及回滚负责人。缺少任一项都不能作为下一阶段前驱。
