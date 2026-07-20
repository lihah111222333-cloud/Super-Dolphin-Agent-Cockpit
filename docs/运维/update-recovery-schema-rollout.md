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
- `validate-inputs` 必须先在 GitHub-hosted runner 上 checkout 默认分支的完整历史，再执行 `git merge-base --is-ancestor build_commit origin/default_branch`；反向 ancestry、浅克隆或在 self-hosted runner 上补做该检查都不构成发布授权。

## 当前部署 blocker

当前仓库 owner 类型为 `User`，GitHub 不提供本方案要求的自定义 runner group；因此本 workflow 当前不可部署、不可用于发布。只有以下三项全部完成后才能触发：仓库迁移到 `Organization`；组织管理员完成 `update-recovery-release` runner group 预检；可信的 `.github/workflows/update-recovery-upgrade-evidence.yml` producer workflow 与两平台真实 attestation 已实际部署并成功运行。`validate-inputs` 通过仓库 REST API 强制要求 `owner.type=Organization`，并要求可信 producer 文件存在；任一不满足都会 fail-fast，不允许只凭 URL、人工口头确认或标签名称继续。

组织管理员必须在触发前使用具备 Actions 管理权限的凭据完成外部 API 预检并保存响应：查询组织 runner groups，确认目标 group 名称为 `update-recovery-release`、`visibility=selected`、selected repositories 仅包含本仓库、`restricted_to_workflows=true`，且 selected workflow 精确为 `owner/repo/.github/workflows/release.yml@refs/heads/<default_branch>`。仓库 `GITHUB_TOKEN` 通常无权读取组织 runner group 管理配置，因此 workflow 不得把 403/404 当作通过或静默降级；管理员预检响应必须作为 Environment 审批证据，由 reviewer 逐项核对。未提供该证据即为发布 blocker。

## 原生证据与升级矩阵

每次执行都必须通过组织级 runner group `update-recovery-release`，并在其中选择带 `self-hosted`、平台、`ARM64` 和专用 `update-recovery-release` 标签的真实 runner。该 runner group 必须仅授权本仓库和默认分支的 `.github/workflows/release.yml`；未配置仓库与 workflow 访问边界视为发布 blocker。runner 缺失、架构不符、打包或测试失败都会阻断阶段审批，不允许 fallback 或 `continue-on-error`。macOS 打包 job 绑定所选阶段的受保护 Environment，在审批前不能取得该 Environment 的签名与公证秘密；Windows job 必须等待该审批和 macOS 证据成功后才能执行。

| 平台 | 必须执行 | 证据 |
| --- | --- | --- |
| macOS arm64 | `make release-update-gate`；`scripts/package_macos.sh` 的签名、打包与包内验证 | 当前 commit 的 DMG、原生测试日志、打包日志以及当前 commit 的版本无关恢复状态矩阵 metadata |
| Windows arm64 | updater/appupdate/failure 与 Guard 原生矩阵；`scripts/package_windows.ps1 -Artifact zip` | 当前 commit 的 ZIP、原生测试日志、打包日志以及当前 commit 的版本无关恢复状态矩阵 metadata |

`release-update-gate` 只证明当前 commit 的版本无关恢复状态矩阵，不证明指定 `previous_version -> version` 或 `version -> previous_version` 已真实执行。两种方向必须此前已由固定路径 `.github/workflows/update-recovery-upgrade-evidence.yml` 在真实 macOS arm64 与 Windows arm64 runner 上执行，并分别以不同的本仓库精确 run URL 填入 `macos_upgrade_matrix_evidence` 与 `windows_upgrade_matrix_evidence`；唯一允许的 URL 形态是 `https://github.com/<owner>/<repo>/actions/runs/<正整数>`，禁止 path、query 或 fragment 后缀。

可信 producer 必须分别上传唯一、未过期的 `update-recovery-upgrade-attestation-macos-arm64` 与 `update-recovery-upgrade-attestation-windows-arm64` artifact，根目录只包含一个 `attestation.json`。`validate-inputs` 使用 `actions: read` 调用 GitHub API，验证 run 存在、`conclusion=success`、`head_sha=build_commit`、workflow path 精确匹配 producer，并下载 attestation 核对 `version`、`build_commit`、`signing_public_key_fingerprint`、`previous_version`、`monitoring_window_hours` 完整五元组、平台和双向升级字段。任何 API、下载、解压、唯一性或字段校验失败都立即阻断。当前仓库尚未部署该可信 producer，因此该 contract 是有意 fail-closed 的部署 blocker，不得退化为仅信 URL。

Windows 当前只支持本地 package/verify 证据；`package_windows_github_release.ps1` 对 package-owned publishing 明确 fail-fast unsupported。该限制不得被包装成发布成功，也不得用 macOS 证据替代 Windows 原生证据。

阶段前驱要求：内部 20 台将 `predecessor_evidence` 固定为 `native-arm64-current-run`，并使用本次 macOS/Windows ARM64 artifacts；10% 引用内部阶段完成且观察结束的精确 run URL；30% 引用 10% 完成 run；100% 引用 30% 完成 run。每个 final stage job 上传 `update-recovery-stage-attestation-<stage>`，绑定 stage 和完整五元组。百分比阶段通过 API 验证前驱 run 成功、`head_sha` 相同、workflow path 精确为 `.github/workflows/release.yml`，并下载唯一、未过期的预期上一阶段 attestation 核对 stage 与完整五元组。Environment reviewer 必须同时核对前驱和两平台证据中的 `version`、`build_commit`、`signing_public_key_fingerprint`、`previous_version`、`monitoring_window_hours`；缺一即阻断审批。

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
