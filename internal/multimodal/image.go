package multimodal

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ProcessImage extracts image data (base64 data URL or HTTP URL) and saves it to a temporary file
func ProcessImage(tempDir, imageURLOrData string) (string, error) {
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", err
	}

	uniqueName := fmt.Sprintf("muse_upload_%s", uuid.New().String()[:8])

	// Case 1: Data URL (e.g. data:image/png;base64,...)
	if strings.HasPrefix(imageURLOrData, "data:") {
		parts := strings.SplitN(imageURLOrData, ",", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid data URI format")
		}

		header := parts[0]
		ext := ".png"
		if strings.Contains(header, "image/jpeg") || strings.Contains(header, "image/jpg") {
			ext = ".jpg"
		} else if strings.Contains(header, "image/webp") {
			ext = ".webp"
		} else if strings.Contains(header, "image/gif") {
			ext = ".gif"
		}

		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return "", fmt.Errorf("failed to decode base64 image: %w", err)
		}

		filePath := filepath.Join(tempDir, uniqueName+ext)
		if err := os.WriteFile(filePath, decoded, 0644); err != nil {
			return "", err
		}
		return filePath, nil
	}

	// Case 2: HTTP / HTTPS URL
	if strings.HasPrefix(imageURLOrData, "http://") || strings.HasPrefix(imageURLOrData, "https://") {
		resp, err := http.Get(imageURLOrData)
		if err != nil {
			return "", fmt.Errorf("failed to fetch image URL: %w", err)
		}
		defer resp.Body.Close()

		ext := ".png"
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "image/jpeg") {
			ext = ".jpg"
		} else if strings.Contains(contentType, "image/webp") {
			ext = ".webp"
		}

		filePath := filepath.Join(tempDir, uniqueName+ext)
		f, err := os.Create(filePath)
		if err != nil {
			return "", err
		}
		defer f.Close()

		if _, err := io.Copy(f, resp.Body); err != nil {
			return "", err
		}
		return filePath, nil
	}

	// Case 3: Direct local file path
	if _, err := os.Stat(imageURLOrData); err == nil {
		return imageURLOrData, nil
	}

	return "", fmt.Errorf("unrecognized image source")
}
