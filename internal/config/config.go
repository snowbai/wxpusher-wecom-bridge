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
	platform := "Chrome-Other"
	switch runtime.GOOS {
	case "linux":
		platform = "Chrome-Linux"
	case "darwin":
		platform = "Chrome-Mac"
	}

	return Config{
		WxPusher: WxPusherConfig{
			Host:     "wxpusher.zjiecode.com",
			Version:  "1.1.0",
			Platform: platform,
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
	if c.WxPusher.Host == "" {
		problems = append(problems, "wxpusher.host is required")
	}
	if c.WxPusher.Version == "" {
		problems = append(problems, "wxpusher.version is required")
	}
	if c.WxPusher.Platform == "" {
		problems = append(problems, "wxpusher.platform is required")
	}
	if c.Storage.SQLitePath == "" {
		problems = append(problems, "storage.sqlite_path is required")
	}
	if c.WeCom.WebhookURL == "" {
		problems = append(problems, "wecom webhook URL is required through wecom.webhook_url or wecom.webhook_env")
	}
	if c.Retry.WeComMaxAttempts < 1 {
		problems = append(problems, "retry.wecom_max_attempts must be >= 1")
	}
	if c.Retry.EnrichmentMaxAttempts < 1 {
		problems = append(problems, "retry.enrichment_max_attempts must be >= 1")
	}
	if c.Browser.Enabled && c.Browser.TimeoutSeconds < 1 {
		problems = append(problems, "browser.timeout_seconds must be >= 1 when browser is enabled")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
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
