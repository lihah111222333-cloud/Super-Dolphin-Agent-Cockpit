package contentkeychain

import (
	"errors"
	"os"
	"path/filepath"
)

// openPinned 同时打开明确文件和父目录，后续由checkIdentity确认二者未换绑。
func openPinned(config Config, info os.FileInfo) (*Provider, error) {
	directoryInfo, err := os.Lstat(filepath.Dir(config.KeychainPath))
	if err != nil {
		return nil, ErrUnsafePath
	}
	directory, err := os.Open(filepath.Dir(config.KeychainPath))
	if err != nil {
		return nil, ErrUnsafePath
	}
	file, err := os.Open(config.KeychainPath)
	if err != nil {
		return nil, errors.Join(ErrUnsafePath, closePrivateFile(directory))
	}
	p := &Provider{config: config, file: file, identity: info, directory: directory, directoryIdentity: directoryInfo}
	return p, nil
}

// closePrivateFile 脱敏关闭错误，不将路径或系统描述暴露给调用方。
func closePrivateFile(file *os.File) error {
	if file != nil {
		if err := file.Close(); err != nil {
			return ErrUnsafePath
		}
	}
	return nil
}

// checkDirectory 复验构造时打开的私有父目录，而非只验证同名新目录的权限。
func (p *Provider) checkDirectory() error {
	if p.directory == nil || p.directoryIdentity == nil {
		return ErrUnsafePath
	}
	path := filepath.Dir(p.config.KeychainPath)
	if !canonicalPath(path) {
		return ErrUnsafePath
	}
	current, err := os.Lstat(path)
	if err != nil || !current.IsDir() || current.Mode().Perm()&0077 != 0 {
		return ErrUnsafePath
	}
	opened, err := p.directory.Stat()
	if err != nil || !os.SameFile(current, p.directoryIdentity) || !os.SameFile(current, opened) {
		return ErrUnsafePath
	}
	return nil
}

// refreshIdentity 仅由已完成Add/duplicate及完整readback验证的成功lease分支调用。
func (p *Provider) refreshIdentity() error {
	if err := p.checkDirectory(); err != nil {
		return err
	}
	if err := p.store.checkPrivate(); err != nil {
		return err
	}
	info, err := privatePath(p.config.KeychainPath)
	if err != nil {
		return err
	}
	if os.SameFile(info, p.identity) {
		return p.checkIdentity()
	}
	candidate, err := os.Open(p.config.KeychainPath)
	if err != nil {
		return ErrUnsafePath
	}
	if err = p.validateReplacement(candidate, info); err != nil {
		return errors.Join(err, closePrivateFile(candidate))
	}
	if err = closePrivateFile(p.file); err != nil {
		// os.File.Close使旧对象失效；不发布候选，也不保留可自动重试的可用状态。
		p.file = nil
		return errors.Join(err, closePrivateFile(candidate))
	}
	p.file, p.identity = candidate, info
	return nil
}

// validateReplacement 在发布候选fd前同时验证路径、父目录与必需native私有性。
func (p *Provider) validateReplacement(candidate *os.File, info os.FileInfo) error {
	if err := p.matchesIdentity(candidate, info); err != nil {
		return err
	}
	if err := p.store.checkPrivate(); err != nil {
		return err
	}
	if err := p.matchesIdentity(candidate, info); err != nil {
		return err
	}
	return p.checkDirectory()
}
