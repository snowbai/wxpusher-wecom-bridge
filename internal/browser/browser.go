package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const (
	defaultSummaryMaxChars   = 1000
	screenshotViewportWidth  = 1280
	screenshotViewportHeight = 900
)

type Config struct {
	Enabled         bool
	ChromePath      string
	ScreenshotDir   string
	Timeout         time.Duration
	SummaryMaxChars int
}

type Result struct {
	URL        string
	Title      string
	Summary    string
	Screenshot string
}

type Fetcher struct {
	cfg Config
}

func NewFetcher(cfg Config) *Fetcher {
	return &Fetcher{cfg: cfg}
}

var urlRE = regexp.MustCompile(`https?://[^\s<>"']+`)

func ExtractURLs(text string) []string {
	matches := urlRE.FindAllString(text, -1)
	urls := make([]string, 0, len(matches))

	for _, match := range matches {
		candidate := strings.TrimRight(match, " .,;!?)，。；！）]")
		parsed, err := url.ParseRequestURI(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		urls = append(urls, candidate)
	}

	return urls
}

func TrimSummary(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	trimmed := strings.TrimSpace(text)
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes])
}

func effectiveSummaryLimit(maxChars int) int {
	if maxChars <= 0 {
		return defaultSummaryMaxChars
	}
	return maxChars
}

func validateFetchURL(pageURL string) error {
	parsed, err := url.ParseRequestURI(pageURL)
	if err != nil {
		return fmt.Errorf("invalid fetch URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported fetch URL scheme %q", parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return errors.New("fetch URL host is required")
	}
	if strings.TrimSuffix(host, ".") == "localhost" {
		return errors.New("fetch URL host localhost is not allowed")
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return nil
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() {
		return fmt.Errorf("fetch URL IP %s is not allowed", addr)
	}

	return nil
}

func boundedBodyTextExpression(maxChars int) string {
	limit := effectiveSummaryLimit(maxChars)
	return fmt.Sprintf(`(() => {
  const root = document.body || document.documentElement;
  if (!root) return "";
  const limit = %d;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const out = [];
  let count = 0;
  let node;
  while ((node = walker.nextNode()) && count < limit) {
    for (const ch of (node.nodeValue || "")) {
      out.push(ch);
      count++;
      if (count >= limit) break;
    }
  }
  return out.join("");
})()`, limit)
}

func (f *Fetcher) Fetch(ctx context.Context, pageURL string) (Result, error) {
	if err := validateFetchURL(pageURL); err != nil {
		return Result{}, err
	}

	cfg := f.cfg
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.ScreenshotDir == "" {
		cfg.ScreenshotDir = "."
	}

	if err := os.MkdirAll(cfg.ScreenshotDir, 0o755); err != nil {
		return Result{}, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Headless,
		chromedp.DisableGPU,
	)
	if cfg.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(cfg.ChromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(timeoutCtx, opts...)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	limit := effectiveSummaryLimit(cfg.SummaryMaxChars)
	var title string
	var body string
	var screenshot []byte
	if err := chromedp.Run(browserCtx,
		chromedp.EmulateViewport(screenshotViewportWidth, screenshotViewportHeight),
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Evaluate(boundedBodyTextExpression(limit), &body),
		chromedp.CaptureScreenshot(&screenshot),
	); err != nil {
		return Result{}, err
	}

	sum := sha256.Sum256([]byte(pageURL))
	screenshotPath := filepath.Join(cfg.ScreenshotDir, hex.EncodeToString(sum[:])+".png")
	if err := os.WriteFile(screenshotPath, screenshot, 0o644); err != nil {
		return Result{}, err
	}

	return Result{
		URL:        pageURL,
		Title:      title,
		Summary:    TrimSummary(body, limit),
		Screenshot: screenshotPath,
	}, nil
}
