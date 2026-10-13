package instance

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EnsureSingleInstance ensures only one instance of musegateway runs.
// If an older process is alive, it terminates the older one and waits for cleanup.
func EnsureSingleInstance(dataDir string) (func(), error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("gagal membuat direktori data: %w", err)
	}

	pidFile := filepath.Join(dataDir, "musegateway.pid")
	currentPID := os.Getpid()

	if data, err := os.ReadFile(pidFile); err == nil {
		content := strings.TrimSpace(string(data))
		if oldPID, err := strconv.Atoi(content); err == nil && oldPID > 0 && oldPID != currentPID {
			if isProcessAlive(oldPID) {
				log.Printf("⚠️ Ditemukan instance aktif sebelumnya (PID: %d). Menghentikan instance lama...", oldPID)
				if err := killProcess(oldPID); err != nil {
					log.Printf("⚠️ Gagal menghentikan instance lama (PID %d): %v", oldPID, err)
				} else {
					log.Printf("🛑 Instance lama (PID: %d) berhasil dihentikan.", oldPID)
				}

				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if !isProcessAlive(oldPID) {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	}

	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(currentPID)), 0644); err != nil {
		return nil, fmt.Errorf("gagal menyimpan PID file: %w", err)
	}
	log.Printf("🔒 Single-Instance Lock aktif (PID: %d) -> %s", currentPID, pidFile)

	cleanup := func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			if strings.TrimSpace(string(data)) == strconv.Itoa(currentPID) {
				_ = os.Remove(pidFile)
				log.Printf("🔓 Single-Instance Lock dilepas (PID: %d)", currentPID)
			}
		}
	}

	return cleanup, nil
}
