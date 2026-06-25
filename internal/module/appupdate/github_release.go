// Package appupdate 提供应用自动更新的检查、下载和安装能力，支持 GitHub Releases 和自定义 manifest 两种更新源。
package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const githubLatestReleaseAPI = "https://api.github.com/repos/%s/%s/releases/latest"

// githubRelease 是 GitHub API 返回的最新 release 结构。
type githubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []githubReleaseAsset `json:"assets"`
}

// githubReleaseAsset 是 GitHub release asset 的元数据。
type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

// githubPlatformAssets 将平台对应的 artifact 和 manifest asset 配对。
type githubPlatformAssets struct {
	artifact githubReleaseAsset
	manifest githubReleaseAsset
}

// fetchGitHubLatestManifest 处理fetchgithublatestmanifest。
func (s *service) fetchGitHubLatestManifest(ctx context.Context) (ManifestPayload, UpdateArtifact, error) {
	release, err := s.fetchGitHubLatestRelease(ctx)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	assets, err := githubAssetsForPlatform(release, s.cfg.Platform)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	raw, err := s.fetchGitHubManifestAsset(ctx, assets.manifest)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	payload, artifact, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      s.cfg.PublicKey,
		AppID:          appID,
		Channel:        s.cfg.Channel,
		Platform:       s.cfg.Platform,
		CurrentVersion: s.cfg.CurrentVersion,
	})
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validateManifestArtifactMatchesGitHubAsset(artifact, assets.artifact); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	return payload, artifact, nil
}

// fetchGitHubLatestRelease 处理fetchgithublatestrelease。
func (s *service) fetchGitHubLatestRelease(ctx context.Context) (githubRelease, error) {
	apiURL, err := githubLatestReleaseURL(s.cfg.GitHubRepo)
	if err != nil {
		return githubRelease{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return githubRelease{}, fmt.Errorf("create GitHub latest release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Super-Dolphin-Updater")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("fetch GitHub latest release: %w", err)
	}
	defer resp.Body.Close()
	if err := requireSuccessStatus("fetch GitHub latest release", resp); err != nil {
		return githubRelease{}, err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return githubRelease{}, fmt.Errorf("read GitHub latest release: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(raw, &release); err != nil {
		return githubRelease{}, fmt.Errorf("decode GitHub latest release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, errors.New("GitHub latest release tag_name is required")
	}
	if len(release.Assets) == 0 {
		return githubRelease{}, errors.New("GitHub latest release has no assets")
	}
	return release, nil
}

// fetchGitHubManifestAsset 处理fetchgithubmanifestasset。
func (s *service) fetchGitHubManifestAsset(ctx context.Context, asset githubReleaseAsset) ([]byte, error) {
	if err := validateGitHubAssetDownloadURL(asset); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub app update manifest request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub app update manifest: %w", err)
	}
	defer resp.Body.Close()
	if err := requireSuccessStatus("fetch GitHub app update manifest", resp); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read GitHub app update manifest: %w", err)
	}
	if !json.Valid(raw) {
		return nil, errors.New("GitHub app update manifest is not valid JSON")
	}
	return raw, nil
}

// githubLatestReleaseURL 生成指向 GitHub 最新 release 的 API URL。
func githubLatestReleaseURL(repo string) (string, error) {
	owner, name, err := githubRepoParts(repo)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(githubLatestReleaseAPI, url.PathEscape(owner), url.PathEscape(name)), nil
}

// validateGitHubRepo 校验 GitHub repo 格式是否合法。
func validateGitHubRepo(repo string) error {
	_, _, err := githubRepoParts(repo)
	return err
}

// githubRepoParts 解析 "owner/repo" 格式字符串，不符合格式时返回错误。
func githubRepoParts(repo string) (string, string, error) {
	value := strings.TrimSpace(repo)
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("%s must be owner/repo", envUpdateGitHubRepo)
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", "", fmt.Errorf("%s must be owner/repo without whitespace", envUpdateGitHubRepo)
	}
	return parts[0], parts[1], nil
}

// githubAssetsForPlatform 从 release asset 列表中找出当前平台的 artifact 和 manifest。
func githubAssetsForPlatform(release githubRelease, platform string) (githubPlatformAssets, error) {
	artifactName, err := githubArtifactAssetName(platform)
	if err != nil {
		return githubPlatformAssets{}, err
	}
	artifact, ok := githubAssetByName(release.Assets, artifactName)
	if !ok {
		return githubPlatformAssets{}, fmt.Errorf("GitHub release missing update artifact asset %s", artifactName)
	}
	manifestName := githubManifestAssetName(platform)
	manifest, ok := githubAssetByName(release.Assets, manifestName)
	if !ok {
		return githubPlatformAssets{}, fmt.Errorf("GitHub release missing update manifest asset %s", manifestName)
	}
	return githubPlatformAssets{artifact: artifact, manifest: manifest}, nil
}

// githubArtifactAssetName 按平台返回更新产物的文件名（dmg/exe），不支持的平台返回错误。
func githubArtifactAssetName(platform string) (string, error) {
	switch updatePlatformOS(platform) {
	case "darwin":
		return "Super-Dolphin-" + platform + ".dmg", nil
	case "windows":
		return "Super-Dolphin-" + platform + ".exe", nil
	default:
		return "", fmt.Errorf("unsupported app update platform %q", platform)
	}
}

// githubManifestAssetName 返回平台对应的 manifest JSON 文件名。
func githubManifestAssetName(platform string) string {
	return "Super-Dolphin-" + platform + ".update.json"
}

// githubAssetByName 在 asset 列表中按名称查找，未找到时返回 ok=false。
func githubAssetByName(assets []githubReleaseAsset, name string) (githubReleaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

// validateManifestArtifactMatchesGitHubAsset 校验manifest产物matchesgithubasset。
func validateManifestArtifactMatchesGitHubAsset(artifact UpdateArtifact, asset githubReleaseAsset) error {
	if err := validateGitHubAssetDownloadURL(asset); err != nil {
		return err
	}
	if artifact.URL != asset.BrowserDownloadURL {
		return fmt.Errorf("app update artifact URL = %q, want GitHub release asset URL %q", artifact.URL, asset.BrowserDownloadURL)
	}
	if artifact.Size != asset.Size {
		return fmt.Errorf("app update artifact size = %d, want GitHub release asset size %d", artifact.Size, asset.Size)
	}
	assetSHA, err := githubAssetSHA256(asset)
	if err != nil {
		return err
	}
	if !strings.EqualFold(artifact.SHA256, assetSHA) {
		return fmt.Errorf("app update artifact sha256 = %s, want GitHub release asset sha256 %s", artifact.SHA256, assetSHA)
	}
	return nil
}

// validateGitHubAssetDownloadURL 校验githubassetdownloadURL。
func validateGitHubAssetDownloadURL(asset githubReleaseAsset) error {
	if strings.TrimSpace(asset.Name) == "" {
		return errors.New("GitHub release asset name is required")
	}
	assetURL, err := url.Parse(asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("parse GitHub release asset URL: %w", err)
	}
	if assetURL.Scheme != "https" || assetURL.Host == "" {
		return fmt.Errorf("GitHub release asset URL must be HTTPS with host: %q", asset.BrowserDownloadURL)
	}
	if asset.Size <= 0 {
		return fmt.Errorf("GitHub release asset %s size = %d, want > 0", asset.Name, asset.Size)
	}
	return nil
}

// githubAssetSHA256 从 asset.Digest 字段解析 sha256:<hex> 格式的摘要值。
func githubAssetSHA256(asset githubReleaseAsset) (string, error) {
	algorithm, value, ok := strings.Cut(strings.TrimSpace(asset.Digest), ":")
	if !ok || !strings.EqualFold(algorithm, "sha256") {
		return "", fmt.Errorf("GitHub release asset %s digest must be sha256:<hex>", asset.Name)
	}
	if err := validateSHA256Hex(value); err != nil {
		return "", err
	}
	return strings.ToLower(value), nil
}
