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
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
	"musegateway/internal/account"
	"musegateway/internal/config"
	"musegateway/internal/gateway"
)

func main() {
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	loginAccountID := flag.String("login", "", "Account ID to perform interactive headed login")
	flag.Parse()

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
	fmt.Println("========================================================")
	fmt.Println("🚀 Starting Muse AI Gateway (Go Edition)")
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

func runLoginFlow(cfg *config.GatewayConfig, accountID string) {
	var targetAccount *config.AccountConfig
	for i := range cfg.Accounts {
		if cfg.Accounts[i].ID == accountID {
			targetAccount = &cfg.Accounts[i]
			break
		}
	}

	if targetAccount == nil {
		// Auto-register new account config
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
		chromedp.Flag("disable-gpu", false),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	defer taskCancel()

	targetURL := targetAccount.ChatURL
	if targetURL == "" {
		targetURL = "https://muse.ai"
	}

	if err := chromedp.Run(taskCtx, chromedp.Navigate(targetURL)); err != nil {
		fmt.Printf("Failed to open browser: %v\n", err)
		return
	}

	fmt.Print("\n--> Press ENTER after login is complete: ")
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')

	fmt.Printf("[OK] Session state successfully saved in: %s\n", absDir)
}
