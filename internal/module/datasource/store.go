// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

// DatasourceDocument 是 datasource 服务暴露给 prompt 动态段的文档内容别名。
// 别名固定在 contract 层，避免 module 与 store 之间复制 wire 字段。
type DatasourceDocument = contract.DatasourceDocument

// UpsertDatasourceDocumentParams 是 datasource 文档落库的跨层写入参数。
// service 只填当前 workspace、文件名和正文；具体持久化策略由 store 实现决定。
type UpsertDatasourceDocumentParams = contract.UpsertDatasourceDocumentParams

// DatasourceDocumentStore 是 datasource service 依赖的最小存储接口。
// 该接口位于 contract 层，方便 prompt provider 和 store 实现共享同一份边界。
type DatasourceDocumentStore = contract.DatasourceDocumentStore
