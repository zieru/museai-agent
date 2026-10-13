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
	// IMPORTANT CHROMEDP RULE:
	// Do NOT set a timeout on the first Run of s.taskCtx, because timing out or cancelling
	// that context kills the entire underlying browser process!
	// First allocate the browser tab on s.taskCtx directly:
	var initActions []chromedp.Action
	if s.DisableImages {
		initActions = append(initActions, network.Enable(), network.SetBlockedURLS([]string{
			"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.svg", "*.woff", "*.woff2", "*.ttf",
		}))
	}

	targetURL := s.ChatURL
	if targetURL == "" {
		targetURL = "https://muse.ai"
	}

	initActions = append(initActions, chromedp.Navigate(targetURL))

	// Run without timeout on taskCtx to keep browser alive
	if err := chromedp.Run(s.taskCtx, initActions...); err != nil {
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
	reqCtx, reqCancel := chromedp.NewContext(s.taskCtx)
	defer reqCancel()

	callCtx, cancel := context.WithTimeout(reqCtx, timeout)
	defer cancel()

	if len(filePaths) > 0 {
		if err := s.UploadFiles(callCtx, filePaths); err != nil {
			fmt.Printf("[%s] Warning: upload files failed: %v\n", s.AccountID, err)
		}
		// Brief pause for UI file attachment card to mount
		time.Sleep(1 * time.Second)
	}

	// 1. Ensure page is on chat URL
	targetURL := s.ChatURL
	if targetURL == "" {
		targetURL = "https://muse.ai"
	}

	var currentURL string
	_ = chromedp.Run(callCtx, chromedp.Location(&currentURL))
	if currentURL == "" || currentURL == "about:blank" || !strings.Contains(currentURL, "muse.ai") {
		fmt.Printf("[%s] Navigating to %s (current: %s)...\n", s.AccountID, targetURL, currentURL)
		if err := chromedp.Run(callCtx, chromedp.Navigate(targetURL)); err != nil {
			return "", fmt.Errorf("failed to navigate to %s: %w", targetURL, err)
		}
		time.Sleep(3 * time.Second)
	}

	// 2. Capture baseline assistant text before sending new prompt
	extractAssistantJS := `(() => {
		const bubbles = Array.from(document.querySelectorAll('.hatch-chat-groupable-bubble, [data-role="assistant"], div[class*="message-bubble" i], article'));
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

	var beforeAssistantText string
	_ = chromedp.Run(callCtx, chromedp.Evaluate(extractAssistantJS, &beforeAssistantText))

	// 3. Locate composer element
	composerSelector := `textarea, div[contenteditable="true"], [role="textbox"]`
	var foundComposer bool
	findComposerJS := `(() => {
		const candidates = ['textarea', 'div[contenteditable="true"]', '[role="textbox"]'];
		for (const sel of candidates) {
			const els = Array.from(document.querySelectorAll(sel)).filter(e => e.getBoundingClientRect().width > 0);
			if (els.length > 0) return true;
		}
		return false;
	})()`

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = chromedp.Run(callCtx, chromedp.Evaluate(findComposerJS, &foundComposer))
		if foundComposer {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !foundComposer {
		var pageURL string
		_ = chromedp.Run(callCtx, chromedp.Location(&pageURL))
		return "", fmt.Errorf("composer not found on page (current URL: %s)", pageURL)
	}

	// 4. Fill composer and trigger React input state via both keystroke and native values
	fillAndTypeJS := fmt.Sprintf(`(() => {
		const candidates = ['textarea', 'div[contenteditable="true"]', '[role="textbox"]'];
		let el = null;
		for (const sel of candidates) {
			const els = Array.from(document.querySelectorAll(sel)).filter(e => e.getBoundingClientRect().width > 0);
			if (els.length > 0) {
				els.sort((a, b) => b.getBoundingClientRect().top - a.getBoundingClientRect().top);
				el = els[0];
				break;
			}
		}
		if (!el) return false;

		el.focus();
		if (el.tagName.toLowerCase() === 'textarea' || el.tagName.toLowerCase() === 'input') {
			const proto = el.tagName.toLowerCase() === 'textarea' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
			const nativeSet = Object.getOwnPropertyDescriptor(proto, 'value').set;
			nativeSet.call(el, %q);
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		} else {
			el.innerText = %q;
			el.dispatchEvent(new Event('input', { bubbles: true }));
		}
		return true;
	})()`, prompt, prompt)

	_ = chromedp.Run(callCtx,
		chromedp.Focus(composerSelector),
		chromedp.Evaluate(fillAndTypeJS, nil),
	)
	time.Sleep(400 * time.Millisecond)

	// 5. Submit via send button click or keyboard Enter
	sendJS := `(() => {
		// Look for buttons near composer that are enabled
		const btns = Array.from(document.querySelectorAll('button:not([disabled]), [role="button"]:not([disabled])')).filter(b => {
			const rect = b.getBoundingClientRect();
			return rect.width > 0 && rect.height > 0;
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
	time.Sleep(500 * time.Millisecond)

	// 6. Poll for NEW reply completion (must differ from beforeAssistantText or wait for new generation)
	return s.waitForReply(callCtx, beforeAssistantText)
}

func (s *Session) waitForReply(ctx context.Context, beforeText string) (string, error) {
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()

	var lastText string
	settleCount := 0
	hasStartedGenerating := false

	// JS to retrieve latest assistant message text
	extractJS := `(() => {
		// Scroll to bottom
		const scroller = document.getElementById('hatch-chat-scroll') || document.querySelector('[class*="overflow-y-auto"]');
		if (scroller) scroller.scrollTop = scroller.scrollHeight;

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
			if lastText != "" && lastText != beforeText {
				return lastText, nil
			}
			return "", ctx.Err()
		case <-ticker.C:
			var isGenerating bool
			_ = chromedp.Run(ctx, chromedp.Evaluate(isGeneratingJS, &isGenerating))
			if isGenerating {
				hasStartedGenerating = true
			}

			var curText string
			err := chromedp.Run(ctx, chromedp.Evaluate(extractJS, &curText))
			if err != nil || curText == "" {
				continue
			}

			curText = strings.TrimSpace(curText)

			// CRITICAL: If new generation hasn't started and text is identical to previous bubble, keep waiting!
			if !hasStartedGenerating && curText == beforeText && beforeText != "" {
				continue
			}

			if curText != beforeText {
				hasStartedGenerating = true
			}

			if curText == lastText && curText != "" {
				settleCount++
				// Generation settled
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
