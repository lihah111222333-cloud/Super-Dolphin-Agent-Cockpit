package datasourcev2

import "errors"

// errPDFTextNotFound 保留 RPC 层的历史错误映射；PDF 正文实际由 service.go 直接调用 pdftext SSOT。
var errPDFTextNotFound = errors.New("datasource v2: pdf text content not found")
