Guard 规则：仓库 guard 是完成条件，不是形式检查；失败即任务未完成，先定位根因再继续。

常见门槛：
- Go 生产文件默认不超过 600 行，函数不超过 80 行，圈复杂度不超过 10，包总行数不超过 10000。
- fix 类改动必须带同提交的回归测试、fixture、golden 或 snapshot。
- 不要删除、削弱或绕过 `.agents/skills/guarding-go-projects` 下的项目守卫来通过任务，除非用户明确要求调整 guard。
- 先跑命中包测试，再跑 guard/build；报告中区分本次回归和仓库既有失败。
