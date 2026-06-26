// Package difftracker 提供基于 git 的工作区快照和 diff 渲染能力。
// toolbridge 在工具调用前记录脏文件状态，调用后再用这里的快照生成统一 diff，避免把二进制或超限文件塞进事件载荷。
package difftracker
