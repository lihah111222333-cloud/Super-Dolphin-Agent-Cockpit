package datasource

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// DatasourceDocument 是数据源服务对外使用的文档内容。
type DatasourceDocument = contract.DatasourceDocument

// UpsertDatasourceDocumentParams 是数据源文档写入存储时的参数。
type UpsertDatasourceDocumentParams = contract.UpsertDatasourceDocumentParams

// DatasourceDocumentStore 是数据源服务需要的文档存储接口。
type DatasourceDocumentStore = contract.DatasourceDocumentStore
