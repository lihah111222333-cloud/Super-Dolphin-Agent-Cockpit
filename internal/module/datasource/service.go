// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

var (
	// datasource 上传/删除的领域错误，RPC 层会映射为对应 jrpc2 错误码。
	errMissingSourcePath          = errors.New("datasource: sourcePath is required")
	errSourcePathMustBeAbsolute   = errors.New("datasource: sourcePath must be absolute")
	errSourcePathOutsideWorkspace = errors.New("datasource: sourcePath outside workspace")
	errSourcePathMustBeFile       = errors.New("datasource: sourcePath must be a file")
	errUnsupportedFileExtension   = errors.New("datasource: unsupported file extension")
	errUnsupportedTextEncoding    = errors.New("datasource: unsupported text encoding")
	errInvalidDatasourceFileName  = errors.New("datasource: fileName must be a file name")
	errDeleteTargetMustBeFile     = errors.New("datasource: delete target must be a file")
	errDatasourceContentEmpty     = errors.New("datasource: extracted content is empty")
	errDatasourceTextTooLarge     = errors.New("datasource: text is too large")
)

// Service 定义 datasource 模块的文件上传、列举、文档读取和删除接口。
// 所有文件路径都约束在当前 workspace 内，文档存储是可选增强能力。
type Service interface {
	UploadFile(context.Context, UploadFileRequest) (UploadFileResult, error)
	ListFiles(context.Context) (ListFilesResult, error)
	ListDocuments(context.Context, string) (ListDocumentsResult, error)
	DeleteFile(context.Context, DeleteFileRequest) (DeleteFileResult, error)
}

// UploadFileRequest 是文件上传请求，SourcePath 必须是当前 workspace 内的绝对路径。
type UploadFileRequest struct {
	SourcePath string `json:"sourcePath"`
}

// UploadFileResult 是上传成功后的 wire 响应，StoredPath 是 workspace 内目标路径。
type UploadFileResult struct {
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	Size       int64  `json:"size"`
	StoredPath string `json:"storedPath"`
}

// ListFilesResult 是 datasource/list 的 wire 响应，FileNames 始终为文件名而非路径。
type ListFilesResult struct {
	FileNames []string `json:"fileNames"`
}

// ListDocumentsResult 是 prompt 动态段读取已入库文档的响应。
type ListDocumentsResult struct {
	Documents []DatasourceDocument `json:"documents"`
}

// DeleteFileRequest 是删除上传文件的请求，只允许传文件名。
type DeleteFileRequest struct {
	FileName string `json:"fileName"`
}

// DeleteFileResult 是 datasource/delete 的 wire 响应。
type DeleteFileResult struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

// service 保存可选的 datasource 文档存储。
// store 为 nil 时仍支持上传目录的文件复制/列举/删除。
type service struct {
	documents DatasourceDocumentStore
}

// NewService 创建不带文档存储的 datasource 服务。
func NewService() Service {
	return NewServiceWithStore(nil)
}

// NewServiceWithStore 创建 datasource 服务。
// store 为 nil 时上传内容不会入库，但文件仍会复制到 workspace 上传目录。
func NewServiceWithStore(store DatasourceDocumentStore) Service {
	return &service{documents: store}
}

// datasourceContext 确保 context 非 nil，避免下游调用 panic。
func datasourceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// UploadFile 校验并复制 workspace 内的 datasource 文件。
// 文本抽取成功后才写文档存储；任何路径、编码或复制错误都会阻断上传。
func (s *service) UploadFile(ctx context.Context, req UploadFileRequest) (UploadFileResult, error) {
	ctx = datasourceContext(ctx)
	source, err := prepareUploadSource(ctx, req)
	if err != nil {
		return UploadFileResult{}, err
	}
	content, err := extractDatasourceText(ctx, source.path, source.extension)
	if err != nil {
		return UploadFileResult{}, err
	}
	workspaceRoot, targetPath, err := copyUploadIntoWorkspace(ctx, source.path)
	if err != nil {
		return UploadFileResult{}, err
	}
	if err := s.persistUploadedDocument(ctx, workspaceRoot, source, targetPath, content); err != nil {
		return UploadFileResult{}, err
	}
	return uploadFileResult(source, targetPath), nil
}

// uploadSource 保存已验证的上传文件元信息，供后续文本提取和入库使用。
type uploadSource struct {
	path      string
	name      string
	extension string
	size      int64
}

// prepareUploadSource 校验上传请求并读取源文件元信息。
func prepareUploadSource(ctx context.Context, req UploadFileRequest) (uploadSource, error) {
	sourcePath, err := validateUploadRequest(req)
	if err != nil {
		return uploadSource{}, err
	}
	if err := ctx.Err(); err != nil {
		return uploadSource{}, err
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return uploadSource{}, fmt.Errorf("stat source file: %w", err)
	}
	if sourceInfo.IsDir() {
		return uploadSource{}, errSourcePathMustBeFile
	}
	ext := strings.ToLower(filepath.Ext(sourcePath))
	if !isAllowedUploadExtension(ext) {
		return uploadSource{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, ext)
	}
	if sourceInfo.Size() > datasourceMaxImportBytes {
		return uploadSource{}, errDatasourceTextTooLarge
	}
	return uploadSource{
		path:      sourcePath,
		name:      filepath.Base(sourcePath),
		extension: ext,
		size:      sourceInfo.Size(),
	}, nil
}

// copyUploadIntoWorkspace 将源文件复制到当前 workspace 上传目录。
// 上传目录会按需创建，复制失败时不会返回半成品路径。
func copyUploadIntoWorkspace(ctx context.Context, sourcePath string) (string, string, error) {
	workspaceRoot, err := currentDatasourceWorkspaceRoot()
	if err != nil {
		return "", "", err
	}
	targetDir, err := ensureDatasourceUploadDir(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	targetPath := filepath.Join(targetDir, filepath.Base(sourcePath))
	if err := copyUploadFile(ctx, sourcePath, targetPath); err != nil {
		return "", "", err
	}
	return workspaceRoot, targetPath, nil
}

// persistUploadedDocument 将提取文本写入可选文档存储。
// documents store 为 nil 表示当前运行模式只管理上传文件，不持久化正文。
func (s *service) persistUploadedDocument(
	ctx context.Context,
	workspaceRoot string,
	source uploadSource,
	targetPath string,
	content string,
) error {
	if s.documents == nil {
		return nil
	}
	return s.documents.UpsertDocument(ctx, UpsertDatasourceDocumentParams{
		WorkspaceRoot: workspaceRoot,
		Name:          source.name,
		Extension:     source.extension,
		Size:          source.size,
		StoredPath:    targetPath,
		Content:       content,
	})
}

// uploadFileResult 将已验证源信息和目标路径组装为上传响应。
func uploadFileResult(source uploadSource, targetPath string) UploadFileResult {
	return UploadFileResult{
		Name:       source.name,
		Extension:  source.extension,
		Size:       source.size,
		StoredPath: targetPath,
	}
}

// ListFiles 列出当前 workspace 上传目录中的普通文件名。
// 目录项会被跳过并按名称排序，返回值不暴露绝对路径。
func (s *service) ListFiles(ctx context.Context) (ListFilesResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ListFilesResult{}, err
	}

	uploadDir, err := ensureCurrentDatasourceUploadDir()
	if err != nil {
		return ListFilesResult{}, err
	}
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		return ListFilesResult{}, fmt.Errorf("read datasource upload dir: %w", err)
	}

	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ListFilesResult{}, err
		}
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return ListFilesResult{}, fmt.Errorf("stat datasource upload entry %s: %w", entry.Name(), err)
		}
		if info.Mode().IsRegular() {
			fileNames = append(fileNames, entry.Name())
		}
	}
	sort.Strings(fileNames)
	return ListFilesResult{FileNames: fileNames}, nil
}

// ListDocuments 读取指定工作区已持久化的数据源文档。
func (s *service) ListDocuments(ctx context.Context, workspaceRoot string) (ListDocumentsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ListDocumentsResult{}, err
	}
	if s.documents == nil {
		return ListDocumentsResult{Documents: []DatasourceDocument{}}, nil
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		var err error
		workspaceRoot, err = currentDatasourceWorkspaceRoot()
		if err != nil {
			return ListDocumentsResult{}, err
		}
	} else {
		workspaceRoot = filepath.Clean(workspaceRoot)
	}
	documents, err := s.documents.ListDocuments(ctx, workspaceRoot)
	if err != nil {
		return ListDocumentsResult{}, err
	}
	return ListDocumentsResult{Documents: documents}, nil
}

// ensureCurrentDatasourceUploadDir 确保当前 workspace 的上传目录存在，不存在时自动创建。
func ensureCurrentDatasourceUploadDir() (string, error) {
	uploadDir, err := currentDatasourceUploadDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return "", fmt.Errorf("create datasource upload dir: %w", err)
	}
	return uploadDir, nil
}

// ensureDatasourceUploadDir 确保指定 workspace 下的上传目录存在。
func ensureDatasourceUploadDir(workspaceRoot string) (string, error) {
	uploadDir := datasourceUploadDir(workspaceRoot)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return "", fmt.Errorf("create datasource upload dir: %w", err)
	}
	return uploadDir, nil
}

// DeleteFile 删除当前 workspace 上传目录中的单个文件。
// 请求只能携带文件名；删除文档存储失败时整体返回错误，避免文件和索引状态分裂。
func (s *service) DeleteFile(ctx context.Context, req DeleteFileRequest) (DeleteFileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fileName, err := validateDeleteRequest(req)
	if err != nil {
		return DeleteFileResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeleteFileResult{}, err
	}

	workspaceRoot, err := currentDatasourceWorkspaceRoot()
	if err != nil {
		return DeleteFileResult{}, err
	}
	uploadDir := datasourceUploadDir(workspaceRoot)
	targetPath := filepath.Join(uploadDir, fileName)
	info, err := os.Stat(targetPath)
	if err != nil {
		return DeleteFileResult{}, fmt.Errorf("stat datasource file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return DeleteFileResult{}, errDeleteTargetMustBeFile
	}
	if err := os.Remove(targetPath); err != nil {
		return DeleteFileResult{}, fmt.Errorf("delete datasource file: %w", err)
	}
	if s.documents != nil {
		if err := s.documents.DeleteDocument(ctx, workspaceRoot, fileName); err != nil {
			return DeleteFileResult{}, err
		}
	}

	return DeleteFileResult{Name: fileName, Deleted: true}, nil
}

// validateUploadRequest 校验上传路径，返回清理后的绝对路径。
func validateUploadRequest(req UploadFileRequest) (string, error) {
	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath == "" {
		return "", errMissingSourcePath
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		return "", errSourcePathMustBeAbsolute
	}
	if err := ensureUploadSourceInsideWorkspace(sourcePath); err != nil {
		return "", err
	}
	return sourcePath, nil
}

// ensureUploadSourceInsideWorkspace 检查源文件路径是否在当前 workspace 范围内。
func ensureUploadSourceInsideWorkspace(sourcePath string) error {
	workspaceRoot, err := currentDatasourceWorkspaceRoot()
	if err != nil {
		return err
	}
	if !platformshared.ContainsPath(workspaceRoot, sourcePath) {
		return fmt.Errorf("%w: %s", errSourcePathOutsideWorkspace, sourcePath)
	}
	return nil
}

// validateDeleteRequest 校验删除请求只包含安全文件名。
// 绝对路径、上级目录、卷名和路径分隔符都会被拒绝。
func validateDeleteRequest(req DeleteFileRequest) (string, error) {
	fileName := strings.TrimSpace(req.FileName)
	if fileName == "" ||
		fileName == "." ||
		fileName == ".." ||
		filepath.IsAbs(fileName) ||
		filepath.VolumeName(fileName) != "" ||
		strings.ContainsAny(fileName, `/\`) {
		return "", errInvalidDatasourceFileName
	}
	return fileName, nil
}

// currentDatasourceUploadDir 返回当前 workspace 的上传目录绝对路径。
func currentDatasourceUploadDir() (string, error) {
	baseDir, err := currentDatasourceWorkspaceRoot()
	if err != nil {
		return "", err
	}
	return datasourceUploadDir(baseDir), nil
}

// currentDatasourceWorkspaceRoot 返回当前进程工作目录作为 workspace 根路径。
func currentDatasourceWorkspaceRoot() (string, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(baseDir), nil
}

// datasourceUploadDir 返回指定 workspace 下 datasource 上传目录的绝对路径。
func datasourceUploadDir(sourceDir string) string {
	return filepath.Join(sourceDir, ".agent", "datasources", "uploads")
}

// isAllowedUploadExtension 检查扩展名是否在 pdf 或文本白名单中。
func isAllowedUploadExtension(ext string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	return ext == ".pdf" || isTextUploadExtension(ext)
}

// copyUploadFile 通过临时文件复制上传内容。
// 只有 replaceUploadFile 成功后才保留临时文件，失败路径会清理临时文件。
func copyUploadFile(ctx context.Context, sourcePath, targetPath string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	tempPath, err := copySourceToUploadTemp(sourcePath, filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replaceUploadFile(tempPath, targetPath); err != nil {
		return err
	}
	committed = true
	return nil
}

// copySourceToUploadTemp 先把源文件复制到上传目录的临时文件。
func copySourceToUploadTemp(sourcePath, targetDir string) (tempPath string, err error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open source file: %w", err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close source file: %w", closeErr)
		}
		if err != nil && tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()

	target, err := os.CreateTemp(targetDir, ".upload-*")
	if err != nil {
		return "", fmt.Errorf("create upload temp file: %w", err)
	}
	tempPath = target.Name()

	if _, copyErr := io.Copy(target, source); copyErr != nil {
		_ = target.Close()
		err = fmt.Errorf("copy upload file: %w", copyErr)
		return
	}
	if closeErr := target.Close(); closeErr != nil {
		err = fmt.Errorf("close upload file: %w", closeErr)
		return
	}
	return tempPath, nil
}

// replaceUploadFile 用临时文件替换最终上传文件，兼容不能原子重命名的场景。
func replaceUploadFile(tempPath, targetPath string) error {
	if err := os.Rename(tempPath, targetPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("replace upload file: %w", err)
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("stat upload target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("replace upload file: target is not a regular file: %s", filepath.Base(targetPath))
	}
	if err := os.Remove(targetPath); err != nil {
		return fmt.Errorf("remove existing upload file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("replace upload file: %w", err)
	}
	return nil
}
