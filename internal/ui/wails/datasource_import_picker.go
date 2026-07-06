package wails

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const datasourceImportPickerTokenTTL = 10 * time.Minute

type datasourceImportFileSelection struct {
	SourcePath  string `json:"sourcePath"`
	PickerToken string `json:"pickerToken"`
}

type datasourceImportPickerRecord struct {
	SourcePath string
	ExpiresAt  time.Time
}

type datasourceImportPickerTokens struct {
	mu     sync.Mutex
	now    func() time.Time
	tokens map[string]datasourceImportPickerRecord
}

// newDatasourceImportPickerTokens 创建 datasource 导入文件选择器的短期 capability 注册表。
func newDatasourceImportPickerTokens(now func() time.Time) *datasourceImportPickerTokens {
	if now == nil {
		now = time.Now
	}
	return &datasourceImportPickerTokens{
		now:    now,
		tokens: make(map[string]datasourceImportPickerRecord),
	}
}

// selectDatasourceImportFile 打开 datasource 专用单文件选择器，并为选中的本地路径签发一次性 token。
// 取消选择返回空结果；选择器返回异常路径或 token 签发失败时立即报错。
func (a *App) selectDatasourceImportFile(defaultPath string, filters []selectFileFilter) (datasourceImportFileSelection, error) {
	sourcePath, err := a.selectSingleFileWithFilters(defaultPath, filters)
	if err != nil {
		return datasourceImportFileSelection{}, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return datasourceImportFileSelection{}, nil
	}
	if a == nil || a.datasourceImportPickerTokens == nil {
		return datasourceImportFileSelection{}, errors.New("datasource import picker: token issuer is not configured")
	}
	cleanSourcePath, token, err := a.datasourceImportPickerTokens.mint(sourcePath)
	if err != nil {
		return datasourceImportFileSelection{}, err
	}
	return datasourceImportFileSelection{SourcePath: cleanSourcePath, PickerToken: token}, nil
}

// VerifyDatasourceImportPickerToken 验证 datasource 导入请求是否使用了桌面文件选择器签发的 token。
// token 验证成功或命中过期记录都会消费对应记录，避免同一 capability 被重复使用。
func (a *App) VerifyDatasourceImportPickerToken(sourcePath, token string) bool {
	if a == nil || a.datasourceImportPickerTokens == nil {
		return false
	}
	return a.datasourceImportPickerTokens.verify(sourcePath, token)
}

func (r *datasourceImportPickerTokens) mint(sourcePath string) (string, string, error) {
	if r == nil {
		return "", "", errors.New("datasource import picker: token issuer is not configured")
	}
	cleanSourcePath, err := cleanDatasourceImportPickerSourcePath(sourcePath)
	if err != nil {
		return "", "", err
	}
	token, err := newDatasourceImportPickerToken()
	if err != nil {
		return "", "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	r.tokens[token] = datasourceImportPickerRecord{
		SourcePath: cleanSourcePath,
		ExpiresAt:  now.Add(datasourceImportPickerTokenTTL),
	}
	return cleanSourcePath, token, nil
}

// verify 校验 token 是否仍有效且绑定到同一个清理后的绝对路径。
// 命中 token 后立即删除记录，确保 datasource 本地导入 capability 不可重放。
func (r *datasourceImportPickerTokens) verify(sourcePath, token string) bool {
	if r == nil {
		return false
	}
	cleanSourcePath, err := cleanDatasourceImportPickerSourcePath(sourcePath)
	if err != nil {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.pruneLocked(now)
	record, ok := r.tokens[token]
	if !ok {
		return false
	}
	delete(r.tokens, token)
	if now.After(record.ExpiresAt) {
		return false
	}
	return record.SourcePath == cleanSourcePath
}

func (r *datasourceImportPickerTokens) pruneLocked(now time.Time) {
	for token, record := range r.tokens {
		if now.After(record.ExpiresAt) {
			delete(r.tokens, token)
		}
	}
}

func cleanDatasourceImportPickerSourcePath(sourcePath string) (string, error) {
	cleanSourcePath := strings.TrimSpace(sourcePath)
	if cleanSourcePath == "" {
		return "", errors.New("datasource import picker: source path is required")
	}
	if strings.ContainsRune(cleanSourcePath, '\x00') {
		return "", errors.New("datasource import picker: source path contains null byte")
	}
	cleanSourcePath = filepath.Clean(cleanSourcePath)
	if !filepath.IsAbs(cleanSourcePath) {
		return "", errors.New("datasource import picker: source path must be absolute")
	}
	return cleanSourcePath, nil
}

func newDatasourceImportPickerToken() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("datasource import picker: create token: %w", err)
	}
	return "dsimp_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
