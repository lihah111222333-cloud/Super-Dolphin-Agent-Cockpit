# Compact round state（仅在 round_state flag 开启时注入）

跨轮只携带以下字段：`round`、`patch_hash`、`latest_failure`、`unresolved_evidence`、`cache_key`、`previous_hypothesis`。

不要复制 transcript、full log、已通过测试或重复的历史失败；缺少最新证据时保持 unresolved，不猜测根因。

```json
{"round":2,"patch_hash":"sha256:...","latest_failure":"...","unresolved_evidence":["..."],"cache_key":"...","previous_hypothesis":"..."}
```
