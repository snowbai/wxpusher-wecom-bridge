package config

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	WxPusher WxPusherConfig `toml:"wxpusher"`
	Receiver ReceiverConfig `toml:"receiver"`
	Storage  StorageConfig  `toml:"storage"`
	WeCom    WeComConfig    `toml:"wecom"`
	Browser  BrowserConfig  `toml:"browser"`
	Retry    RetryConfig    `toml:"retry"`
}

type WxPusherConfig struct {
	Host     string `toml:"host"`
	Version  string `toml:"version"`
	Platform string `toml:"platform"`
}

type ReceiverConfig struct {
	Mode                string `toml:"mode"`
	CDPURL              string `toml:"cdp_url"`
	ExtensionID         string `toml:"extension_id"`
	ReadyTimeoutSeconds int    `toml:"ready_timeout_seconds"`
}

type StorageConfig struct {
	SQLitePath string `toml:"sqlite_path"`
}

type WeComConfig struct {
	WebhookURL string `toml:"webhook_url"`
	WebhookEnv string `toml:"webhook_env"`
}

type BrowserConfig struct {
	Enabled         bool   `toml:"enabled"`
	ChromePath      string `toml:"chrome_path"`
	ScreenshotDir   string `toml:"screenshot_dir"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
	SummaryMaxChars int    `toml:"summary_max_chars"`
}

type RetryConfig struct {
	WeComMaxAttempts      int `toml:"wecom_max_attempts"`
	EnrichmentMaxAttempts int `toml:"enrichment_max_attempts"`
	InitialBackoffSeconds int `toml:"initial_backoff_seconds"`
	MaxBackoffSeconds     int `toml:"max_backoff_seconds"`
}

func Default() Config {
	return Config{
		WxPusher: WxPusherConfig{
			Host:     "wxpusher.zjiecode.com",
			Version:  "1.1.0",
			Platform: platformForGOOS(runtime.GOOS),
		},
		Receiver: ReceiverConfig{
			Mode:                "chrome-cdp",
			CDPURL:              "http://127.0.0.1:9222",
			ReadyTimeoutSeconds: 60,
		},
		Storage: StorageConfig{SQLitePath: "/var/lib/wxpusher-bridge/bridge.db"},
		WeCom:   WeComConfig{WebhookEnv: "WXPUSHER_BRIDGE_WECOM_WEBHOOK_URL"},
		Browser: BrowserConfig{
			Enabled:         true,
			ChromePath:      "/usr/bin/chromium",
			ScreenshotDir:   "/var/lib/wxpusher-bridge/screenshots",
			TimeoutSeconds:  20,
			SummaryMaxChars: 1000,
		},
		Retry: RetryConfig{
			WeComMaxAttempts:      5,
			EnrichmentMaxAttempts: 3,
			InitialBackoffSeconds: 2,
			MaxBackoffSeconds:     60,
		},
	}
}

func platformForGOOS(goos string) string {
	switch goos {
	case "windows":
		return "Chrome-Windows"
	case "darwin":
		return "Chrome-Mac"
	default:
		return "Chrome-Other"
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.WeCom.WebhookURL == "" && cfg.WeCom.WebhookEnv != "" {
		cfg.WeCom.WebhookURL = os.Getenv(cfg.WeCom.WebhookEnv)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.WxPusher.Host) == "" {
		problems = append(problems, "wxpusher.host is required")
	}
	if strings.TrimSpace(c.WxPusher.Version) == "" {
		problems = append(problems, "wxpusher.version is required")
	}
	if strings.TrimSpace(c.WxPusher.Platform) == "" {
		problems = append(problems, "wxpusher.platform is required")
	}
	if c.Receiver.Mode != "chrome-cdp" && c.Receiver.Mode != "go-fallback" {
		problems = append(problems, "receiver.mode must be chrome-cdp or go-fallback")
	}
	if c.Receiver.Mode == "chrome-cdp" {
		if strings.TrimSpace(c.Receiver.CDPURL) == "" {
			problems = append(problems, "receiver.cdp_url is required when receiver.mode is chrome-cdp")
		}
		if err := validateChromeExtensionID(c.Receiver.ExtensionID); err != nil {
			problems = append(problems, "receiver.extension_id "+err.Error())
		}
		if c.Receiver.ReadyTimeoutSeconds < 1 {
			problems = append(problems, "receiver.ready_timeout_seconds must be >= 1 when receiver.mode is chrome-cdp")
		}
	}
	if strings.TrimSpace(c.Storage.SQLitePath) == "" {
		problems = append(problems, "storage.sqlite_path is required")
	}
	if strings.TrimSpace(c.WeCom.WebhookURL) == "" {
		problems = append(problems, "wecom webhook URL is required through wecom.webhook_url or wecom.webhook_env")
	}
	if c.Retry.WeComMaxAttempts < 1 {
		problems = append(problems, "retry.wecom_max_attempts must be >= 1")
	}
	if c.Retry.EnrichmentMaxAttempts < 1 {
		problems = append(problems, "retry.enrichment_max_attempts must be >= 1")
	}
	if c.Retry.InitialBackoffSeconds < 1 {
		problems = append(problems, "retry.initial_backoff_seconds must be >= 1")
	}
	if c.Retry.MaxBackoffSeconds < c.Retry.InitialBackoffSeconds {
		problems = append(problems, "retry.max_backoff_seconds must be >= retry.initial_backoff_seconds")
	}
	if c.Browser.Enabled {
		if strings.TrimSpace(c.Browser.ChromePath) == "" {
			problems = append(problems, "browser.chrome_path is required when browser is enabled")
		}
		if strings.TrimSpace(c.Browser.ScreenshotDir) == "" {
			problems = append(problems, "browser.screenshot_dir is required when browser is enabled")
		}
		if c.Browser.TimeoutSeconds < 1 {
			problems = append(problems, "browser.timeout_seconds must be >= 1 when browser is enabled")
		}
		if c.Browser.SummaryMaxChars < 1 {
			problems = append(problems, "browser.summary_max_chars must be >= 1 when browser is enabled")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func validateChromeExtensionID(extensionID string) error {
	if len(extensionID) != 32 {
		return errors.New("must be exactly 32 characters")
	}
	for _, r := range extensionID {
		if r < 'a' || r > 'p' {
			return errors.New("must contain only characters a-p")
		}
	}
	return nil
}

func RedactSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "***"
	}
	return fmt.Sprintf("%s***%s", s[:4], s[len(s)-4:])
}
