package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wecom"
)

type fakeSender struct {
	err   error
	sent  []string
	calls int
}

func (s *fakeSender) SendMarkdown(_ context.Context, content string) error {
	s.calls++
	s.sent = append(s.sent, content)
	return s.err
}

type fakeFetcher struct {
	result EnrichmentResult
	err    error
	calls  int
}

func (f *fakeFetcher) Fetch(_ context.Context, url string) (EnrichmentResult, error) {
	f.calls++
	if f.result.URL == "" {
		f.result.URL = url
	}
	return f.result, f.err
}

func TestHandleMessagePersistsAndQueues(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	svc := New(st, nil, nil, Config{WeComMaxAttempts: 3, EnrichmentMaxAttempts: 2})
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)

	err := svc.HandleMessage(ctx, IncomingMessage{
		QID:        "q1",
		MsgType:    20001,
		Content:    "hello https://example.com",
		RawPayload: []byte(`{}`),
		ReceivedAt: now,
	})
	if err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("delivery tasks = %d, want 1", len(deliveries))
	}
	if deliveries[0].Kind != store.DeliveryOriginal {
		t.Fatalf("delivery kind = %q, want %q", deliveries[0].Kind, store.DeliveryOriginal)
	}
	wantPayload := wecom.BuildOriginalMarkdown("q1", "hello https://example.com").Content
	if deliveries[0].Payload != wantPayload {
		t.Fatalf("delivery payload = %q, want %q", deliveries[0].Payload, wantPayload)
	}

	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(enrichments) != 1 {
		t.Fatalf("enrichment tasks = %d, want 1", len(enrichments))
	}
	if enrichments[0].URL != "https://example.com" {
		t.Fatalf("enrichment URL = %q, want https://example.com", enrichments[0].URL)
	}
}

func TestHandleMessageDuplicateDoesNotEnqueueDuplicateTasks(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	svc := New(st, nil, nil, Config{})
	now := time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC)
	msg := IncomingMessage{
		QID:        "q-duplicate",
		MsgType:    20001,
		Content:    "hello https://example.com",
		RawPayload: []byte(`{}`),
		ReceivedAt: now,
	}

	if err := svc.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("first HandleMessage() error = %v", err)
	}
	if err := svc.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("second HandleMessage() error = %v", err)
	}

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("delivery tasks = %d, want 1", len(deliveries))
	}
	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(enrichments) != 1 {
		t.Fatalf("enrichment tasks = %d, want 1", len(enrichments))
	}
}

func TestDeliveryWorkerSendsAndMarksDone(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sender := &fakeSender{}
	svc := New(st, sender, nil, Config{})
	now := time.Date(2026, 5, 11, 10, 10, 0, 0, time.UTC)
	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-deliver", MsgType: 20001, Content: "hello", ReceivedAt: now}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	if err := svc.processDeliveryOnce(ctx); err != nil {
		t.Fatalf("processDeliveryOnce() error = %v", err)
	}

	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.calls)
	}
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0], "q-deliver") {
		t.Fatalf("sent payloads = %#v, want one payload containing q-deliver", sender.sent)
	}
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks after success, want 0", len(claimed))
	}
}

func TestDeliveryWorkerRetriesThenFails(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sender := &fakeSender{err: errors.New("wecom down")}
	svc := New(st, sender, nil, Config{WeComMaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	now := time.Date(2026, 5, 11, 10, 15, 0, 0, time.UTC)
	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-retry", MsgType: 20001, Content: "hello", ReceivedAt: now}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	if err := svc.processDeliveryOnce(ctx); err != nil {
		t.Fatalf("first processDeliveryOnce() error = %v", err)
	}
	retry, err := st.ClaimDeliveryTasks(ctx, 10, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(retry) != 1 {
		t.Fatalf("retry tasks = %d, want 1", len(retry))
	}
	if retry[0].Attempts != 1 || retry[0].LastError != "wecom down" {
		t.Fatalf("retry task = %+v, want attempts=1 last_error=wecom down", retry[0])
	}

	if err := svc.processDeliveryTask(ctx, retry[0]); err != nil {
		t.Fatalf("second processDeliveryTask() error = %v", err)
	}
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("final ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks after max attempts, want 0", len(claimed))
	}
}

func TestEnrichmentWorkerCreatesEnrichmentAndEnrichedDelivery(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	fetcher := &fakeFetcher{result: EnrichmentResult{
		Title:      "Example",
		Summary:    "summary",
		Screenshot: "/tmp/example.png",
	}}
	svc := New(st, nil, fetcher, Config{})
	now := time.Date(2026, 5, 11, 10, 20, 0, 0, time.UTC)
	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-enrich", MsgType: 20001, Content: "see https://example.com", ReceivedAt: now}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	tasks, err := st.ClaimEnrichmentTasks(ctx, 10, now)
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("enrichment tasks = %d, want 1", len(tasks))
	}

	if err := svc.processEnrichmentTask(ctx, tasks[0]); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v", err)
	}

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("delivery tasks = %d, want original and enriched", len(deliveries))
	}
	if deliveries[1].Kind != store.DeliveryEnriched {
		t.Fatalf("second delivery kind = %q, want %q", deliveries[1].Kind, store.DeliveryEnriched)
	}
	wantPayload := wecom.BuildEnrichedMarkdown("message-1", "https://example.com", "Example", "summary", "/tmp/example.png").Content
	if deliveries[1].Payload != wantPayload {
		t.Fatalf("enriched payload = %q, want %q", deliveries[1].Payload, wantPayload)
	}
}

func TestWorkersRequireDependencies(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if err := New(st, nil, nil, Config{}).RunDeliveryWorker(ctx, time.Millisecond); err == nil {
		t.Fatal("RunDeliveryWorker() error = nil, want missing sender error")
	}
	if err := New(st, nil, nil, Config{}).RunEnrichmentWorker(ctx, time.Millisecond); err == nil {
		t.Fatal("RunEnrichmentWorker() error = nil, want missing fetcher error")
	}
}

func openTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	st, err := store.OpenSQLite(t.TempDir() + "/store.db")
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return st
}
