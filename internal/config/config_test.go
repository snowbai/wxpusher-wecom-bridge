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
