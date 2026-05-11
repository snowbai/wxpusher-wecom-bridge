package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndEnvWebhook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(`
[storage]
sqlite_path = "`+filepath.Join(dir, "bridge.db")+`"

[wecom]
webhook_env = "TEST_WECOM_WEBHOOK"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_WECOM_WEBHOOK", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.WxPusher.Host != "wxpusher.zjiecode.com" {
		t.Fatalf("host = %q", cfg.WxPusher.Host)
	}
	if cfg.WxPusher.Version != "1.1.0" {
		t.Fatalf("version = %q", cfg.WxPusher.Version)
	}
	if cfg.WeCom.WebhookURL != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc" {
		t.Fatalf("webhook url not resolved from env")
	}
}

func TestValidateRejectsMissingWebhook(t *testing.T) {
	cfg := Default()
	cfg.Storage.SQLitePath = filepath.Join(t.TempDir(), "bridge.db")
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected missing webhook error")
	}
}

func TestValidateRejectsWhitespaceRequiredStrings(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{
			name: "wxpusher host",
			edit: func(cfg *Config) {
				cfg.WxPusher.Host = " \t\n"
			},
		},
		{
			name: "wxpusher version",
			edit: func(cfg *Config) {
				cfg.WxPusher.Version = " "
			},
		},
		{
			name: "wxpusher platform",
			edit: func(cfg *Config) {
				cfg.WxPusher.Platform = " "
			},
		},
		{
			name: "storage sqlite path",
			edit: func(cfg *Config) {
				cfg.Storage.SQLitePath = " "
			},
		},
		{
			name: "wecom webhook url",
			edit: func(cfg *Config) {
				cfg.WeCom.WebhookURL = " "
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateRejectsInvalidRetryBackoff(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{
			name: "initial backoff less than one",
			edit: func(cfg *Config) {
				cfg.Retry.InitialBackoffSeconds = 0
			},
		},
		{
			name: "max backoff less than initial",
			edit: func(cfg *Config) {
				cfg.Retry.InitialBackoffSeconds = 10
				cfg.Retry.MaxBackoffSeconds = 9
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateRejectsInvalidEnabledBrowserConfig(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
	}{
		{
			name: "empty chrome path",
			edit: func(cfg *Config) {
				cfg.Browser.ChromePath = " "
			},
		},
		{
			name: "empty screenshot dir",
			edit: func(cfg *Config) {
				cfg.Browser.ScreenshotDir = " "
			},
		},
		{
			name: "timeout less than one",
			edit: func(cfg *Config) {
				cfg.Browser.TimeoutSeconds = 0
			},
		},
		{
			name: "summary max chars less than one",
			edit: func(cfg *Config) {
				cfg.Browser.SummaryMaxChars = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func validConfig() Config {
	cfg := Default()
	cfg.WeCom.WebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=abc"
	return cfg
}
