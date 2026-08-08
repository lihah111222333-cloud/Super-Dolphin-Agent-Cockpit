# 远程 CI 本地初始化

`aliyun.json` 是可跨设备复用的无密钥阿里云 ECI 配置。克隆仓库后运行：

```bash
make remote-ci-init
```

初始化脚本会把跟踪配置原子复制到当前仓库的 `.git/super-dolphin-remote/config.json`，并在相邻位置创建本地 `config.baseline-state.sqlite` 的当前 schema/index。数据库为空时只建立 schema；accepted baseline 仍由 normal run/hook 使用配置内的严格 generation-one 回执，在实时验证云端事实后首写。

初始化还会安装仓库本地的 `git remote-ci` 别名。使用 `git remote-ci commit ...` 或 `git remote-ci push ...` 时，版本化启动器会从 GitHub CLI 的系统凭据存储读取当前 `github.com` 身份和短期 token，并从目标仓库的受信 Gate 为本次进程链签发 agent token，然后通过环境执行原始 Git 命令。启动器不把原始凭据写入 Git 配置、文件、SQLite、日志或命令参数。

同一启动器可从本仓库对另一个已经执行过 `make install-hooks` 的受信仓库调用：

```bash
./scripts/git_with_remote_ci_credentials.sh --repository /absolute/path/to/repository -- commit -m '中文提交信息'
./scripts/git_with_remote_ci_credentials.sh --repository /absolute/path/to/repository -- push
```

启动器所属仓库必须带有版本化 `.githooks/trusted-gate-launcher.sh`；目标仓库的 exact-tree Gate 必须已安装并通过 receipt 校验。GitHub CLI 未认证、Python 3 不可用、仅提供一半环境凭据、agent-token bootstrap 不规范或目标 Gate 不受信时都会 fail-fast；启动器不会回退为无凭据运行。

SQLite、WAL/SHM、日志、PID、current/latest 指针、运行结果和本地备份都属于设备本地状态，不得加入 Git。脚本不会覆盖非空 SQLite authority。
