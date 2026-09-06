package standalone

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// ReadCredential 读取显式私有文件，不扫描个人配置，不把 token 放入错误或日志。
func ReadCredential(path string) (token string, err error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("orch credential_file must be absolute")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return "", errors.New("orch credential_file must be an existing regular file, not a symlink")
	}
	if err := securefs.CheckPrivateOwnerOnly(path, before); err != nil {
		return "", fmt.Errorf("orch credential permissions: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("orch credential_file cannot be opened")
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return "", errors.New("orch credential_file identity changed")
	}
	return readCredentialBody(file)
}

// readCredentialBody 有界读取 bearer token，允许文件末尾换行但拒绝内部空白。
func readCredentialBody(reader io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(raw) > 4096 {
		return "", errors.New("orch credential_file is unreadable or oversized")
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 24 {
		return "", errors.New("orch credential must contain at least 24 visible ASCII characters")
	}
	for _, value := range token {
		if value < 0x21 || value > 0x7e {
			return "", errors.New("orch credential contains invalid characters")
		}
	}
	return token, nil
}
