# 远程 CI 本地初始化

`aliyun.json` 是可跨设备复用的无密钥阿里云 ECI 配置。克隆仓库后运行：

```bash
make remote-ci-init
```

初始化脚本会把跟踪配置原子复制到当前仓库的 `.git/super-dolphin-remote/config.json`，并在相邻位置创建本地 `config.baseline-state.sqlite` 的当前 schema/index。数据库为空时只建立 schema；accepted baseline 仍由 normal run/hook 使用配置内的严格 generation-one 回执，在实时验证云端事实后首写。

SQLite、WAL/SHM、日志、PID、current/latest 指针、运行结果和本地备份都属于设备本地状态，不得加入 Git。脚本不会覆盖非空 SQLite authority。
