# P21 测试夹具

> 来源：`docs/plans/迁移/p21/fix01.v2.md` §五 / `docs/security/p21-redteam.sh`
>
> 这些夹具是 P21 红队脚本和阶段 A/B/C/D 测试的共享基座。
> **不要**把任何真实密钥、真实工作树、外部服务凭据放到这里。

## 目录结构

| 目录 | 用途 | 谁用 |
|---|---|---|
| `repos/` | 多 repo 工作树（含 symlink、相对路径） | RT-2 / RT-3 fingerprint 隔离测试 |
| `secrets/` | ≥15 类高价值密钥语料（**全部为合成假值**） | RT-5 脱敏覆盖断言 |
| `prompt-injection/` | 提示注入语料 | extractor / reviewer 注入防护断言 |
| `webhooks/` | mitmproxy / httpbin 回放脚本 | RT-9 SSRF + redirect 重校验 |
| `cron/` | 时钟漂移、lease 过期录像 | C1 cron crash recovery 测试 |

## 快速使用

```bash
# 1. 启动多 repo 工作树（fingerprint 隔离测试）
bash test/fixtures/p21/repos/bootstrap.sh

# 2. 验证脱敏夹具全部命中
go run ./test/fixtures/p21/secrets/check_redaction.go \
       test/fixtures/p21/secrets/sample.txt

# 3. 跑全套红队脚本
bash docs/security/p21-redteam.sh
```

## 安全约束

- `secrets/sample.txt` 中的所有"密钥"必须是合成假值（无效前缀 / 全 X / lorem ipsum 字符），
  即便不慎泄露也不能登录任何真实系统。
- `repos/` 目录内不得包含实际项目代码；只放最小占位 README，避免与产品 cwd 混淆。
- `webhooks/` 的回放脚本只能命中 `127.0.0.1` / `localhost` / 显式 mock 域名，
  禁止打到任何真实第三方 webhook。

## 维护

新增夹具时同步：
1. 在本 README 增加表行说明
2. 在 `docs/security/p21-redteam.sh` 增加对应 ASSERT
3. 在 `docs/plans/迁移/p21/fix01.v2.md` §五登记
