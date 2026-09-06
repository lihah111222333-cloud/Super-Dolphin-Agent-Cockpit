# Provider executable digest owner（接线前置设计）

状态：设计裁决，尚未接入生产。

## 当前事实

`standalone.Provider` 当前只声明 `Name/Executable/Home/Models/Sandbox`，而
`managedruntime.NewProcessReceiptVerifier` 要求一个显式
`ExecutableDigestVerifier`。当前仓库没有 production digest owner；只有测试
fixture 和 verifier 接口。因此不能从路径、PID 或 start-token 推导发布制品摘要。

## 裁决

Provider 配置必须新增 `executable_digest`，固定为小写64字符hex；缺失、大小写不规范、
长度错误或与发布制品不匹配均 fail-fast。配置解析不自动读取个人CLI配置、PATH、
shell、临时文件或网络来源。测试 fixture 可以在创建测试配置时对自身明确执行文件计算
摘要，但不能把这种计算变成 production fallback。

production digest owner 必须在安装/发布制品层提供 immutable manifest：

```text
{ provider, executable_identity, executable_digest, artifact_version, schema_version }
```

manifest 必须在 service 启动前由受信安装目录读取并逐字段校验；Provider config 的
digest 与 manifest 精确匹配后，才构造 `ExecutableDigestVerifier`。不匹配时服务不得
创建 listener、发布 ready 或启动 provider。

## 接线顺序

```text
LoadConfig
  → validate executable_digest
  → load immutable artifact manifest
  → compare provider executable identity/digest
  → construct ExecutableDigestVerifier
  → Fx inject ProcessReceiptVerifier
  → launch/ready → capture/verify → Workflow.Finalize
```

禁止把 verifier 隐式创建在 Workflow、Execution 或测试 helper 内；禁止把测试摘要、
进程路径或当前文件hash当作发布真值。Codex/Claude 必须分别匹配自己的 executable
identity；Windows/unsupported capture 保持 fail-closed/unknown。

## 完成证据

需要新增配置字段守卫、manifest parser/immutability测试、Fx缺verifier启动失败测试、
真实 Codex/Claude Launch→ready→capture→verify→Finalize E2E。完成前保持
`CONTENT-Production: NO-GO`、`ST01–ST18 Active`，不声称正式服务已发布。
