package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"musegateway/internal/version"
)

type ReleaseInfo struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []Asset   `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

func CheckForUpdate(ctx context.Context, repo, currentVer string) (*ReleaseInfo, *Asset, bool, error) {
	if repo == "" {
		repo = version.DefaultRepo
	}
	if currentVer == "" {
		currentVer = version.Version
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, nil, false, fmt.Errorf("gagal membuat HTTP request: %w", err)
	}

	req.Header.Set("User-Agent", fmt.Sprintf("MuseGateway-Updater/%s", currentVer))
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("gagal menghubungi GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, false, fmt.Errorf("repository atau release tidak ditemukan pada '%s'", repo)
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, nil, false, fmt.Errorf("GitHub API mengembalikan status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, nil, false, fmt.Errorf("gagal mem-parsing response GitHub release: %w", err)
	}

	hasUpdate := isNewerVersion(currentVer, rel.TagName)
	matchedAsset := findMatchingAsset(rel.Assets, runtime.GOOS, runtime.GOARCH)

	return &rel, matchedAsset, hasUpdate, nil
}

func isNewerVersion(current, latest string) bool {
	c := strings.TrimPrefix(strings.TrimSpace(current), "v")
	l := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if c == "" || l == "" {
		return false
	}
	return c != l
}

func findMatchingAsset(assets []Asset, targetOS, targetArch string) *Asset {
	for _, a := range assets {
		nameLower := strings.ToLower(a.Name)
		if targetOS == "linux" && (strings.Contains(nameLower, "linux-musl") || strings.Contains(nameLower, "linux")) {
			return &a
		}
		if targetOS == "windows" && strings.HasSuffix(nameLower, ".exe") {
			return &a
		}
	}
	if len(assets) > 0 {
		return &assets[0]
	}
	return nil
}

func ApplyUpdate(ctx context.Context, downloadURL string, progressFn func(downloaded, total int64)) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("gagal mendapatkan path executable saat ini: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("gagal resolve symlink executable: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	tempFile, err := os.CreateTemp(exeDir, "musegateway-update-*.tmp")
	if err != nil {
		tempFile, err = os.CreateTemp("", "musegateway-update-*.tmp")
		if err != nil {
			return fmt.Errorf("gagal membuat temporary file untuk update: %w", err)
		}
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("gagal membuat download request: %w", err)
	}
	req.Header.Set("User-Agent", "MuseGateway-Updater")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gagal mengunduh binary update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unduhan gagal dengan HTTP status %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 64*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := tempFile.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("gagal menulis data ke temporary file: %w", writeErr)
			}
			downloaded += int64(n)
			if progressFn != nil {
				progressFn(downloaded, totalSize)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("error saat membaca stream download: %w", readErr)
		}
	}

	_ = tempFile.Close()
	_ = os.Chmod(tempPath, 0755)

	if runtime.GOOS == "windows" {
		oldExe := exePath + ".old"
		_ = os.Remove(oldExe)
		if err := os.Rename(exePath, oldExe); err != nil {
			return fmt.Errorf("gagal me-rename executable lama ke .old: %w", err)
		}
		if err := copyOrMoveFile(tempPath, exePath); err != nil {
			_ = os.Rename(oldExe, exePath)
			return fmt.Errorf("gagal memasang binary baru: %w", err)
		}
	} else {
		if err := os.Rename(tempPath, exePath); err != nil {
			if err := copyOrMoveFile(tempPath, exePath); err != nil {
				return fmt.Errorf("gagal mengganti file executable: %w", err)
			}
		}
	}

	_ = os.Chmod(exePath, 0755)
	return nil
}

func copyOrMoveFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
