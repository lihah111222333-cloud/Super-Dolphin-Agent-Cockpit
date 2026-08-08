// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pdftext"
)

var errPDFTextNotFound = errors.New("datasource: pdf text content not found")

const datasourceMaxImportBytes = 10 * 1024 * 1024

// extractPDFText 将 PDF 正文抽取委托给唯一的 pdftext 真值实现，并保留 datasource 错误映射。
func extractPDFText(ctx context.Context, sourcePath string) (string, error) {
	result, err := pdftext.ExtractFile(ctx, sourcePath, pdftext.Limits{MaxInputBytes: datasourceMaxImportBytes, MaxOutputBytes: datasourceMaxImportBytes})
	if err != nil {
		if errors.Is(err, pdftext.ErrInputTooLarge) || errors.Is(err, pdftext.ErrOutputTooLarge) {
			return "", errDatasourceTextTooLarge
		}
		return "", err
	}
	return result.Text, nil
}
