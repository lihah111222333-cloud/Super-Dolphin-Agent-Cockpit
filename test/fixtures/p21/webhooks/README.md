# P21 webhook 回放夹具

P2 webhook 适配器（Slack / 钉钉 / 飞书）的本地回放点。

## 启动方式

二选一：

### A. httpbin 容器（最简）
```bash
docker run --rm -p 18080:80 kennethreitz/httpbin
# 端点：
#   POST http://127.0.0.1:18080/post           普通回声
#   POST http://127.0.0.1:18080/redirect-to    302 跳转（SSRF 重校验）
```

### B. mitmproxy 脚本（需要复杂语义）
```bash
mitmdump -s test/fixtures/p21/webhooks/replay.py -p 18443
```

## 必须覆盖的回放场景

| 场景 | 端点 | 期望行为 |
|---|---|---|
| 200 正常回声 | /post | adapter 接受 200 |
| 5xx 重试 | /status/503 | 指数退避 + 最多 3 次 |
| 302 → 私网 | /redirect-to?url=http://10.0.0.1/ | 重跑 LookupIP，拒绝 |
| 302 → loopback | /redirect-to?url=http://127.0.0.1/ | 重跑 LookupIP，拒绝 |
| 302 → DNS rebind | /redirect-to?url=http://rebind.example/ | 解析后再次 isBlockedIP，拒绝 |

## 安全约束

- 任何回放脚本只能命中 `127.0.0.1` / `localhost` / 显式 mock 域名。
- 禁止把真实 Slack / 钉钉 / 飞书 webhook URL 写入夹具。
