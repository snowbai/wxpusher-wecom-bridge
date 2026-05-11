package app

import (
	"context"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
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
