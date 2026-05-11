package browser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
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
		candidate := strings.TrimRight(match, " .,;!?)，。；！）")
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

func (f *Fetcher) Fetch(ctx context.Context, pageURL string) (Result, error) {
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

	var title string
	var body string
	var screenshot []byte
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(pageURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Text("body", &body, chromedp.ByQuery),
		chromedp.FullScreenshot(&screenshot, 90),
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
		Summary:    TrimSummary(body, cfg.SummaryMaxChars),
		Screenshot: screenshotPath,
	}, nil
}
