package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultRepository = "ngojclee/camera-connect"
	defaultAppName    = "CameraConnect"
)

// CheckerOptions configures update release checking and download behavior.
type CheckerOptions struct {
	Repository       string
	AppName          string
	LatestReleaseURL string
	HTTPClient       *http.Client
}

// Checker queries latest release metadata and downloads update assets.
type Checker struct {
	repository string
	appName    string
	latestURL  string
	client     *http.Client
}

// LatestRelease represents the latest known release data.
type LatestRelease struct {
	CurrentVersion string
	LatestVersion  string
	HasUpdate      bool
	ReleaseNotes   string
	ReleaseURL     string
	PublishedAt    string
	AssetName      string
	AssetURL       string
	CheckedAt      string
}

type releaseAPIResponse struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Assets      []releaseItem `json:"assets"`
}

type releaseItem struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// NewChecker constructs a release checker.
func NewChecker(opts CheckerOptions) *Checker {
	repo := strings.TrimSpace(opts.Repository)
	if repo == "" {
		repo = defaultRepository
	}

	appName := strings.TrimSpace(opts.AppName)
	if appName == "" {
		appName = defaultAppName
	}

	latestURL := strings.TrimSpace(opts.LatestReleaseURL)
	if latestURL == "" {
		latestURL = fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", repo)
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2500 * time.Millisecond}
	}

	return &Checker{
		repository: repo,
		appName:    appName,
		latestURL:  latestURL,
		client:     client,
	}
}

// CheckLatest retrieves release metadata and computes update availability.
func (c *Checker) CheckLatest(ctx context.Context, currentVersion string) (LatestRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.latestURL, nil)
	if err != nil {
		return LatestRelease{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CameraConnect-Agent")

	resp, err := c.client.Do(req)
	if err != nil {
		return LatestRelease{}, fmt.Errorf("request latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return LatestRelease{}, fmt.Errorf("latest release endpoint status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return LatestRelease{}, fmt.Errorf("read release response body: %w", err)
	}

	releases, err := parseReleaseResponses(body)
	if err != nil {
		return LatestRelease{}, fmt.Errorf("decode release response: %w", err)
	}
	if len(releases) == 0 {
		return LatestRelease{}, fmt.Errorf("no releases returned from %s", c.latestURL)
	}

	parsed, err := selectReleaseForApp(releases, c.appName)
	if err != nil {
		return LatestRelease{}, err
	}

	selected := selectPreferredAsset(parsed.Assets, c.appName)
	latestVersion := resolveReleaseVersion(parsed, selected)
	if latestVersion == "" {
		latestVersion = strings.TrimSpace(parsed.TagName)
	}
	if latestVersion == "" {
		latestVersion = strings.TrimSpace(parsed.Name)
	}

	info := LatestRelease{
		CurrentVersion: strings.TrimSpace(currentVersion),
		LatestVersion:  latestVersion,
		ReleaseNotes:   strings.TrimSpace(parsed.Body),
		ReleaseURL:     strings.TrimSpace(parsed.HTMLURL),
		PublishedAt:    strings.TrimSpace(parsed.PublishedAt),
		AssetName:      strings.TrimSpace(selected.Name),
		AssetURL:       strings.TrimSpace(selected.BrowserDownloadURL),
		CheckedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	if info.CurrentVersion != "" && info.LatestVersion != "" {
		if cmp, err := CompareVersions(info.CurrentVersion, info.LatestVersion); err == nil && cmp < 0 {
			info.HasUpdate = true
		}
	}
	return info, nil
}

// DownloadToFile downloads an asset URL to destination path atomically.
func (c *Checker) DownloadToFile(ctx context.Context, assetURL, destinationPath string, onProgress func(downloaded, total int64)) error {
	if strings.TrimSpace(assetURL) == "" {
		return fmt.Errorf("asset url is required")
	}
	if strings.TrimSpace(destinationPath) == "" {
		return fmt.Errorf("destination path is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", "CameraConnect-Agent")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download status=%d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	tempPath := destinationPath + ".part"
	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("create temp download file: %w", err)
	}

	downloaded := int64(0)
	total := resp.ContentLength
	reader := io.TeeReader(resp.Body, writerFunc(func(p []byte) (int, error) {
		n := len(p)
		downloaded += int64(n)
		if onProgress != nil {
			onProgress(downloaded, total)
		}
		return n, nil
	}))

	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("download copy: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temp download file: %w", closeErr)
	}

	if err := os.Rename(tempPath, destinationPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("rename temp download file: %w", err)
	}
	return nil
}

// ResolveAssetName resolves a filename for an asset URL.
func ResolveAssetName(assetURL, fallback string) string {
	name := strings.TrimSpace(fallback)
	if name != "" {
		return filepath.Base(name)
	}

	u, err := url.Parse(strings.TrimSpace(assetURL))
	if err != nil {
		return "cameraconnect_update.bin"
	}
	base := strings.TrimSpace(path.Base(u.Path))
	if base == "" || base == "." || base == "/" {
		return "cameraconnect_update.bin"
	}
	return base
}

func parseReleaseResponses(body []byte) ([]releaseAPIResponse, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("empty release response")
	}

	if strings.HasPrefix(trimmed, "[") {
		var list []releaseAPIResponse
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return nil, err
		}
		return list, nil
	}

	var single releaseAPIResponse
	if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
		return nil, err
	}
	return []releaseAPIResponse{single}, nil
}

func selectReleaseForApp(releases []releaseAPIResponse, appName string) (releaseAPIResponse, error) {
	if len(releases) == 0 {
		return releaseAPIResponse{}, fmt.Errorf("no releases available")
	}

	appKey := normalizeAppKey(appName)
	best := releaseAPIResponse{}
	hasBest := false
	bestVersion := ""
	bestVersionParsed := false

	for _, release := range releases {
		if !releaseMatchesApp(release, appKey) {
			continue
		}

		version := resolveReleaseVersion(release, selectPreferredAsset(release.Assets, appName))
		versionParsed := version != ""

		if !hasBest {
			best = release
			hasBest = true
			bestVersion = version
			bestVersionParsed = versionParsed
			continue
		}

		switch {
		case versionParsed && !bestVersionParsed:
			best = release
			bestVersion = version
			bestVersionParsed = true
		case versionParsed && bestVersionParsed:
			cmp, err := CompareVersions(version, bestVersion)
			if err == nil && cmp > 0 {
				best = release
				bestVersion = version
			}
		}
	}

	if hasBest {
		return best, nil
	}

	// Fallback: use the first release when app-specific filtering finds nothing.
	return releases[0], nil
}

func selectPreferredAsset(assets []releaseItem, appName string) releaseItem {
	if len(assets) == 0 {
		return releaseItem{}
	}

	appKey := normalizeAppKey(appName)
	bestIdx := 0
	bestScore := scoreAsset(assets[0], appKey)
	for i := 1; i < len(assets); i++ {
		score := scoreAsset(assets[i], appKey)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	return assets[bestIdx]
}

func scoreAsset(asset releaseItem, appKey string) int {
	name := strings.ToLower(strings.TrimSpace(asset.Name))
	normalizedName := normalizeAppKey(name)
	score := 0
	switch {
	case strings.HasSuffix(name, ".exe"):
		score += 40
	case strings.HasSuffix(name, ".msi"):
		score += 35
	case strings.HasSuffix(name, ".zip"):
		score += 20
	}

	if appKey != "" {
		if strings.Contains(normalizedName, appKey) {
			score += 60
		}
	} else if strings.Contains(name, "cameraconnect") || strings.Contains(name, "camera-connect") {
		score += 30
	}
	if strings.Contains(name, "setup") || strings.Contains(name, "installer") {
		score += 20
	}
	if strings.Contains(name, "windows") || strings.Contains(name, "win64") || strings.Contains(name, "x64") {
		score += 5
	}
	if strings.Contains(name, "portable") {
		score -= 5
	}
	if asset.Size > 0 {
		score++
	}
	return score
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) {
	return f(p)
}

var versionTokenPattern = regexp.MustCompile(`(?i)v?\d+(?:\.\d+){2,3}`)

func resolveReleaseVersion(release releaseAPIResponse, selectedAsset releaseItem) string {
	candidates := []string{
		release.TagName,
		release.Name,
		selectedAsset.Name,
	}
	for _, asset := range release.Assets {
		candidates = append(candidates, asset.Name)
	}

	best := ""
	for _, candidate := range candidates {
		version := extractVersionToken(candidate)
		if version == "" {
			continue
		}
		if best == "" {
			best = version
			continue
		}
		cmp, err := CompareVersions(version, best)
		if err == nil && cmp > 0 {
			best = version
		}
	}
	return best
}

func extractVersionToken(raw string) string {
	matches := versionTokenPattern.FindAllString(strings.TrimSpace(raw), -1)
	if len(matches) == 0 {
		return ""
	}
	best := strings.TrimSpace(matches[0])
	for i := 1; i < len(matches); i++ {
		candidate := strings.TrimSpace(matches[i])
		cmp, err := CompareVersions(candidate, best)
		if err == nil && cmp > 0 {
			best = candidate
		}
	}
	return best
}

func normalizeAppKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "", ".", "")
	return replacer.Replace(key)
}

func releaseMatchesApp(release releaseAPIResponse, appKey string) bool {
	if appKey == "" {
		return true
	}

	if strings.Contains(normalizeAppKey(release.TagName), appKey) {
		return true
	}
	if strings.Contains(normalizeAppKey(release.Name), appKey) {
		return true
	}
	for _, asset := range release.Assets {
		if strings.Contains(normalizeAppKey(asset.Name), appKey) {
			return true
		}
	}
	return false
}
