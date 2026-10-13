package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"musegateway/internal/account"
	"musegateway/internal/config"
	"musegateway/internal/gateway"
	"musegateway/internal/instance"
	"musegateway/internal/updater"
	"musegateway/internal/version"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	loginAccountID := flag.String("login", "", "Account ID to perform interactive headed login")
	doUpdate := flag.Bool("update", false, "Periksa dan pasang update binary terbaru dari GitHub Releases")
	doCheck := flag.Bool("check", false, "Periksa ketersediaan update terbaru dari GitHub Releases")
	flag.Parse()

	if *doUpdate || *doCheck {
		handleCLIUpdate(*doUpdate)
		return
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 1. Interactive login mode
	if *loginAccountID != "" {
		runLoginFlow(cfg, *loginAccountID)
		return
	}

	// 2. Gateway Server Daemon Mode
	// Single instance check
	cleanupLock, err := instance.EnsureSingleInstance("sessions")
	if err != nil {
		log.Printf("Warning single instance: %v", err)
	} else {
		defer cleanupLock()
	}

	fmt.Println("========================================================")
	fmt.Println("🚀 Starting Muse AI Gateway (Go Edition)")
	fmt.Printf("   Version:         v%s\n", version.Version)
	fmt.Printf("   Port:            %d\n", cfg.Port)
	fmt.Printf("   Default Model:   %s\n", cfg.DefaultModel)
	fmt.Printf("   Headless:        %t\n", cfg.Headless)
	fmt.Printf("   Disable Images:  %t (Lightweight Browser Mode)\n", cfg.DisableImages)
	fmt.Printf("   Total Accounts:  %d\n", len(cfg.Accounts))
	fmt.Println("========================================================")

	pool := account.NewPool(cfg)
	if err := pool.InitializeSessions(); err != nil {
		log.Printf("Warning during initial session warmup: %v", err)
	}
	defer pool.CloseAll()

	srv := gateway.NewServer(cfg, pool)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	go func() {
		fmt.Printf("[Gateway] Listening on http://127.0.0.1:%d\n", cfg.Port)
		fmt.Println("   - Models:             GET  /api/v1/models")
		fmt.Println("   - OpenAI Chat:        POST /api/v1/chat/completions (model: muse/muse_01)")
		fmt.Println("   - Claude Code / Anth: POST /api/v1/messages")
		fmt.Println("   - Health check:       GET  /health")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("\nShutting down Muse AI Gateway...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	fmt.Println("Gateway stopped.")
}

func handleCLIUpdate(apply bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Printf("🔍 Memeriksa update dari %s ...\n", version.DefaultRepo)
	rel, asset, hasUpdate, err := updater.CheckForUpdate(ctx, version.DefaultRepo, version.Version)
	if err != nil {
		fmt.Printf("❌ Gagal memeriksa update: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("• Versi saat ini : v%s\n", version.Version)
	fmt.Printf("• Release terbaru: %s (tanggal %s)\n", rel.TagName, rel.PublishedAt.Format("02 Jan 2006 15:04"))
	if rel.Body != "" {
		fmt.Printf("• Changelog:\n%s\n", rel.Body)
	}

	if asset == nil {
		fmt.Printf("⚠️ Tidak ada binary yang cocok untuk sistem operasi %s/%s pada release %s.\n", runtime.GOOS, runtime.GOARCH, rel.TagName)
		os.Exit(1)
	}
	fmt.Printf("• Binary asset   : %s (%.2f MB)\n", asset.Name, float64(asset.Size)/(1024*1024))

	if !apply {
		if hasUpdate {
			fmt.Printf("✨ Update tersedia. Jalankan './musegateway-linux-musl-amd64 --update' untuk memasang.\n")
		} else {
			fmt.Printf("✅ Muse AI Gateway sudah menggunakan versi terbaru.\n")
		}
		return
	}

	if !hasUpdate {
		fmt.Printf("✅ Versi terbaru sudah terpasang. Tidak perlu update.\n")
		return
	}

	fmt.Printf("⬇️ Mengunduh %s ...\n", asset.Name)
	if err := updater.ApplyUpdate(ctx, asset.BrowserDownloadURL, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\r⏳ %.1f%% (%d/%d MB)", pct, downloaded/(1024*1024), total/(1024*1024))
		}
	}); err != nil {
		fmt.Printf("\n❌ Gagal memasang update: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ Update berhasil dipasang ke versi %s!\n", rel.TagName)
}

func runLoginFlow(cfg *config.GatewayConfig, accountID string) {
	var targetAccount *config.AccountConfig
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == accountID {
			targetAccount = &cfg.Accounts[i]
			break
		}
	}

	if targetAccount == nil {
		storageDir := filepath.Join(".", "sessions", accountID)
		newAcc := config.AccountConfig{
			ID:         accountID,
			Name:       fmt.Sprintf("Account %s", accountID),
			ChatURL:    "https://muse.ai",
			StorageDir: storageDir,
			Enabled:    true,
		}
		cfg.Accounts = append(cfg.Accounts, newAcc)
		targetAccount = &newAcc
		_ = config.SaveConfig("configs/config.yaml", cfg)
	}

	absDir, _ := filepath.Abs(targetAccount.StorageDir)
	_ = os.MkdirAll(absDir, 0755)

	fmt.Printf("\nOpening Headed Browser for Account: %s (%s)...\n", targetAccount.ID, absDir)
	fmt.Println("1. Log in to your Muse AI account in the opened window.")
	fmt.Println("2. Open or create the chat thread.")
	fmt.Println("3. Return to this console and press ENTER.")

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(absDir),
		chromedp.Flag("headless", false),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("window-size", "1280,800"),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	targetURL := targetAccount.ChatURL
	if targetURL == "" {
		targetURL = "https://muse.ai"
	}

	fmt.Printf("[*] Navigating browser to %s ...\n", targetURL)
	if err := chromedp.Run(taskCtx, chromedp.Navigate(targetURL)); err != nil {
		fmt.Printf("Warning during initial navigate: %v\n", err)
	}

	fmt.Print("\n--> Press ENTER after login is complete: ")
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')

	fmt.Printf("[OK] Session state successfully saved in: %s\n", absDir)
}
