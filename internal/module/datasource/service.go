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
)

var (
	errMissingSourcePath         = errors.New("datasource: sourcePath is required")
	errSourcePathMustBeAbsolute  = errors.New("datasource: sourcePath must be absolute")
	errSourcePathMustBeFile      = errors.New("datasource: sourcePath must be a file")
	errUnsupportedFileExtension  = errors.New("datasource: unsupported file extension")
	errInvalidDatasourceFileName = errors.New("datasource: fileName must be a file name")
	errDeleteTargetMustBeFile    = errors.New("datasource: delete target must be a file")
	errDatasourceContentEmpty    = errors.New("datasource: extracted content is empty")
)

type Service interface {
	UploadFile(context.Context, UploadFileRequest) (UploadFileResult, error)
	ListFiles(context.Context) (ListFilesResult, error)
	ListDocuments(context.Context, string) (ListDocumentsResult, error)
	DeleteFile(context.Context, DeleteFileRequest) (DeleteFileResult, error)
}

type UploadFileRequest struct {
	SourcePath string `json:"sourcePath"`
}

type UploadFileResult struct {
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	Size       int64  `json:"size"`
	StoredPath string `json:"storedPath"`
}

type ListFilesResult struct {
	FileNames []string `json:"fileNames"`
}

type ListDocumentsResult struct {
	Documents []DatasourceDocument `json:"documents"`
}

type DeleteFileRequest struct {
	FileName string `json:"fileName"`
}

type DeleteFileResult struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type service struct {
	documents DatasourceDocumentStore
}

// NewService 创建服务。
func NewService() Service {
	return NewServiceWithStore(nil)
}

// NewServiceWithStore 创建带文档存储的 datasource 服务。
func NewServiceWithStore(store DatasourceDocumentStore) Service {
	return &service{documents: store}
}

func datasourceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// UploadFile 处理upload文件。
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
	return uploadSource{
		path:      sourcePath,
		name:      filepath.Base(sourcePath),
		extension: ext,
		size:      sourceInfo.Size(),
	}, nil
}

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

func uploadFileResult(source uploadSource, targetPath string) UploadFileResult {
	return UploadFileResult{
		Name:       source.name,
		Extension:  source.extension,
		Size:       source.size,
		StoredPath: targetPath,
	}
}

// ListFiles 列出文件。
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

func ensureDatasourceUploadDir(workspaceRoot string) (string, error) {
	uploadDir := datasourceUploadDir(workspaceRoot)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return "", fmt.Errorf("create datasource upload dir: %w", err)
	}
	return uploadDir, nil
}

// DeleteFile 删除文件。
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

func validateUploadRequest(req UploadFileRequest) (string, error) {
	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath == "" {
		return "", errMissingSourcePath
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		return "", errSourcePathMustBeAbsolute
	}
	return sourcePath, nil
}

// validateDeleteRequest 校验delete请求。
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

func currentDatasourceUploadDir() (string, error) {
	baseDir, err := currentDatasourceWorkspaceRoot()
	if err != nil {
		return "", err
	}
	return datasourceUploadDir(baseDir), nil
}

func currentDatasourceWorkspaceRoot() (string, error) {
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(baseDir), nil
}

func datasourceUploadDir(sourceDir string) string {
	return filepath.Join(sourceDir, ".agent", "datasources", "uploads")
}

func isAllowedUploadExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".pdf", ".txt":
		return true
	default:
		return false
	}
}

// copyUploadFile 复制upload文件。
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
