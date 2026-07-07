# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4049
>
> 未细分职责文件：53

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 53 |
| 未细分职责占比 | 1.31% |

## 2. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `internal` | 26 |
| `test` | 9 |
| `third_party` | 9 |
| `cmd` | 7 |
| `sql` | 1 |
| `tests` | 1 |

## 3. 样例文件

- `cmd/super-dolphin-release-manifest/main.go`
- `cmd/super-dolphin-release-manifest/main_test.go`
- `cmd/super-dolphin-updater/detach_darwin.go`
- `cmd/super-dolphin-updater/detach_default.go`
- `cmd/super-dolphin-updater/install.go`
- `cmd/super-dolphin-updater/install_test.go`
- `cmd/super-dolphin-updater/main.go`
- `internal/guards/code_size_guard_test.go`
- `internal/guards/guard_manifest.json`
- `internal/guards/refactor_baseline.json`
- `internal/guards/rollback_skip_guard_test.go`
- `internal/testutil/golden/golden.go`
- `internal/testutil/golden/orchestration_stub.go`
- `internal/util/clone/clone.go`
- `internal/util/configutil/configutil.go`
- `internal/util/ctxutil/ctxutil.go`
- `internal/util/historyjsonl/history.go`
- `internal/util/historyjsonl/history_test.go`
- `internal/util/historyjsonl/page.go`
- `internal/util/idempotency/registry.go`
- `internal/util/idempotency/registry_test.go`
- `internal/util/identifier/uuid.go`
- `internal/util/identifier/uuid_test.go`
- `internal/util/idgen/idgen.go`
- `internal/util/jsoninput/jsoninput.go`
- `internal/util/pathutil/pathutil.go`
- `internal/util/pathutil/pathutil_test.go`
- `internal/util/repofingerprint/fingerprint.go`
- `internal/util/repofingerprint/fingerprint_test.go`
- `internal/util/safego/safego.go`
- `internal/util/toolresults/path.go`
- `internal/util/toolresults/path_test.go`
- `internal/util/util.go`
- `sql/schema/prompt_intent_drafts.sql`
- `test/fixtures/p21/README.md`
- `test/fixtures/p21/cron/README.md`
- `test/fixtures/p21/prompt-injection/samples.txt`
- `test/fixtures/p21/repos/.gitkeep`
- `test/fixtures/p21/repos/bootstrap.sh`
- `test/fixtures/p21/secrets/check_redaction.go`
- `test/fixtures/p21/secrets/sample.txt`
- `test/fixtures/p21/webhooks/README.md`
- `test/fixtures/p21/webhooks/replay.py`
- `tests/scripts/guard_env_test.sh`
- `third_party/kelindar-event/LICENSE`
- `third_party/kelindar-event/README.md`
- `third_party/kelindar-event/default.go`
- `third_party/kelindar-event/default_test.go`
- `third_party/kelindar-event/event.go`
- `third_party/kelindar-event/event_test.go`

## 4. 修复方式

优先在 `scripts/generate_ai_project_map.js` 的 `PURPOSE_RULES` 中补充路径前缀和职责说明，然后重新运行：

```bash
node scripts/generate_ai_project_map.js
```
