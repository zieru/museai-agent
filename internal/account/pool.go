package account

import (
	"context"
	"fmt"
	"sync"
	"time"

	"musegateway/internal/browser"
	"musegateway/internal/config"
)

type ManagedAccount struct {
	Config    config.AccountConfig
	Session   *browser.Session
	mu        sync.Mutex
	isBusy    bool
	lastUsed  time.Time
}

type Pool struct {
	accounts []*ManagedAccount
	mu       sync.RWMutex
	cfg      *config.GatewayConfig
	currIdx  int
}

func NewPool(cfg *config.GatewayConfig) *Pool {
	return &Pool{
		cfg: cfg,
	}
}

// InitializeSessions prepares browser sessions for all enabled accounts
func (p *Pool) InitializeSessions() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, accCfg := range p.cfg.Accounts {
		if !accCfg.Enabled {
			continue
		}

		sess, err := browser.NewSession(browser.SessionOptions{
			AccountID:     accCfg.ID,
			StorageDir:    accCfg.StorageDir,
			ChatURL:       accCfg.ChatURL,
			Headless:      p.cfg.Headless,
			DisableImages: p.cfg.DisableImages,
			Proxy:         accCfg.Proxy,
		})
		if err != nil {
			fmt.Printf("Warning: failed to initialize session for account %s: %v\n", accCfg.ID, err)
			continue
		}

		go func(s *browser.Session, id string) {
			fmt.Printf("[Account: %s] Warming up isolated browser session...\n", id)
			if err := s.Start(60 * time.Second); err != nil {
				fmt.Printf("[Account: %s] Warning during startup: %v\n", id, err)
			} else {
				fmt.Printf("[Account: %s] Ready!\n", id)
			}
		}(sess, accCfg.ID)

		p.accounts = append(p.accounts, &ManagedAccount{
			Config:  accCfg,
			Session: sess,
		})
	}

	if len(p.accounts) == 0 {
		return fmt.Errorf("no accounts successfully initialized")
	}

	return nil
}

// CloseAll closes all active browser contexts
func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, acc := range p.accounts {
		if acc.Session != nil {
			acc.Session.Close()
		}
	}
}

// GetAccount retrieves an account by ID or picks next available in round-robin fashion
func (p *Pool) GetAccount(preferredID string) (*ManagedAccount, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.accounts) == 0 {
		return nil, fmt.Errorf("no active accounts available")
	}

	// 1. Direct match if specific account ID requested (e.g. from model name or header)
	if preferredID != "" {
		for _, acc := range p.accounts {
			if acc.Config.ID == preferredID {
				return acc, nil
			}
		}
	}

	// 2. Round robin selection
	acc := p.accounts[p.currIdx%len(p.accounts)]
	p.currIdx++
	return acc, nil
}

// ExecutePrompt runs a prompt with isolated mutex locking per account
func (a *ManagedAccount) ExecutePrompt(reqCtx context.Context, prompt string, files []string, timeout time.Duration) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.isBusy = true
	a.lastUsed = time.Now()
	defer func() {
		a.isBusy = false
	}()

	return a.Session.SendPrompt(prompt, files, timeout)
}
