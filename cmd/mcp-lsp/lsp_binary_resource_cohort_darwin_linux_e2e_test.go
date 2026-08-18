//go:build e2e && (darwin || linux)

// 资源 cohort E2E 的 Darwin/Linux 平台边界由 build tag 明确声明。
// 实现集中在 lsp_binary_resource_cohort_e2e_test.go，避免同一 package 的 const/type/func 重复定义。
// Windows 不编入该测试文件。
package main
