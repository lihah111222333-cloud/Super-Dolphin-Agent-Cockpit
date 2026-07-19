package app

import (
	"io"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
)

// RunSchemaFilesystemWorkerIfRequested 在正常应用装配前分流 schema filesystem worker。
func RunSchemaFilesystemWorkerIfRequested(reader io.Reader, writer io.Writer) (bool, error) {
	return schema.RunFilesystemWorkerIfRequested(reader, writer)
}

// PrepareSchemaFilesystemWorker 在请求到达前固定 schema worker 宿主执行物。
func PrepareSchemaFilesystemWorker() (func() error, error) {
	return schema.PrepareFilesystemWorker()
}
