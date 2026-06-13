package datasource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	errMissingCWD                = errors.New("datasource: cwd is required")
	errMissingSourcePath         = errors.New("datasource: sourcePath is required")
	errCWDMustBeAbsolute         = errors.New("datasource: cwd must be absolute")
	errCWDMustBeDirectory        = errors.New("datasource: cwd must be a directory")
	errSourcePathMustBeAbsolute  = errors.New("datasource: sourcePath must be absolute")
	errSourcePathMustBeFile      = errors.New("datasource: sourcePath must be a file")
	errUnsupportedFileExtension  = errors.New("datasource: unsupported file extension")
	errUploadTargetAlreadyExists = errors.New("datasource: upload target already exists")
)

type Service interface {
	UploadFile(context.Context, UploadFileRequest) (UploadFileResult, error)
}

type UploadFileRequest struct {
	CWD        string `json:"cwd"`
	SourcePath string `json:"sourcePath"`
}

type UploadFileResult struct {
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	Size       int64  `json:"size"`
	StoredPath string `json:"storedPath"`
}

type service struct{}

func NewService() Service {
	return &service{}
}

func (s *service) UploadFile(ctx context.Context, req UploadFileRequest) (UploadFileResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	projectRoot, sourcePath, err := validateUploadRequest(req)
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

	targetDir := datasourceUploadDir(projectRoot)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return UploadFileResult{}, fmt.Errorf("create datasource upload dir: %w", err)
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

func validateUploadRequest(req UploadFileRequest) (string, string, error) {
	projectRoot := strings.TrimSpace(req.CWD)
	if projectRoot == "" {
		return "", "", errMissingCWD
	}
	projectRoot = filepath.Clean(projectRoot)
	if !filepath.IsAbs(projectRoot) {
		return "", "", errCWDMustBeAbsolute
	}
	projectInfo, err := os.Stat(projectRoot)
	if err != nil {
		return "", "", fmt.Errorf("stat cwd: %w", err)
	}
	if !projectInfo.IsDir() {
		return "", "", errCWDMustBeDirectory
	}

	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath == "" {
		return "", "", errMissingSourcePath
	}
	sourcePath = filepath.Clean(sourcePath)
	if !filepath.IsAbs(sourcePath) {
		return "", "", errSourcePathMustBeAbsolute
	}
	return projectRoot, sourcePath, nil
}

func datasourceUploadDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".agent", "datasources", "uploads")
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
