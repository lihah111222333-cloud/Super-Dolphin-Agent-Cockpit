package appupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// stageBoundary 将更新 stage 固定到持有的目录根，所有子项均通过该根访问。
type stageBoundary struct {
	root *os.Root
	path string
}

// openStageBoundary 使用逐段 no-follow 根句柄打开或创建 stage，并拒绝其中已有的 alias。
func openStageBoundary(path string) (*stageBoundary, error) {
	root, err := openNoFollowStageRoot(path)
	if err != nil {
		return nil, err
	}
	stage := &stageBoundary{root: root, path: path}
	if err := stage.rejectAliases(); err != nil {
		return nil, errors.Join(err, stage.Close())
	}
	return stage, nil
}

// Close 释放 stage 根句柄，后续操作不得再使用该边界。
func (stage *stageBoundary) Close() error {
	if stage == nil || stage.root == nil {
		return nil
	}
	root := stage.root
	stage.root = nil
	return root.Close()
}

// openNoFollowStageRoot 从文件系统根逐段验证目录身份，避免路径字符串检查后的链接替换。
func openNoFollowStageRoot(path string) (*os.Root, error) {
	rootPath, components, err := stageRootComponents(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open app update filesystem root %q: %w", rootPath, err)
	}
	for _, component := range components {
		child, childErr := openNoFollowStageChild(root, component)
		if childErr != nil {
			return nil, errors.Join(childErr, root.Close())
		}
		if closeErr := root.Close(); closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("close app update stage parent %q: %w", root.Name(), closeErr),
				child.Close(),
			)
		}
		root = child
	}
	return root, nil
}

// stageRootComponents 将 clean absolute stage 路径拆成可通过持有根安全打开的目录段。
func stageRootComponents(path string) (string, []string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", nil, fmt.Errorf("app update stage path must be clean absolute: %q", path)
	}
	rootPath := string(os.PathSeparator)
	if volume := filepath.VolumeName(path); volume != "" {
		rootPath = volume + string(os.PathSeparator)
	}
	relative, err := filepath.Rel(rootPath, path)
	if err != nil {
		return "", nil, fmt.Errorf("resolve app update stage relative path %q: %w", path, err)
	}
	if relative == "." {
		return "", nil, fmt.Errorf("app update stage path must not be filesystem root: %q", path)
	}
	components := strings.Split(relative, string(os.PathSeparator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return "", nil, fmt.Errorf("app update stage path has invalid component %q", component)
		}
	}
	return rootPath, components, nil
}

// openNoFollowStageChild 打开或创建直接子目录，并校验打开前后的同一文件身份。
func openNoFollowStageChild(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create app update stage component %q: %w", name, err)
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect app update stage component %q: %w", name, err)
	}
	if err := requireStageDirectory(name, info); err != nil {
		return nil, err
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open app update stage component %q: %w", name, err)
	}
	opened, err := stageRootInfo(child)
	if err != nil {
		return nil, errors.Join(err, child.Close())
	}
	if !os.SameFile(info, opened) {
		return nil, errors.Join(
			fmt.Errorf("app update stage component %q changed while opening (possible alias)", name),
			child.Close(),
		)
	}
	return child, nil
}

// stageRootInfo 读取已经打开的目录根身份，用于与 no-follow 的 Lstat 结果比对。
func stageRootInfo(root *os.Root) (os.FileInfo, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open app update stage root identity: %w", err)
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return nil, errors.Join(fmt.Errorf("inspect app update stage root identity: %w", statErr), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close app update stage root identity: %w", closeErr)
	}
	return info, nil
}

// requireStageDirectory 拒绝 stage 路径段中的 symlink、reparse point 和非目录。
func requireStageDirectory(name string, info os.FileInfo) error {
	if isStageAlias(info) {
		return fmt.Errorf("app update stage component %q is an alias or reparse point", name)
	}
	if !info.IsDir() {
		return fmt.Errorf("app update stage component %q must be a directory", name)
	}
	return nil
}

// rejectAliases 扫描现有 stage 条目，在网络或外部 helper 前拒绝预置 alias。
func (stage *stageBoundary) rejectAliases() error {
	directory, err := stage.root.Open(".")
	if err != nil {
		return fmt.Errorf("open app update stage for alias scan: %w", err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return errors.Join(fmt.Errorf("read app update stage entries: %w", readErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close app update stage alias scan: %w", closeErr)
	}
	for _, entry := range entries {
		info, err := stage.root.Lstat(entry.Name())
		if err != nil {
			return fmt.Errorf("inspect app update stage entry %q: %w", entry.Name(), err)
		}
		if isStageAlias(info) {
			return fmt.Errorf("app update stage entry %q is an alias or reparse point", entry.Name())
		}
	}
	return nil
}

// isStageAlias 统一识别会改变路径解析语义的链接、reparse point 和特殊文件。
func isStageAlias(info os.FileInfo) bool {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 || mode&os.ModeIrregular != 0 {
		return true
	}
	return !info.IsDir() && !mode.IsRegular()
}

// stageFileName 要求传入路径正好是当前 stage 根下的单个文件。
func (stage *stageBoundary) stageFileName(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != stage.path {
		return "", fmt.Errorf("app update stage file is outside stage boundary: %q", path)
	}
	name := filepath.Base(path)
	if name == "." || name == ".." || name == "" || strings.ContainsAny(name, `/\\`) {
		return "", fmt.Errorf("app update stage file name is invalid: %q", name)
	}
	return name, nil
}

// readFile 通过 stage 根读取既有常规文件，不跟随 alias。
func (stage *stageBoundary) readFile(name string) ([]byte, error) {
	file, err := stage.openExistingRegular(name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.Join(fmt.Errorf("read app update stage file %q: %w", name, readErr), closeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close app update stage file %q: %w", name, closeErr)
	}
	return raw, nil
}

// openWriteFile 打开常规 stage 文件的安全描述符；截断只在身份复核完成后发生。
func (stage *stageBoundary) openWriteFile(name string, appendOnly bool) (*os.File, error) {
	if _, err := stage.stageFileName(filepath.Join(stage.path, name)); err != nil {
		return nil, err
	}
	file, err := stage.openOrCreateRegular(name)
	if err != nil {
		return nil, err
	}
	if err := prepareStageWriteFile(file, appendOnly); err != nil {
		return nil, errors.Join(fmt.Errorf("prepare app update stage file %q: %w", name, err), file.Close())
	}
	return file, nil
}

// openOrCreateRegular 按 Lstat 的结果安全打开既有常规文件或用 O_EXCL 创建新文件。
func (stage *stageBoundary) openOrCreateRegular(name string) (*os.File, error) {
	_, err := stage.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return stage.createRegular(name)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect app update stage file %q: %w", name, err)
	}
	return stage.openExistingRegular(name, os.O_WRONLY)
}

// createRegular 通过持有的 stage 根独占创建常规文件，并复核创建后的目录项身份。
func (stage *stageBoundary) createRegular(name string) (*os.File, error) {
	file, err := stage.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create app update stage file %q: %w", name, err)
	}
	if err := stage.verifyCreatedRegular(name, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// verifyCreatedRegular 检查新建文件及其当前 stage 目录项仍是同一个常规文件。
func (stage *stageBoundary) verifyCreatedRegular(name string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect created app update stage file %q: %w", name, err)
	}
	return stage.verifyOpenRegular(name, info, file)
}

// prepareStageWriteFile 在身份校验之后定位或截断已打开的 stage 文件。
func prepareStageWriteFile(file *os.File, appendOnly bool) error {
	if appendOnly {
		_, err := file.Seek(0, io.SeekEnd)
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err := file.Seek(0, io.SeekStart)
	return err
}

// openExistingRegular 用 Lstat、持有根和文件身份三重校验打开既有常规文件。
func (stage *stageBoundary) openExistingRegular(name string, flags int) (*os.File, error) {
	if _, err := stage.stageFileName(filepath.Join(stage.path, name)); err != nil {
		return nil, err
	}
	expected, err := stage.root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect app update stage file %q: %w", name, err)
	}
	if err := requireRegularStageFile(name, expected); err != nil {
		return nil, err
	}
	file, err := stage.root.OpenFile(name, flags, 0)
	if err != nil {
		return nil, fmt.Errorf("open app update stage file %q: %w", name, err)
	}
	if err := stage.verifyOpenRegular(name, expected, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// verifyOpenRegular 确认文件在打开前后及当前目录项均为同一常规文件。
func (stage *stageBoundary) verifyOpenRegular(name string, expected os.FileInfo, file *os.File) error {
	if err := requireRegularStageFile(name, expected); err != nil {
		return err
	}
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened app update stage file %q: %w", name, err)
	}
	current, err := stage.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("reinspect app update stage file %q: %w", name, err)
	}
	if err := requireRegularStageFile(name, current); err != nil {
		return err
	}
	if !os.SameFile(expected, opened) || !os.SameFile(current, opened) {
		return fmt.Errorf("app update stage file %q changed while opening (possible alias)", name)
	}
	return nil
}

// requireRegularStageFile 拒绝 stage 中的 alias、reparse point、目录和其他特殊文件。
func requireRegularStageFile(name string, info os.FileInfo) error {
	if isStageAlias(info) {
		return fmt.Errorf("app update stage file %q is an alias or reparse point", name)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("app update stage file %q must be a regular file", name)
	}
	return nil
}

// writeFile 通过已校验的文件描述符覆盖写入 stage 文件。
func (stage *stageBoundary) writeFile(name string, raw []byte) error {
	file, err := stage.openWriteFile(name, false)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil {
		return errors.Join(fmt.Errorf("write app update stage file %q: %w", name, writeErr), closeErr)
	}
	if written != len(raw) {
		return errors.Join(fmt.Errorf("write app update stage file %q: %w", name, io.ErrShortWrite), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close app update stage file %q: %w", name, closeErr)
	}
	return nil
}

// writeSelectedUpdate 将已选更新信息序列化写入 stage 内的 manifest 文件。
func writeSelectedUpdate(stage *stageBoundary, staged selectedUpdate) error {
	raw, err := json.MarshalIndent(staged, "", "  ")
	if err != nil {
		return fmt.Errorf("encode selected app update: %w", err)
	}
	raw = append(raw, '\n')
	if err := stage.writeFile(selectedUpdateFilename, raw); err != nil {
		return fmt.Errorf("write selected app update: %w", err)
	}
	return nil
}

// readSelectedUpdate 从 stage 内的 manifest 文件反序列化已选更新信息。
func readSelectedUpdate(stage *stageBoundary) (selectedUpdate, error) {
	raw, err := stage.readFile(selectedUpdateFilename)
	if err != nil {
		return selectedUpdate{}, fmt.Errorf("read selected app update: %w", err)
	}
	var staged selectedUpdate
	if err := json.Unmarshal(raw, &staged); err != nil {
		return selectedUpdate{}, fmt.Errorf("decode selected app update: %w", err)
	}
	return staged, nil
}

// validateStagedUpdate 校验 stage 内产物的元数据和 SHA-256，拒绝越界或 alias 路径。
func validateStagedUpdate(stage *stageBoundary, staged selectedUpdate) error {
	artifactPath := selectedArtifactPath(staged)
	if strings.TrimSpace(artifactPath) == "" {
		return errors.New("selected app update artifact_path is required")
	}
	if err := validateArtifact(staged.Artifact); err != nil {
		return err
	}
	artifactName, err := stage.stageFileName(artifactPath)
	if err != nil {
		return err
	}
	file, err := stage.openExistingRegular(artifactName, os.O_RDONLY)
	if err != nil {
		return fmt.Errorf("open selected app update artifact: %w", err)
	}
	return verifyStagedArtifactSHA256File(file, staged.Artifact.SHA256)
}

// verifyStagedArtifactSHA256File 通过已验证的 stage 文件描述符重新计算 SHA-256。
func verifyStagedArtifactSHA256File(file *os.File, want string) error {
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return errors.Join(fmt.Errorf("hash staged app update artifact: %w", copyErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged app update artifact: %w", closeErr)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%w: staged app update artifact sha256 = %s, want %s", contract.ErrUpdateIntegrityInvalid, got, want)
	}
	return nil
}

// commitFile 原子移动已校验临时文件；仅可替换已验证的常规 stage 文件。
func (stage *stageBoundary) commitFile(tempName, finalName string) error {
	tempInfo, err := stage.root.Lstat(tempName)
	if err != nil {
		return fmt.Errorf("inspect app update temporary file %q: %w", tempName, err)
	}
	if err := requireRegularStageFile(tempName, tempInfo); err != nil {
		return err
	}
	if finalInfo, err := stage.root.Lstat(finalName); err == nil {
		if err := requireRegularStageFile(finalName, finalInfo); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect app update stage file %q: %w", finalName, err)
	}
	if err := stage.root.Rename(tempName, finalName); err != nil {
		return fmt.Errorf("commit app update stage file %q: %w", finalName, err)
	}
	committed, err := stage.root.Lstat(finalName)
	if err != nil {
		return fmt.Errorf("inspect committed app update stage file %q: %w", finalName, err)
	}
	if err := requireRegularStageFile(finalName, committed); err != nil {
		return err
	}
	if !os.SameFile(tempInfo, committed) {
		return fmt.Errorf("app update stage file %q changed while committing (possible alias)", finalName)
	}
	return nil
}
