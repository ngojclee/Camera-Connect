package update

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckerCheckLatest(t *testing.T) {
	responseBody := `{
			"tag_name":"v2.0.0.0",
			"name":"v2.0.0.0",
			"body":"Release notes here",
			"html_url":"https://example.com/release",
			"published_at":"2026-03-30T01:02:03Z",
			"assets":[
				{"name":"CameraConnectInstaller.exe","browser_download_url":"https://example.com/download/installer.exe","size":1234}
			]
		}`
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
				Request:    req,
			}, nil
		}),
	}

	checker := NewChecker(CheckerOptions{
		Repository:       "example/repo",
		LatestReleaseURL: "https://example.invalid/latest",
		HTTPClient:       client,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := checker.CheckLatest(ctx, "v1.9.0.0")
	if err != nil {
		t.Fatalf("CheckLatest error: %v", err)
	}
	if !got.HasUpdate {
		t.Fatalf("expected has update true, got false: %#v", got)
	}
	if got.LatestVersion != "v2.0.0.0" {
		t.Fatalf("LatestVersion=%q, want v2.0.0.0", got.LatestVersion)
	}
	if !strings.Contains(got.ReleaseNotes, "Release notes") {
		t.Fatalf("ReleaseNotes=%q", got.ReleaseNotes)
	}
	if got.AssetURL == "" || got.AssetName == "" {
		t.Fatalf("expected asset info, got %#v", got)
	}
}

func TestCheckerCheckLatest_SelectsMatchingAppFromHubReleases(t *testing.T) {
	responseBody := `[
			{
				"tag_name":"OtherTool-v9.9.9",
				"name":"OtherTool-v9.9.9",
				"body":"Other app release",
				"html_url":"https://example.com/release/other",
				"published_at":"2026-03-30T00:00:00Z",
				"assets":[
					{"name":"OtherToolSetup.exe","browser_download_url":"https://example.com/download/other.exe","size":1234}
				]
			},
			{
				"tag_name":"CameraConnect-v2.1.0.0",
				"name":"CameraConnect-v2.1.0.0",
				"body":"CameraConnect release notes",
				"html_url":"https://example.com/release/cameraconnect",
				"published_at":"2026-03-30T01:02:03Z",
				"assets":[
					{"name":"CameraConnectInstaller-v2.1.0.0.exe","browser_download_url":"https://example.com/download/cameraconnect.exe","size":5678}
				]
			}
		]`
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
				Request:    req,
			}, nil
		}),
	}

	checker := NewChecker(CheckerOptions{
		Repository:       "ngojclee/camera-connect",
		AppName:          "CameraConnect",
		LatestReleaseURL: "https://example.invalid/releases",
		HTTPClient:       client,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := checker.CheckLatest(ctx, "v2.0.0.0")
	if err != nil {
		t.Fatalf("CheckLatest error: %v", err)
	}
	if !got.HasUpdate {
		t.Fatalf("expected has update true, got false: %#v", got)
	}
	if got.LatestVersion != "v2.1.0.0" {
		t.Fatalf("LatestVersion=%q, want v2.1.0.0", got.LatestVersion)
	}
	if got.AssetName != "CameraConnectInstaller-v2.1.0.0.exe" {
		t.Fatalf("AssetName=%q", got.AssetName)
	}
	if !strings.Contains(got.AssetURL, "cameraconnect.exe") {
		t.Fatalf("AssetURL=%q", got.AssetURL)
	}
}

func TestDownloadToFile(t *testing.T) {
	const payload = "hello-update"
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Length": []string{strconv.Itoa(len(payload))},
				},
				Body:    io.NopCloser(strings.NewReader(payload)),
				Request: req,
			}, nil
		}),
	}

	dir := t.TempDir()
	dest := filepath.Join(dir, "update.bin")

	checker := NewChecker(CheckerOptions{
		LatestReleaseURL: "http://invalid.local",
		HTTPClient:       client,
	})
	var callbackCount int

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := checker.DownloadToFile(ctx, "https://example.invalid/download", dest, func(downloaded, total int64) {
		callbackCount++
	})
	if err != nil {
		t.Fatalf("DownloadToFile error: %v", err)
	}
	if callbackCount == 0 {
		t.Fatal("expected progress callback to be called")
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != payload {
		t.Fatalf("downloaded payload=%q, want %q", string(data), payload)
	}
}

func TestResolveAssetName(t *testing.T) {
	if got := ResolveAssetName("https://example.com/releases/CameraConnectInstaller.exe", ""); got != "CameraConnectInstaller.exe" {
		t.Fatalf("ResolveAssetName got %q", got)
	}
	if got := ResolveAssetName("", "custom.msi"); got != "custom.msi" {
		t.Fatalf("ResolveAssetName fallback got %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
