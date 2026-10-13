package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Session represents an isolated lightweight browser session backed by chromedp
type Session struct {
	AccountID     string
	StorageDir    string
	ChatURL       string
	Headless      bool
	DisableImages bool
	Proxy         string

	allocCtx    context.Context
	allocCancel context.CancelFunc

	taskCtx    context.Context
	taskCancel context.CancelFunc
}

// SessionOptions configures the isolated browser instance
type SessionOptions struct {
	AccountID     string
	StorageDir    string
	ChatURL       string
	Headless      bool
	DisableImages bool
	Proxy         string
}

// NewSession initializes an isolated headless browser session
func NewSession(opts SessionOptions) (*Session, error) {
	absStorageDir, err := filepath.Abs(opts.StorageDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute storage path: %w", err)
	}
	if err := os.MkdirAll(absStorageDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	execOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(absStorageDir),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-software-rasterizer", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-translate", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("window-size", "1280,800"),
	)

	if opts.Headless {
		execOpts = append(execOpts, chromedp.Flag("headless", "new"))
	} else {
		execOpts = append(execOpts, chromedp.Flag("headless", false))
	}

	if opts.DisableImages {
		// Massive RAM and bandwidth savings: disable image rendering
		execOpts = append(execOpts, chromedp.Flag("blink-settings", "imagesEnabled=false"))
	}

	if opts.Proxy != "" {
		execOpts = append(execOpts, chromedp.ProxyServer(opts.Proxy))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), execOpts...)
	taskCtx, taskCancel := chromedp.NewContext(allocCtx)

	s := &Session{
		AccountID:     opts.AccountID,
		StorageDir:    absStorageDir,
		ChatURL:       opts.ChatURL,
		Headless:      opts.Headless,
		DisableImages: opts.DisableImages,
		Proxy:         opts.Proxy,
		allocCtx:      allocCtx,
		allocCancel:   allocCancel,
		taskCtx:       taskCtx,
		taskCancel:    taskCancel,
	}

	return s, nil
}

// Start launches the browser and navigates to the chat page
func (s *Session) Start(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(s.taskCtx, timeout)
	defer cancel()

	var actions []chromedp.Action
	if s.DisableImages {
		// Block unnecessary heavy media assets over network
		actions = append(actions, network.Enable(), network.SetBlockedURLS([]string{
			"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.svg", "*.woff", "*.woff2", "*.ttf",
		}))
	}

	targetURL := s.ChatURL
	if targetURL == "" {
		targetURL = "https://muse.ai"
	}

	actions = append(actions, chromedp.Navigate(targetURL))

	if err := chromedp.Run(ctx, actions...); err != nil {
		return fmt.Errorf("failed to start and navigate session %s: %w", s.AccountID, err)
	}

	return nil
}

// Close gracefully terminates the isolated browser session
func (s *Session) Close() {
	if s.taskCancel != nil {
		s.taskCancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
}

// Context returns the active chromedp execution context
func (s *Session) Context() context.Context {
	return s.taskCtx
}

// UploadFiles sets file inputs in the DOM via native CDP DOM.setFileInputFiles
func (s *Session) UploadFiles(ctx context.Context, filePaths []string) error {
	if len(filePaths) == 0 {
		return nil
	}

	var nodes []*cdp.Node
	err := chromedp.Run(ctx,
		chromedp.Nodes(`input[type="file"]`, &nodes, chromedp.AtLeast(0)),
	)
	if err != nil || len(nodes) == 0 {
		return fmt.Errorf("file input not found in DOM or failed to query: %w", err)
	}

	targetNode := nodes[len(nodes)-1]
	return chromedp.Run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		return dom.SetFileInputFiles(filePaths).WithNodeID(targetNode.NodeID).Do(c)
	}))
}

// SendPrompt fills the composer, optionally attaches files, submits, and waits for reply
func (s *Session) SendPrompt(prompt string, filePaths []string, timeout time.Duration) (string, error) {
	callCtx, cancel := context.WithTimeout(s.taskCtx, timeout)
	defer cancel()

	if len(filePaths) > 0 {
		if err := s.UploadFiles(callCtx, filePaths); err != nil {
			fmt.Printf("[%s] Warning: upload files failed: %v\n", s.AccountID, err)
		}
		// Brief pause for UI file attachment card to mount
		time.Sleep(1 * time.Second)
	}

	// 1. Locate and fill composer robustly using JS
	fillJS := fmt.Sprintf(`(() => {
		const candidates = [
			'textarea[placeholder*="Message" i]',
			'textarea',
			'div[contenteditable="true"]',
			'[role="textbox"]',
			'input[type="text"]'
		];
		let el = null;
		for (const sel of candidates) {
			const list = Array.from(document.querySelectorAll(sel)).filter(e => {
				const style = window.getComputedStyle(e);
				return style.display !== 'none' && style.visibility !== 'hidden' && style.opacity !== '0';
			});
			if (list.length > 0) {
				list.sort((a, b) => b.getBoundingClientRect().top - a.getBoundingClientRect().top);
				el = list[0];
				break;
			}
		}
		if (!el) return false;

		el.focus();
		if (el.tagName.toLowerCase() === 'textarea' || el.tagName.toLowerCase() === 'input') {
			el.value = %q;
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		} else {
			el.innerText = %q;
			el.dispatchEvent(new Event('input', { bubbles: true }));
		}
		return true;
	})()`, prompt, prompt)

	var filled bool
	composerSelector := `textarea, div[contenteditable="true"], [role="textbox"]`
	_ = chromedp.Run(callCtx,
		chromedp.WaitVisible(composerSelector),
		chromedp.Evaluate(fillJS, &filled),
	)

	if !filled {
		// Fallback to chromedp native input
		if err := chromedp.Run(callCtx,
			chromedp.Focus(composerSelector),
			chromedp.SetValue(composerSelector, prompt),
		); err != nil {
			return "", fmt.Errorf("failed to populate composer: %w", err)
		}
	}

	// 2. Submit via send button or Enter key
	sendJS := `(() => {
		const btns = Array.from(document.querySelectorAll('button, [role="button"]')).filter(b => {
			const style = window.getComputedStyle(b);
			return style.display !== 'none' && style.visibility !== 'hidden' && style.opacity !== '0';
		});
		for (const b of btns) {
			const label = (b.getAttribute('aria-label') || b.innerText || '').toLowerCase();
			if (label.includes('send') || label.includes('submit') || b.type === 'submit' || b.querySelector('svg')) {
				b.click();
				return true;
			}
		}
		return false;
	})()`

	var clicked bool
	_ = chromedp.Run(callCtx, chromedp.Evaluate(sendJS, &clicked))
	if !clicked {
		_ = chromedp.Run(callCtx, chromedp.KeyEvent("\r"))
	}

	// 3. Poll for reply completion
	return s.waitForReply(callCtx, prompt)
}

func (s *Session) waitForReply(ctx context.Context, originalPrompt string) (string, error) {
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()

	var lastText string
	settleCount := 0

	// JS to retrieve latest assistant message text
	extractJS := `(() => {
		const bubbles = Array.from(document.querySelectorAll('.hatch-chat-groupable-bubble, [data-role="assistant"], div[class*="message-bubble" i], article'));
		if (bubbles.length === 0) {
			const texts = Array.from(document.querySelectorAll('div[class*="message" i]')).map(b => b.innerText.trim()).filter(Boolean);
			return texts.length > 0 ? texts[texts.length - 1] : "";
		}
		for (let i = bubbles.length - 1; i >= 0; i--) {
			const b = bubbles[i];
			const cls = b.className || "";
			const isUser = cls.includes("user") || cls.includes("bg-chat-user-bubble");
			if (!isUser) {
				return b.innerText.trim();
			}
		}
		return "";
	})()`

	isGeneratingJS := `(() => {
		const stopBtn = document.querySelector('button[aria-label*="Stop" i], button:has-text("Stop"), [data-testid*="stop" i]');
		if (stopBtn && stopBtn.offsetParent !== null) return true;
		const indicators = document.querySelectorAll('[class*="typing"], [class*="thinking"], [class*="animate-pulse"]');
		for (let el of indicators) {
			if (el.offsetParent !== null) return true;
		}
		return false;
	})()`

	for {
		select {
		case <-ctx.Done():
			if lastText != "" {
				return lastText, nil
			}
			return "", ctx.Err()
		case <-ticker.C:
			var isGenerating bool
			_ = chromedp.Run(ctx, chromedp.Evaluate(isGeneratingJS, &isGenerating))

			var curText string
			err := chromedp.Run(ctx, chromedp.Evaluate(extractJS, &curText))
			if err != nil || curText == "" {
				continue
			}

			// Clean whitespace
			curText = strings.TrimSpace(curText)
			if curText == lastText && curText != "" {
				settleCount++
				// If not generating and output stable for 3 consecutive intervals (~1.8s), generation finished
				if !isGenerating && settleCount >= 3 {
					return curText, nil
				}
			} else {
				lastText = curText
				settleCount = 0
			}
		}
	}
}
