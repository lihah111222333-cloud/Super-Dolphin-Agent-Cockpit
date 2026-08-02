# 项目级 Git hooks

执行 `make install-hooks` 安装版本化 hooks。安装器要求一个受信、绝对路径的 `super-dolphin-gate` launcher，并在每次运行时复验其 owner、类型、可执行权限及非 group/world 可写 mode。

所有提交和推送只使用 remote ECI 门禁；不存在本地 scheduler、Docker 容器执行或本地回退分支。两个 hook 都要求本地 Git 配置或环境提供 `SUPER_DOLPHIN_GATE_REMOTE_CONFIG` 和 `SUPER_DOLPHIN_GATE_LEDGER`，缺失即 fail closed。

远端 hook 不读取 `super-dolphin.remote.maxShards`，也不传递 `--max-shards`；分片调度不设本地上限，由远端协调器按当前工作负载决定。

`pre-commit` 先固定 staged tree，验证并在允许时单次刷新受管 closure 输出，然后以该精确 tree 和 parent commit 调用 `remote hook pre-commit`。`pre-push` 将 Git stdin 的每条精确 ref update 交给 `remote hook pre-push`。远端结果必须绑定同一 source tree、入口和清理证据；任何身份、权威性或状态缺失均拒绝 Git 动作。

`commit-msg` 仍要求中文提交信息和 fix-test evidence。hooks 不从调用方 PATH 解析 gate CLI，也不执行候选工作树中的脚本。
