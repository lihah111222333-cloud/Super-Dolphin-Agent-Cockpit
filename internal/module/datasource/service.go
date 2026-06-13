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
	errUploadTargetAlreadyExists = errors.New("datasource: upload target already exists")
	errInvalidDatasourceFileName = errors.New("datasource: fileName must be a file name")
	errDeleteTargetMustBeFile    = errors.New("datasource: delete target must be a file")
)

type Service interface {
	UploadFile(context.Context, UploadFileRequest) (UploadFileResult, error)
	ListFiles(context.Context) (ListFilesResult, error)
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

type DeleteFileRequest struct {
	FileName string `json:"fileName"`
}

type DeleteFileResult struct {
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type service struct{}

func NewService() Service {
	return &service{}
}

func (s *service) UploadFile(ctx context.Context, req UploadFileRequest) (UploadFileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sourcePath, err := validateUploadRequest(req)
	if err != nil {
		return UploadFileResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return UploadFileResult{}, err
	}

	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return UploadFileResult{}, fmt.Errorf("stat source file: %w", err)
	}
	if sourceInfo.IsDir() {
		return UploadFileResult{}, errSourcePathMustBeFile
	}

	ext := strings.ToLower(filepath.Ext(sourcePath))
	if !isAllowedUploadExtension(ext) {
		return UploadFileResult{}, fmt.Errorf("%w: %s", errUnsupportedFileExtension, ext)
	}

	targetDir, err := ensureCurrentDatasourceUploadDir()
	if err != nil {
		return UploadFileResult{}, err
	}
	targetPath := filepath.Join(targetDir, filepath.Base(sourcePath))
	if err := copyUploadFile(ctx, sourcePath, targetPath); err != nil {
		return UploadFileResult{}, err
	}

	return UploadFileResult{
		Name:       filepath.Base(sourcePath),
		Extension:  ext,
		Size:       sourceInfo.Size(),
		StoredPath: targetPath,
	}, nil
}

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

	uploadDir, err := currentDatasourceUploadDir()
	if err != nil {
		return DeleteFileResult{}, err
	}
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
	baseDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return datasourceUploadDir(baseDir), nil
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

func copyUploadFile(ctx context.Context, sourcePath, targetPath string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close source file: %w", closeErr)
		}
	}()

	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", errUploadTargetAlreadyExists, filepath.Base(targetPath))
		}
		return fmt.Errorf("create upload file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(targetPath)
		}
	}()

	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return fmt.Errorf("copy upload file: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("close upload file: %w", err)
	}
	committed = true
	return nil
}
