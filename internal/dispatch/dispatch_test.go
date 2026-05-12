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
	err        error
	imageErr   error
	sent       []string
	imagePaths []string
	calls      int
	imageCalls int
}

func (s *fakeSender) SendMarkdown(_ context.Context, content string) error {
	s.calls++
	s.sent = append(s.sent, content)
	return s.err
}

func (s *fakeSender) SendImageFile(_ context.Context, path string) error {
	s.imageCalls++
	s.imagePaths = append(s.imagePaths, path)
	return s.imageErr
}

type contextCanceledSender struct {
	cancel context.CancelFunc
	calls  int
}

func (s *contextCanceledSender) SendMarkdown(context.Context, string) error {
	s.calls++
	if s.cancel != nil {
		s.cancel()
	}
	return context.Canceled
}

func (s *contextCanceledSender) SendImageFile(context.Context, string) error {
	s.calls++
	if s.cancel != nil {
		s.cancel()
	}
	return context.Canceled
}

type cancelingSender struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelingSender) SendMarkdown(_ context.Context, _ string) error {
	s.calls++
	if s.calls == 1 {
		s.cancel()
	}
	return nil
}

func (s *cancelingSender) SendImageFile(_ context.Context, _ string) error {
	s.calls++
	if s.calls == 1 {
		s.cancel()
	}
	return nil
}

type contextCanceledFetcher struct {
	cancel context.CancelFunc
	calls  int
}

func (f *contextCanceledFetcher) Fetch(context.Context, string) (EnrichmentResult, error) {
	f.calls++
	if f.cancel != nil {
		f.cancel()
	}
	return EnrichmentResult{}, context.Canceled
}

type cancelingFetcher struct {
	cancel context.CancelFunc
	calls  int
}

func (f *cancelingFetcher) Fetch(_ context.Context, url string) (EnrichmentResult, error) {
	f.calls++
	if f.calls == 1 {
		f.cancel()
	}
	return EnrichmentResult{URL: url, Title: "Title", Summary: "summary", Screenshot: "/tmp/shot.png"}, nil
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

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC().Add(time.Second))
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

	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, time.Now().UTC().Add(time.Second))
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

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("delivery tasks = %d, want 1", len(deliveries))
	}
	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, time.Now().UTC().Add(time.Second))
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
	claimed, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d tasks after success, want 0", len(claimed))
	}
}

func TestDeliveryWorkerSendsScreenshotImageFile(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	sender := &fakeSender{}
	svc := New(st, sender, nil, Config{})
	msg, err := st.SaveMessage(ctx, store.Message{QID: "q-image", MsgType: 20001, Content: "hello", ReceivedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("SaveMessage() error = %v", err)
	}
	if _, err := st.EnqueueDelivery(ctx, store.DeliveryTask{
		MessageID:   msg.ID,
		Kind:        store.DeliveryScreenshot,
		Payload:     "/tmp/screenshot.jpg",
		NextAttempt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("EnqueueDelivery() error = %v", err)
	}

	if err := svc.processDeliveryOnce(ctx); err != nil {
		t.Fatalf("processDeliveryOnce() error = %v", err)
	}

	if sender.imageCalls != 1 {
		t.Fatalf("image calls = %d, want 1", sender.imageCalls)
	}
	if len(sender.imagePaths) != 1 || sender.imagePaths[0] != "/tmp/screenshot.jpg" {
		t.Fatalf("image paths = %#v, want /tmp/screenshot.jpg", sender.imagePaths)
	}
}

func TestDeliveryTaskReturningContextCanceledIsReleased(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := openTestStore(t)
	sender := &contextCanceledSender{cancel: cancel}
	svc := New(st, sender, nil, Config{WeComMaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	now := time.Now().UTC()

	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-current-cancel-delivery", MsgType: 20001, Content: "current", ReceivedAt: now}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	if err := svc.processDeliveryOnce(ctx); err != nil {
		t.Fatalf("processDeliveryOnce() error = %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.calls)
	}

	claimed, err := st.ClaimDeliveryTasks(context.Background(), 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimable delivery tasks = %d, want 1", len(claimed))
	}
	if !strings.Contains(claimed[0].Payload, "q-current-cancel-delivery") {
		t.Fatalf("claimable delivery payload = %q, want current canceled message", claimed[0].Payload)
	}
}

func TestDeliveryProcessCancellationLeavesUnprocessedTaskClaimable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := openTestStore(t)
	sender := &cancelingSender{cancel: cancel}
	svc := New(st, sender, nil, Config{})
	now := time.Now().UTC()

	if err := svc.HandleMessage(context.Background(), IncomingMessage{QID: "q-cancel-delivery-1", MsgType: 20001, Content: "first", ReceivedAt: now}); err != nil {
		t.Fatalf("first HandleMessage() error = %v", err)
	}
	if err := svc.HandleMessage(context.Background(), IncomingMessage{QID: "q-cancel-delivery-2", MsgType: 20001, Content: "second", ReceivedAt: now}); err != nil {
		t.Fatalf("second HandleMessage() error = %v", err)
	}

	err := svc.processDeliveryOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("processDeliveryOnce() error = %v, want nil or context.Canceled", err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls = %d, want 1", sender.calls)
	}

	claimed, err := st.ClaimDeliveryTasks(context.Background(), 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimable delivery tasks = %d, want 1", len(claimed))
	}
	if !strings.Contains(claimed[0].Payload, "q-cancel-delivery-2") {
		t.Fatalf("claimable delivery payload = %q, want second message", claimed[0].Payload)
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
	retry, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC().Add(time.Second))
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
	tasks, err := st.ClaimEnrichmentTasks(ctx, 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("enrichment tasks = %d, want 1", len(tasks))
	}

	if err := svc.processEnrichmentTask(ctx, tasks[0]); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v", err)
	}

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 3 {
		t.Fatalf("delivery tasks = %d, want original, enriched, and screenshot", len(deliveries))
	}
	if deliveries[1].Kind != store.DeliveryEnriched {
		t.Fatalf("second delivery kind = %q, want %q", deliveries[1].Kind, store.DeliveryEnriched)
	}
	wantPayload := wecom.BuildEnrichedMarkdown("message-1", "https://example.com", "Example", "summary", "/tmp/example.png").Content
	if deliveries[1].Payload != wantPayload {
		t.Fatalf("enriched payload = %q, want %q", deliveries[1].Payload, wantPayload)
	}
	if deliveries[2].Kind != store.DeliveryScreenshot {
		t.Fatalf("third delivery kind = %q, want %q", deliveries[2].Kind, store.DeliveryScreenshot)
	}
	if deliveries[2].Payload != "/tmp/example.png" {
		t.Fatalf("screenshot payload = %q, want /tmp/example.png", deliveries[2].Payload)
	}
}

func TestEnrichmentTaskReturningContextCanceledIsReleased(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := openTestStore(t)
	fetcher := &contextCanceledFetcher{cancel: cancel}
	svc := New(st, nil, fetcher, Config{EnrichmentMaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	now := time.Now().UTC()

	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-current-cancel-enrich", MsgType: 20001, Content: "current https://current.example", ReceivedAt: now}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}
	if err := svc.processEnrichmentOnce(ctx); err != nil {
		t.Fatalf("processEnrichmentOnce() error = %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1", fetcher.calls)
	}

	claimed, err := st.ClaimEnrichmentTasks(context.Background(), 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimable enrichment tasks = %d, want 1", len(claimed))
	}
	if claimed[0].URL != "https://current.example" {
		t.Fatalf("claimable enrichment URL = %q, want https://current.example", claimed[0].URL)
	}
}

func TestEnrichmentProcessCancellationLeavesUnprocessedTaskClaimable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := openTestStore(t)
	fetcher := &cancelingFetcher{cancel: cancel}
	svc := New(st, nil, fetcher, Config{})
	now := time.Now().UTC()

	if err := svc.HandleMessage(context.Background(), IncomingMessage{QID: "q-cancel-enrich-1", MsgType: 20001, Content: "first https://one.example", ReceivedAt: now}); err != nil {
		t.Fatalf("first HandleMessage() error = %v", err)
	}
	if err := svc.HandleMessage(context.Background(), IncomingMessage{QID: "q-cancel-enrich-2", MsgType: 20001, Content: "second https://two.example", ReceivedAt: now}); err != nil {
		t.Fatalf("second HandleMessage() error = %v", err)
	}

	err := svc.processEnrichmentOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("processEnrichmentOnce() error = %v, want nil or context.Canceled", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetcher calls = %d, want 1", fetcher.calls)
	}

	claimed, err := st.ClaimEnrichmentTasks(context.Background(), 10, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimable enrichment tasks = %d, want 1", len(claimed))
	}
	if claimed[0].URL != "https://two.example" {
		t.Fatalf("claimable enrichment URL = %q, want https://two.example", claimed[0].URL)
	}
}

func TestEnrichmentSuccessUsesFreshContextForMarkDone(t *testing.T) {
	ctx := context.Background()
	st := &enrichmentContextTrackingStore{}
	svc := New(st, nil, &fakeFetcher{result: EnrichmentResult{Title: "Example", Summary: "summary"}}, Config{})
	task := store.EnrichmentTask{ID: 9, MessageID: 42, URL: "https://example.com"}

	if err := svc.processEnrichmentTask(ctx, task); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v", err)
	}
	if st.markDoneCalls != 1 {
		t.Fatalf("MarkEnrichmentDone calls = %d, want 1", st.markDoneCalls)
	}
}

func TestEnrichmentSaveFailureReleasesTaskForRetry(t *testing.T) {
	ctx := context.Background()
	st := &enrichmentSaveFailStore{saveErr: errors.New("save failed")}
	svc := New(st, nil, &fakeFetcher{result: EnrichmentResult{Title: "Example", Summary: "summary"}}, Config{EnrichmentMaxAttempts: 2})
	task := store.EnrichmentTask{ID: 11, MessageID: 42, URL: "https://example.com"}

	if err := svc.processEnrichmentTask(ctx, task); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v, want handled retry", err)
	}
	if st.retryCalls != 1 {
		t.Fatalf("MarkEnrichmentRetry calls = %d, want 1", st.retryCalls)
	}
	if st.retryAttempts != 1 {
		t.Fatalf("retry attempts = %d, want 1", st.retryAttempts)
	}
	if st.markDoneCalls != 0 {
		t.Fatalf("MarkEnrichmentDone calls = %d, want 0", st.markDoneCalls)
	}
}

func TestEnrichmentMarkDoneFailureReleasesTaskForRetry(t *testing.T) {
	ctx := context.Background()
	st := &enrichmentMarkDoneFailStore{markDoneErr: context.DeadlineExceeded}
	svc := New(st, nil, &fakeFetcher{result: EnrichmentResult{Title: "Example", Summary: "summary"}}, Config{EnrichmentMaxAttempts: 2})
	task := store.EnrichmentTask{ID: 12, MessageID: 42, URL: "https://example.com"}

	if err := svc.processEnrichmentTask(ctx, task); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v, want handled retry", err)
	}
	if st.markDoneCalls != 1 {
		t.Fatalf("MarkEnrichmentDone calls = %d, want 1", st.markDoneCalls)
	}
	if st.retryCalls != 1 {
		t.Fatalf("MarkEnrichmentRetry calls = %d, want 1", st.retryCalls)
	}
	if st.retryAttempts != 1 {
		t.Fatalf("retry attempts = %d, want 1", st.retryAttempts)
	}
}

func TestEnrichmentWorkerDoesNotMarkDoneWhenEnrichedDeliveryEnqueueFails(t *testing.T) {
	ctx := context.Background()
	st := &enrichmentEnqueueFailStore{enqueueErr: errors.New("enqueue failed")}
	svc := New(st, nil, &fakeFetcher{result: EnrichmentResult{Title: "Example", Summary: "summary"}}, Config{EnrichmentMaxAttempts: 2})
	task := store.EnrichmentTask{ID: 7, MessageID: 42, URL: "https://example.com"}

	if err := svc.processEnrichmentTask(ctx, task); err != nil {
		t.Fatalf("processEnrichmentTask() error = %v, want handled retry", err)
	}
	if st.markDoneCalls != 0 {
		t.Fatalf("MarkEnrichmentDone calls = %d, want 0", st.markDoneCalls)
	}
	if st.retryCalls != 1 {
		t.Fatalf("MarkEnrichmentRetry calls = %d, want 1", st.retryCalls)
	}
	if st.retryAttempts != 1 {
		t.Fatalf("retry attempts = %d, want 1", st.retryAttempts)
	}
}

func TestProcessDeliveryOncePreCanceledContextDoesNotClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &claimCountingStore{}
	svc := New(st, &fakeSender{}, nil, Config{})

	err := svc.processDeliveryOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("processDeliveryOnce() error = %v, want context.Canceled", err)
	}
	if st.deliveryClaims != 0 {
		t.Fatalf("delivery claims = %d, want 0", st.deliveryClaims)
	}
}

func TestProcessEnrichmentOncePreCanceledContextDoesNotClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &claimCountingStore{}
	svc := New(st, nil, &fakeFetcher{}, Config{})

	err := svc.processEnrichmentOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("processEnrichmentOnce() error = %v, want context.Canceled", err)
	}
	if st.enrichmentClaims != 0 {
		t.Fatalf("enrichment claims = %d, want 0", st.enrichmentClaims)
	}
}

func TestNextAttemptCapsFirstRetryWhenInitialBackoffExceedsMax(t *testing.T) {
	svc := New(nil, nil, nil, Config{InitialBackoff: 10 * time.Second, MaxBackoff: 2 * time.Second})
	before := time.Now().UTC()
	next := svc.nextAttempt(1)
	delay := next.Sub(before)
	if delay < 1500*time.Millisecond || delay > 2500*time.Millisecond {
		t.Fatalf("first retry delay = %s, want about 2s", delay)
	}
}

func TestHandleMessageFutureReceivedAtDoesNotDelayQueuedTasks(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	svc := New(st, nil, nil, Config{})
	now := time.Now().UTC()
	future := now.Add(24 * time.Hour)

	if err := svc.HandleMessage(ctx, IncomingMessage{QID: "q-future", MsgType: 20001, Content: "hello https://example.com", ReceivedAt: future}); err != nil {
		t.Fatalf("HandleMessage() error = %v", err)
	}

	deliveries, err := st.ClaimDeliveryTasks(ctx, 10, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimDeliveryTasks() error = %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("delivery tasks = %d, want 1", len(deliveries))
	}
	enrichments, err := st.ClaimEnrichmentTasks(ctx, 10, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimEnrichmentTasks() error = %v", err)
	}
	if len(enrichments) != 1 {
		t.Fatalf("enrichment tasks = %d, want 1", len(enrichments))
	}
}

func TestHandleMessageNilStoreReturnsError(t *testing.T) {
	svc := New(nil, nil, nil, Config{})
	err := svc.HandleMessage(context.Background(), IncomingMessage{QID: "q-nil", Content: "hello"})
	if err == nil {
		t.Fatal("HandleMessage() error = nil, want missing store error")
	}
	if !strings.Contains(err.Error(), "store") {
		t.Fatalf("HandleMessage() error = %v, want store error", err)
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

type enrichmentContextTrackingStore struct {
	store.Store
	saveCtx       context.Context
	enqueueCtx    context.Context
	markDoneCalls int
}

func (s *enrichmentContextTrackingStore) SaveEnrichment(ctx context.Context, _ store.Enrichment) error {
	s.saveCtx = ctx
	return nil
}

func (s *enrichmentContextTrackingStore) EnqueueDelivery(ctx context.Context, _ store.DeliveryTask) (int64, error) {
	s.enqueueCtx = ctx
	return 1, nil
}

func (s *enrichmentContextTrackingStore) MarkEnrichmentDone(ctx context.Context, _ int64) error {
	s.markDoneCalls++
	if ctx == s.saveCtx || ctx == s.enqueueCtx {
		return errors.New("MarkEnrichmentDone reused prior status context")
	}
	return nil
}

type enrichmentSaveFailStore struct {
	store.Store
	saveErr       error
	markDoneCalls int
	retryCalls    int
	retryAttempts int
}

func (s *enrichmentSaveFailStore) SaveEnrichment(context.Context, store.Enrichment) error {
	return s.saveErr
}

func (s *enrichmentSaveFailStore) EnqueueDelivery(context.Context, store.DeliveryTask) (int64, error) {
	return 1, nil
}

func (s *enrichmentSaveFailStore) MarkEnrichmentDone(context.Context, int64) error {
	s.markDoneCalls++
	return nil
}

func (s *enrichmentSaveFailStore) MarkEnrichmentRetry(_ context.Context, _ int64, attempts int, _ time.Time, _ string) error {
	s.retryCalls++
	s.retryAttempts = attempts
	return nil
}

func (s *enrichmentSaveFailStore) MarkEnrichmentFailed(context.Context, int64, string) error {
	return nil
}

type enrichmentMarkDoneFailStore struct {
	store.Store
	markDoneErr   error
	markDoneCalls int
	retryCalls    int
	retryAttempts int
}

func (s *enrichmentMarkDoneFailStore) SaveEnrichment(context.Context, store.Enrichment) error {
	return nil
}

func (s *enrichmentMarkDoneFailStore) EnqueueDelivery(context.Context, store.DeliveryTask) (int64, error) {
	return 1, nil
}

func (s *enrichmentMarkDoneFailStore) MarkEnrichmentDone(context.Context, int64) error {
	s.markDoneCalls++
	return s.markDoneErr
}

func (s *enrichmentMarkDoneFailStore) MarkEnrichmentRetry(_ context.Context, _ int64, attempts int, _ time.Time, _ string) error {
	s.retryCalls++
	s.retryAttempts = attempts
	return nil
}

func (s *enrichmentMarkDoneFailStore) MarkEnrichmentFailed(context.Context, int64, string) error {
	return nil
}

type enrichmentEnqueueFailStore struct {
	store.Store
	enqueueErr    error
	markDoneCalls int
	retryCalls    int
	retryAttempts int
}

func (s *enrichmentEnqueueFailStore) SaveEnrichment(context.Context, store.Enrichment) error {
	return nil
}

func (s *enrichmentEnqueueFailStore) EnqueueDelivery(context.Context, store.DeliveryTask) (int64, error) {
	return 0, s.enqueueErr
}

func (s *enrichmentEnqueueFailStore) MarkEnrichmentDone(context.Context, int64) error {
	s.markDoneCalls++
	return nil
}

func (s *enrichmentEnqueueFailStore) MarkEnrichmentRetry(_ context.Context, _ int64, attempts int, _ time.Time, _ string) error {
	s.retryCalls++
	s.retryAttempts = attempts
	return nil
}

func (s *enrichmentEnqueueFailStore) MarkEnrichmentFailed(context.Context, int64, string) error {
	return nil
}

type claimCountingStore struct {
	store.Store
	deliveryClaims   int
	enrichmentClaims int
}

func (s *claimCountingStore) ClaimDeliveryTasks(context.Context, int, time.Time) ([]store.DeliveryTask, error) {
	s.deliveryClaims++
	return nil, nil
}

func (s *claimCountingStore) ClaimEnrichmentTasks(context.Context, int, time.Time) ([]store.EnrichmentTask, error) {
	s.enrichmentClaims++
	return nil, nil
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
