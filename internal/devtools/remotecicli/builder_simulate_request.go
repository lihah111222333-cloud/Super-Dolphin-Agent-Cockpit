package remotecicli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// LoadAndBindBuilderSimulationRequest 严格读取显式 request 文件并绑定 exact Git 身份。
// 该入口只做本地读取与只读 Git 查询，不访问 OSS、ECI、SQLite 或输出结果。
func LoadAndBindBuilderSimulationRequest(ctx context.Context, requestPath string) (remoteci.BoundBuilderSimulationRequest, error) {
	if err := builderSimulationRequestContextErr(ctx); err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, err
	}
	if err := validateBuilderSimulationRequestPath(requestPath); err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, errors.New("builder simulation request path is invalid")
	}
	raw, err := readBuilderSimulationRequestFile(requestPath)
	if err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, errors.New("builder simulation request file cannot be read")
	}
	if err := builderSimulationRequestContextErr(ctx); err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, err
	}
	request, err := remoteci.DecodeBuilderSimulationRequest(raw)
	if err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, errors.New("builder simulation request cannot be decoded")
	}
	if err := builderSimulationRequestContextErr(ctx); err != nil {
		return remoteci.BoundBuilderSimulationRequest{}, err
	}
	bound, err := remoteci.BindBuilderSimulationRequest(ctx, request, gitObjectResolver{})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return remoteci.BoundBuilderSimulationRequest{}, fmt.Errorf("builder simulation request binding context: %w", contextErr)
		}
		return remoteci.BoundBuilderSimulationRequest{}, errors.New("builder simulation request cannot be bound")
	}
	return bound, nil
}

func builderSimulationRequestContextErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("builder simulation request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("builder simulation request context: %w", err)
	}
	return nil
}

// validateBuilderSimulationRequestPath 校验 caller 提供的 clean absolute request path。
func validateBuilderSimulationRequestPath(requestPath string) error {
	if requestPath == "" || strings.TrimSpace(requestPath) != requestPath || filepath.Clean(requestPath) != requestPath || !filepath.IsAbs(requestPath) {
		return errors.New("request path must be a clean absolute path")
	}
	for _, r := range requestPath {
		if unicode.IsControl(r) {
			return errors.New("request path contains control characters")
		}
	}
	return nil
}

// readBuilderSimulationRequestFile 执行 bounded read 并同时处理 close 结果。
func readBuilderSimulationRequestFile(requestPath string) ([]byte, error) {
	file, err := openBuilderSimulationRequestFile(requestPath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, descriptorMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.New("request file cannot be read")
	}
	if len(data) == 0 || len(data) > descriptorMaxBytes {
		return nil, errors.New("request file exceeds bounded size")
	}
	return data, nil
}

// openBuilderSimulationRequestFile 拒绝链接组件并返回已 fstat 的 regular 文件。
func openBuilderSimulationRequestFile(requestPath string) (*os.File, error) {
	if err := validateAbsolutePathComponents(requestPath, false); err != nil {
		return nil, errors.New("request path is not trusted")
	}
	info, err := os.Lstat(requestPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("request file must be a regular non-symlink file")
	}
	file, err := os.Open(requestPath)
	if err != nil {
		return nil, errors.New("request file cannot be opened")
	}
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() {
		closeErr := file.Close()
		if statErr != nil || closeErr != nil {
			return nil, errors.New("request file cannot be inspected")
		}
		return nil, errors.New("request file is not regular")
	}
	return file, nil
}
