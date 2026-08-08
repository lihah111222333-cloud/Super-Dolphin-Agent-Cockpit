package eci

import (
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
)

const (
	// ConfigFileVolumeType 是 ECI 小型只读投影使用的卷类型。
	ConfigFileVolumeType = "ConfigFileVolume"
	// ConfigFileVolumeSafeMode 是控制文件投影唯一允许的权限。
	ConfigFileVolumeSafeMode = 0o555
	// ConfigFileVolumeMaxFileBytes 是 ECI 规定的单个解码内容上限。
	ConfigFileVolumeMaxFileBytes = 32 * 1024
	// ConfigFileVolumeMaxTotalBytes 是 ECI 规定的解码内容总上限。
	ConfigFileVolumeMaxTotalBytes = 60 * 1024
	// ConfigFileVolumeMaxVolumesPerGroup 是 ECI 每个实例不可调整的总卷数上限。
	ConfigFileVolumeMaxVolumesPerGroup = 20
	// ConfigFileVolumeMaxFilesPerGroup 限制重复 CLI 参数数量；结合内容和路径上限，
	// 使投影序列化保持在受控预算内。
	ConfigFileVolumeMaxFilesPerGroup = 64
)

// ConfigFileVolume 描述一个 ECI ConfigFileVolume 投影。
// 投影与分片本地三个 EmptyDir 数据卷严格分离。
type ConfigFileVolume struct {
	Name             string
	DefaultMode      int
	ConfigFileToPath []ConfigFileToPath
}

// ConfigFileToPath 描述相对于 ConfigFileVolume 挂载目录的一个文件。
// Content 在内存中保持解码内容，仅在阿里云 API 边界编码。
type ConfigFileToPath struct {
	Path    string
	Content string
	Mode    int
}

// validateConfigFileVolumes 校验 ECI 内容上限，并阻止投影变成数据或缓存路径。
func validateConfigFileVolumes(volumes []ConfigFileVolume, emptyDirNames []string) error {
	if len(emptyDirNames)+len(volumes) > ConfigFileVolumeMaxVolumesPerGroup {
		return fmt.Errorf("ECI container group volumes exceed fixed limit %d", ConfigFileVolumeMaxVolumesPerGroup)
	}
	seen := make(map[string]struct{}, len(emptyDirNames)+len(volumes))
	for _, name := range emptyDirNames {
		seen[name] = struct{}{}
	}
	totalBytes := 0
	totalFiles := 0
	for index, volume := range volumes {
		totalFiles += len(volume.ConfigFileToPath)
		if totalFiles > ConfigFileVolumeMaxFilesPerGroup {
			return fmt.Errorf("ECI ConfigFileVolume files exceed local limit %d", ConfigFileVolumeMaxFilesPerGroup)
		}
		contentBytes, err := validateConfigFileVolume(volume, index, seen)
		if err != nil {
			return err
		}
		totalBytes += contentBytes
		if totalBytes > ConfigFileVolumeMaxTotalBytes {
			return fmt.Errorf("ECI ConfigFileVolume content exceeds %d bytes in total", ConfigFileVolumeMaxTotalBytes)
		}
	}
	return nil
}

func validateConfigFileVolume(volume ConfigFileVolume, index int, seen map[string]struct{}) (int, error) {
	if err := validateConfigFileVolumeIdentity(volume, index, seen); err != nil {
		return 0, err
	}
	if len(volume.ConfigFileToPath) == 0 {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q must declare at least one file", volume.Name)
	}
	paths := make(map[string]struct{}, len(volume.ConfigFileToPath))
	totalBytes := 0
	for fileIndex, file := range volume.ConfigFileToPath {
		contentBytes, err := validateConfigFileToPath(volume.Name, fileIndex, file, paths)
		if err != nil {
			return 0, err
		}
		totalBytes += contentBytes
	}
	return totalBytes, nil
}

func validateConfigFileVolumeIdentity(volume ConfigFileVolume, index int, seen map[string]struct{}) error {
	if !eciNamePattern.MatchString(volume.Name) {
		return fmt.Errorf("ECI ConfigFileVolume %d name is invalid", index+1)
	}
	if _, exists := seen[volume.Name]; exists {
		return fmt.Errorf("ECI ConfigFileVolume %q duplicates another volume", volume.Name)
	}
	if hasProjectionForbiddenToken(volume.Name) {
		return fmt.Errorf("ECI ConfigFileVolume %q is reserved for credentials, dependencies, or caches", volume.Name)
	}
	seen[volume.Name] = struct{}{}
	if volume.DefaultMode != ConfigFileVolumeSafeMode {
		return fmt.Errorf("ECI ConfigFileVolume %q DefaultMode must be %s", volume.Name, formatConfigFileMode(ConfigFileVolumeSafeMode))
	}
	return nil
}

func validateConfigFileToPath(volumeName string, index int, file ConfigFileToPath, paths map[string]struct{}) (int, error) {
	if err := validateConfigFilePath(file.Path); err != nil {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file %d: %w", volumeName, index+1, err)
	}
	if _, exists := paths[file.Path]; exists {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file path %q is duplicated", volumeName, file.Path)
	}
	paths[file.Path] = struct{}{}
	if hasProjectionForbiddenToken(file.Path) {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file path %q is reserved for credentials, dependencies, or caches", volumeName, file.Path)
	}
	if hasProjectionCredentialMarker(file.Content) {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file %q contains a credential marker", volumeName, file.Path)
	}
	contentBytes := len([]byte(file.Content))
	if contentBytes > ConfigFileVolumeMaxFileBytes {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file %q content exceeds %d bytes", volumeName, file.Path, ConfigFileVolumeMaxFileBytes)
	}
	if file.Mode != 0 && file.Mode != ConfigFileVolumeSafeMode {
		return 0, fmt.Errorf("ECI ConfigFileVolume %q file %q Mode must be %s", volumeName, file.Path, formatConfigFileMode(ConfigFileVolumeSafeMode))
	}
	return contentBytes, nil
}

// validateConfigFileProjectionValues 拒绝写入投影内容的敏感环境值或仓库凭据。
func validateConfigFileProjectionValues(volumes []ConfigFileVolume, values ...string) error {
	for _, value := range values {
		if value == "" {
			continue
		}
		for _, volume := range volumes {
			for _, file := range volume.ConfigFileToPath {
				if containsSensitiveProjectionValue(file.Content, value) {
					return fmt.Errorf("ECI ConfigFileVolume %q file %q contains a sensitive runtime value", volume.Name, file.Path)
				}
			}
		}
	}
	return nil
}

// containsSensitiveProjectionValue 同时拒绝凭据原文和常见 Base64 表示，避免投影脚本在 ECI 内解码后形成第二条凭据通道。
func containsSensitiveProjectionValue(content string, value string) bool {
	if containsSensitiveRuntimeValue(content, value) {
		return true
	}
	if len(value) < 4 {
		return false
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if strings.Contains(content, encoding.EncodeToString([]byte(value))) {
			return true
		}
	}
	return false
}

// containsSensitiveRuntimeValue 对长凭据做精确子串检查，对短凭据只接受独立值边界，避免单字符误报。
func containsSensitiveRuntimeValue(content string, value string) bool {
	if len(value) >= 4 {
		return strings.Contains(content, value)
	}
	for offset := 0; offset <= len(content)-len(value); {
		relative := strings.Index(content[offset:], value)
		if relative < 0 {
			return false
		}
		start := offset + relative
		if sensitiveRuntimeValueAtBoundary(content, start, len(value)) {
			return true
		}
		offset = start + 1
	}
	return false
}

// sensitiveRuntimeValueAtBoundary 阻止短值把普通命令单词中的同名字符误判为凭据。
func sensitiveRuntimeValueAtBoundary(content string, start int, size int) bool {
	end := start + size
	leftBoundary := start == 0 || !isSensitiveRuntimeValueByte(content[start-1])
	rightBoundary := end == len(content) || !isSensitiveRuntimeValueByte(content[end])
	return leftBoundary && rightBoundary
}

// isSensitiveRuntimeValueByte 定义 registry 用户名和 token 内可连续出现的 ASCII 字符。
func isSensitiveRuntimeValueByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("._-@:/+", rune(value))
}

func sensitiveEnvironmentValues(environment map[string]string) []string {
	values := make([]string, 0, len(environment))
	for key, value := range environment {
		if value != "" && hasSensitiveEnvironmentKey(key) {
			values = append(values, value)
		}
	}
	return values
}

func hasProjectionForbiddenToken(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{
		"credential", "secret", "password", "token", "depend", "deps", "cache", "node_modules", "node-modules",
		"go-mod", "go_mod", "build-cache", "build_cache", "npm", "playwright", "vite",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func hasProjectionCredentialMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, token := range []string{
		"super_dolphin_ci_ghcr", "super-dolphin-ci-ghcr", "accesskeyid", "accesskeysecret",
		"aws_access_key_id", "aws_secret_access_key", "client_secret", "private_key", "jwt",
		"authorization", "bearer ", "registry_credential", "registry-credential", "password",
		"x-amz-", "x-oss-signature", "signature=",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// configFileProjectionRedactionValues 返回投影内容及其 API Base64 形态，
// 确保 CLI 错误即使回显参数也不会泄露控制文本或其中的未知值。
func configFileProjectionRedactionValues(volumes []ConfigFileVolume) map[string]string {
	values := make(map[string]string)
	for volumeIndex, volume := range volumes {
		for fileIndex, file := range volume.ConfigFileToPath {
			prefix := fmt.Sprintf("config_file_%d_%d", volumeIndex+1, fileIndex+1)
			values[prefix+"_raw"] = file.Content
			values[prefix+"_base64"] = base64.StdEncoding.EncodeToString([]byte(file.Content))
		}
	}
	return values
}

func hasSensitiveEnvironmentKey(key string) bool {
	lower := strings.ToLower(key)
	for _, token := range []string{"token", "secret", "password", "credential", "authorization", "access_key", "access-key", "username", "user_name", "user-name"} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// validateConfigFilePath 仅接受挂载目录下的规范相对路径。
func validateConfigFilePath(filePath string) error {
	if err := validateConfigFilePathShape(filePath); err != nil {
		return err
	}
	for segment := range strings.SplitSeq(filePath, "/") {
		if err := validateConfigFilePathSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func validateConfigFilePathShape(filePath string) error {
	if filePath == "" {
		return errors.New("ConfigFileToPath.Path must be a canonical relative path")
	}
	if filePath == "." || len(filePath) > 1024 || path.IsAbs(filePath) || path.Clean(filePath) != filePath || strings.ContainsAny(filePath, "\\\\:\x00") || strings.IndexFunc(filePath, unicode.IsControl) >= 0 {
		return errors.New("ConfigFileToPath.Path must be a canonical relative path")
	}
	return nil
}

func validateConfigFilePathSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." {
		return errors.New("ConfigFileToPath.Path must not contain empty, dot, or parent segments")
	}
	return nil
}

// effectiveConfigFileMode 应用卷默认权限，并保持投影权限固定且安全。
func effectiveConfigFileMode(file ConfigFileToPath, volume ConfigFileVolume) int {
	if file.Mode == 0 {
		return volume.DefaultMode
	}
	return file.Mode
}

func formatConfigFileMode(mode int) string {
	return "0" + strconv.FormatInt(int64(mode), 8)
}

// appendConfigFileVolumes 按阿里云 CLI 官方嵌套参数名编码投影卷。
func appendConfigFileVolumes(args []string, start int, volumes []ConfigFileVolume) []string {
	for index, volume := range volumes {
		prefix := fmt.Sprintf("--Volume.%d", start+index)
		args = append(args,
			prefix+".Name", volume.Name,
			prefix+".Type", ConfigFileVolumeType,
			prefix+".ConfigFileVolume.DefaultMode", formatConfigFileMode(volume.DefaultMode),
		)
		for fileIndex, file := range volume.ConfigFileToPath {
			filePrefix := fmt.Sprintf("%s.ConfigFileVolume.ConfigFileToPath.%d", prefix, fileIndex+1)
			args = append(args,
				filePrefix+".Path", file.Path,
				filePrefix+".Content", base64.StdEncoding.EncodeToString([]byte(file.Content)),
				filePrefix+".Mode", formatConfigFileMode(effectiveConfigFileMode(file, volume)),
			)
		}
	}
	return args
}
