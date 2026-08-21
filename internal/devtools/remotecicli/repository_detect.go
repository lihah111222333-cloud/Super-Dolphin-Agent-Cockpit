package remotecicli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const portableWorkloadManifestRelativePath = ".super-dolphin/remote-ci-workloads.json"

// DetectedRepository 是独立远程 CI 入口从当前 exact Git tree 取得的仓库身份。
type DetectedRepository struct {
	Root              string
	RepositoryID      string
	ObjectFormat      string
	CommitSHA         string
	TreeSHA           string
	ManifestPath      string
	ManifestSHA256    string
	ManifestABIDigest string
	Manifest          remoteci.RepositoryWorkloadManifest
}

// DetectRepository 自动解析 startPath 所属的唯一 clean Git 仓库，并严格绑定仓库自有 workload manifest。
// startPath 为空时只使用当前工作目录作为发现起点；仓库身份仍来自 Git 对象和 manifest，不来自目录名。
func DetectRepository(ctx context.Context, startPath string) (DetectedRepository, error) {
	if ctx == nil {
		return DetectedRepository{}, errors.New("repository detection context is required")
	}
	start, err := repositoryDetectionStartPath(startPath)
	if err != nil {
		return DetectedRepository{}, err
	}
	root, err := repositoryDetectionGitField(ctx, start, "--show-toplevel")
	if err != nil {
		return DetectedRepository{}, fmt.Errorf("detect repository root: %w", err)
	}
	if err := validateAbsolutePathComponents(root, true); err != nil {
		return DetectedRepository{}, fmt.Errorf("validate detected repository root: %w", err)
	}
	return detectRepositoryAtRoot(ctx, root)
}

// repositoryDetectionStartPath 规范化显式路径，或把当前目录作为唯一自动发现起点。
func repositoryDetectionStartPath(startPath string) (string, error) {
	if startPath == "" {
		var err error
		startPath, err = os.Getwd()
		if err != nil {
			return "", errors.New("current working directory is unavailable")
		}
	}
	absolute, err := filepath.Abs(startPath)
	if err != nil || filepath.Clean(absolute) != absolute {
		return "", errors.New("repository detection start path is invalid")
	}
	if err := validateAbsolutePathComponents(absolute, true); err != nil {
		return "", err
	}
	return absolute, nil
}

// detectRepositoryAtRoot 绑定 clean HEAD、对象格式和仓库自有 manifest。
func detectRepositoryAtRoot(ctx context.Context, root string) (DetectedRepository, error) {
	if err := validateRepositoryWorktreeClean(ctx, root); err != nil {
		return DetectedRepository{}, err
	}
	objectFormat, err := repositoryDetectionGitField(ctx, root, "--show-object-format")
	if err != nil || objectFormat != "sha1" && objectFormat != "sha256" {
		return DetectedRepository{}, errors.New("detected repository object format is invalid")
	}
	commit, err := repositoryDetectionGitField(ctx, root, "--verify", "HEAD^{commit}")
	if err != nil || !validDetectedObjectID(commit, objectFormat) {
		return DetectedRepository{}, errors.New("detected repository HEAD commit is invalid")
	}
	tree, err := repositoryDetectionGitField(ctx, root, "--verify", "HEAD^{tree}")
	if err != nil || !validDetectedObjectID(tree, objectFormat) {
		return DetectedRepository{}, errors.New("detected repository HEAD tree is invalid")
	}
	return bindDetectedRepositoryManifest(root, objectFormat, commit, tree)
}

func validateRepositoryWorktreeClean(ctx context.Context, root string) error {
	output, err := runBoundedGitObjectCommand(ctx, root, 1<<20, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return errors.New("inspect repository worktree status failed")
	}
	if len(output) != 0 {
		return errors.New("repository worktree must be clean before automatic CI routing")
	}
	return nil
}

func repositoryDetectionGitField(ctx context.Context, root string, args ...string) (string, error) {
	output, err := runBoundedGitObjectCommand(ctx, root, 4<<10, append([]string{"rev-parse"}, args...)...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(output), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("git identity output is invalid")
	}
	return value, nil
}

func bindDetectedRepositoryManifest(root, objectFormat, commit, tree string) (DetectedRepository, error) {
	manifestPath := filepath.Join(root, filepath.FromSlash(portableWorkloadManifestRelativePath))
	data, err := readRegularBoundedFile(manifestPath)
	if err != nil {
		return DetectedRepository{}, errors.New("read repository workload manifest failed")
	}
	manifest, err := remoteci.DecodeRepositoryWorkloadManifest(data)
	if err != nil {
		return DetectedRepository{}, fmt.Errorf("decode repository workload manifest: %w", err)
	}
	abiDigest, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		return DetectedRepository{}, errors.New("derive repository workload manifest ABI failed")
	}
	return DetectedRepository{
		Root: root, RepositoryID: manifest.RepositoryID, ObjectFormat: objectFormat,
		CommitSHA: commit, TreeSHA: tree, ManifestPath: manifestPath,
		ManifestSHA256: digestBytes(data), ManifestABIDigest: abiDigest, Manifest: manifest,
	}, nil
}

// validDetectedObjectID 按已验证 object format 检查 lowercase Git object ID。
func validDetectedObjectID(value, objectFormat string) bool {
	wantLength := 40
	if objectFormat == "sha256" {
		wantLength = 64
	}
	if len(value) != wantLength {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
