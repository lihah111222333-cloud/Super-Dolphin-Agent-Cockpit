package personalization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// Service 定义个性化服务接口，负责按 cwd 读写用户个人资料。
type Service interface {
	GetProfile(ctx context.Context, cwd string) (ProfileResult, error)
	SaveProfile(ctx context.Context, cwd string, profile Profile) (ProfileResult, error)
}

// service 是 Service 接口的内部实现，依赖窄持久化端口保存资料。
type service struct {
	prefs PreferenceStore
}

// NewService 创建项目级个性化服务。该服务只读写 UI 偏好端口，不持有额外全局状态。
func NewService(prefs PreferenceStore) Service {
	return &service{prefs: prefs}
}

// GetProfile 读取当前项目的个人资料；没有保存过时返回空 profile。
func (s *service) GetProfile(ctx context.Context, cwd string) (ProfileResult, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ProfileResult{}, fmt.Errorf("personalization: cwd is required")
	}
	if s.prefs == nil {
		return ProfileResult{}, fmt.Errorf("personalization: preference store is required")
	}
	raw, err := s.prefs.GetValue(ctx, cwd, profilePreferenceKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return ProfileResult{}, nil
		}
		return ProfileResult{}, fmt.Errorf("personalization: get profile: %w", err)
	}
	if len(raw) == 0 {
		return ProfileResult{}, nil
	}
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return ProfileResult{}, fmt.Errorf("personalization: decode profile: %w", err)
	}
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return ProfileResult{}, err
	}
	return ProfileResult{Profile: normalized}, nil
}

// SaveProfile 校验并保存当前项目的个人资料；全空 profile 表示清空个人资料。
func (s *service) SaveProfile(ctx context.Context, cwd string, profile Profile) (ProfileResult, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ProfileResult{}, fmt.Errorf("personalization: cwd is required")
	}
	if s.prefs == nil {
		return ProfileResult{}, fmt.Errorf("personalization: preference store is required")
	}
	normalized, err := normalizeProfile(profile)
	if err != nil {
		return ProfileResult{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return ProfileResult{}, fmt.Errorf("personalization: encode profile: %w", err)
	}
	if err := s.prefs.Upsert(ctx, PreferenceUpsertParams{Cwd: cwd, Key: profilePreferenceKey, Value: raw}); err != nil {
		return ProfileResult{}, fmt.Errorf("personalization: save profile: %w", err)
	}
	return ProfileResult{Profile: normalized}, nil
}

// normalizeProfile 对各字段做 TrimSpace 并校验长度，不修改逻辑。
func normalizeProfile(profile Profile) (Profile, error) {
	normalized := Profile{
		DisplayName:        strings.TrimSpace(profile.DisplayName),
		Role:               strings.TrimSpace(profile.Role),
		Background:         strings.TrimSpace(profile.Background),
		CustomInstructions: strings.TrimSpace(profile.CustomInstructions),
	}
	if err := validateProfileField("displayName", normalized.DisplayName, maxShortProfileFieldRunes); err != nil {
		return Profile{}, err
	}
	if err := validateProfileField("role", normalized.Role, maxShortProfileFieldRunes); err != nil {
		return Profile{}, err
	}
	if err := validateProfileField("background", normalized.Background, maxLongProfileFieldRunes); err != nil {
		return Profile{}, err
	}
	if err := validateProfileField("customInstructions", normalized.CustomInstructions, maxLongProfileFieldRunes); err != nil {
		return Profile{}, err
	}
	return normalized, nil
}

// validateProfileField 校验单个字段的 rune 长度是否超出上限。
func validateProfileField(name, value string, maxRunes int) error {
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("personalization: %s must be at most %d characters", name, maxRunes)
	}
	return nil
}
