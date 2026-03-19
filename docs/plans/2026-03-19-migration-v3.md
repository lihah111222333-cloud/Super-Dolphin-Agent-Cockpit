# Super Agent V3 — 迁移模式方案

> **日期:** 2026-03-19
> **前身:** go-agent-v2 Two-Zone DRY V2 方案 (2026-03-18)
> **决策:** 放弃原地重构，采用迁移模式——新建项目从 0 开始，按函数粒度迁移
> **核心原则:** 旧代码就是最好的护栏；迁移 > 原地重构；进度可视；风险隔离

---

## 0. 为什么放弃原地重构

V2 方案设计严谨，但存在结构性矛盾：

1. **护栏成本倒挂** — 需 13-20 天才能改第一行业务代码
2. **测不准困境** — 先理解旧行为 → 冻结 → 改 → 证明没变
3. **87K 行原地动刀** — 如同飞行中换引擎
4. **总工期 45-66 天** — 20% 纯粹为原地重构风险而生

**迁移模式天然解决：**
- 旧代码继续跑 = 天然冻结
- 只需证明"新的对"
- 不迁移 = 自动消失（薄 wrapper）
- 新架构从第一天就设计正确

---

## 1. V3 项目基本信息

```
目标目录:   /Volumes/bot/super-agent-v3
模块名:     github.com/anthropic/super-agent-v3
Go 版本:    1.25.7
源项目:     go-agent-v2 (83,583 行)
目标行数:   ≤45,000 行
```

---

## 2. 新架构

```
cmd/                    ← 入口组装层
internal/               ← 业务实现层
├── apiserver/          ← RPC 服务
├── runner/             ← 状态机核心
├── store/              ← 数据层
├── bus/                ← 事件总线（零业务依赖）
├── uistate/            ← UI 状态管理
├── mcp/                ← MCP 运行时
├── executor/           ← 代码执行器
├── dashboard/          ← 仪表盘
├── guards/             ← 护栏测试
└── config/             ← 配置
pkg/                    ← 可复用库层
├── factory/            ← 跨包 DRY 原语（Zone A）
├── agentsdk/           ← Provider 抽象
├── toolsdk/            ← 工具 SDK
├── idamcp/             ← IDA MCP
├── errors/             ← 统一错误
├── logger/             ← 日志
└── util/               ← 工具函数
```

---

## 3. 迁移优先级 P0-P7

| 批次 | 模块 | 行数 | 迁移策略 |
|---|---|---|---|
| **P0** | pkg/factory, errors, util, logger | ~1,620 | 新建 + 直接搬 |
| **P1** | config, bus, store | ~4,734 | 精简后搬 |
| **P2** | runner, agentsdk | ~14,272 | 规则表化后搬 |
| **P3** | toolsdk, mcp | ~17,943 | descriptor 化后搬 |
| **P4** | uistate, executor | ~5,219 | 精简后搬 |
| **P5** | apiserver, dashboard | ~17,977 | handler 骨架重写 |
| **P6** | idamcp | ~10,199 | 独立迁移 |
| **P7** | cmd/ 入口 | ~4,264 | 最后组装 |

---

## 4. 护栏精简

V2: 700+ 守卫 → V3: ~150-200 核心守卫

**V3 三层护栏：**
1. Contract Tests — 核心行为合同
2. Schema Golden — snapshot 比对
3. CI 硬门槛 — race/lint/行数预算

---

## 5. 时间估算

总计：34-51 天（比 V2 少 ~25%，且第一天就能写业务代码）
