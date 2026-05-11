package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/chromereceiver"
	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wxpusher"
)

func TestBackoffCapsAtMax(t *testing.T) {
	cfg := config.Default()
	cfg.Retry.InitialBackoffSeconds = 2
	cfg.Retry.MaxBackoffSeconds = 10

	got := Backoff(cfg, 10)
	if got != 10*time.Second {
		t.Fatalf("backoff = %s", got)
	}
}

func TestStartWorkerDoesNotBlockCaller(t *testing.T) {
	release := make(chan struct{})
	errs := make(chan error, 1)

	returned := make(chan struct{})
	go func() {
		startWorker(errs, "test", func() error {
			<-release
			return context.Canceled
		})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("startWorker blocked the caller")
	}

	close(release)
}

func TestChromeCDPModeDoesNotRequireStoredIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.Receiver.Mode = "chrome-cdp"
	cfg.Receiver.ExtensionID = "abcdefghijklmnopabcdefghijklmnop"
	cfg.WeCom.WebhookURL = "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test"
	cfg.Storage.SQLitePath = filepath.Join(t.TempDir(), "bridge.db")
	cfg.Browser.Enabled = false

	st, err := store.OpenSQLite(cfg.Storage.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx, cancel := context.WithCancel(context.Background())
	a := New(cfg, slog.Default(), st)
	a.chromeReceiver = receiverFunc(func(ctx context.Context, onMessage func(wxpusher.Message), onEvent func(chromereceiver.Event)) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})

	err = a.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context canceled", err)
	}
}
