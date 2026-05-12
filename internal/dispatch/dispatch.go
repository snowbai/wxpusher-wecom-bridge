package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/browser"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wecom"
)

const (
	defaultInitialBackoff = 2 * time.Second
	defaultMaxBackoff     = 60 * time.Second
	defaultClaimLimit     = 1
	statusUpdateTimeout   = 5 * time.Second
)

type Sender interface {
	SendMarkdown(context.Context, string) error
	SendImageFile(context.Context, string) error
}

type Fetcher interface {
	Fetch(context.Context, string) (EnrichmentResult, error)
}

type IncomingMessage struct {
	QID        string
	MsgType    int
	Content    string
	RawPayload []byte
	ReceivedAt time.Time
}

type EnrichmentResult struct {
	URL        string
	Title      string
	Summary    string
	Screenshot string
}

type Config struct {
	WeComMaxAttempts      int
	EnrichmentMaxAttempts int
	InitialBackoff        time.Duration
	MaxBackoff            time.Duration
}

type Service struct {
	store   store.Store
	sender  Sender
	fetcher Fetcher
	cfg     Config
}

func New(st store.Store, sender Sender, fetcher Fetcher, cfg Config) *Service {
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = defaultInitialBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultMaxBackoff
	}
	return &Service{store: st, sender: sender, fetcher: fetcher, cfg: cfg}
}

func (s *Service) HandleMessage(ctx context.Context, msg IncomingMessage) error {
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	saved, err := s.store.SaveMessage(ctx, store.Message{
		QID:        msg.QID,
		MsgType:    msg.MsgType,
		Content:    msg.Content,
		RawPayload: msg.RawPayload,
		ReceivedAt: msg.ReceivedAt,
	})
	if err != nil {
		return err
	}
	if !saved.Inserted {
		return nil
	}

	now := time.Now().UTC()
	payload := wecom.BuildOriginalMarkdown(msg.QID, msg.Content).Content
	if _, err := s.store.EnqueueDelivery(ctx, store.DeliveryTask{
		MessageID:   saved.ID,
		Kind:        store.DeliveryOriginal,
		Payload:     payload,
		NextAttempt: now,
	}); err != nil {
		return err
	}

	for _, pageURL := range browser.ExtractURLs(msg.Content) {
		if _, err := s.store.EnqueueEnrichment(ctx, store.EnrichmentTask{
			MessageID:   saved.ID,
			URL:         pageURL,
			NextAttempt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RunDeliveryWorker(ctx context.Context, pollInterval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sender == nil {
		return errors.New("dispatch delivery worker requires sender")
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	for {
		if err := s.processDeliveryOnce(ctx); err != nil {
			return err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) RunEnrichmentWorker(ctx context.Context, pollInterval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.fetcher == nil {
		return errors.New("dispatch enrichment worker requires fetcher")
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	for {
		if err := s.processEnrichmentOnce(ctx); err != nil {
			return err
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Service) processDeliveryOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	tasks, err := s.store.ClaimDeliveryTasks(ctx, defaultClaimLimit, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return s.releaseDeliveryTask(ctx, task, err)
		}
		if err := s.processDeliveryTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processDeliveryTask(ctx context.Context, task store.DeliveryTask) error {
	if err := ctx.Err(); err != nil {
		return s.releaseDeliveryTask(ctx, task, err)
	}
	if s.sender == nil {
		return errors.New("dispatch delivery worker requires sender")
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	if err := s.sendDelivery(ctx, task); err != nil {
		nextAttempts := task.Attempts + 1
		statusCtx, cancel := statusContext()
		defer cancel()
		if s.cfg.WeComMaxAttempts > 0 && nextAttempts >= s.cfg.WeComMaxAttempts {
			return s.store.MarkDeliveryFailed(statusCtx, task.ID, err.Error())
		}
		return s.store.MarkDeliveryRetry(statusCtx, task.ID, nextAttempts, s.nextAttempt(nextAttempts), err.Error())
	}
	statusCtx, cancel := statusContext()
	defer cancel()
	return s.store.MarkDeliveryDone(statusCtx, task.ID, "ok")
}

func (s *Service) sendDelivery(ctx context.Context, task store.DeliveryTask) error {
	switch task.Kind {
	case store.DeliveryScreenshot:
		return s.sender.SendImageFile(ctx, task.Payload)
	default:
		return s.sender.SendMarkdown(ctx, task.Payload)
	}
}

func (s *Service) processEnrichmentOnce(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	tasks, err := s.store.ClaimEnrichmentTasks(ctx, defaultClaimLimit, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return s.releaseEnrichmentTask(ctx, task, err)
		}
		if err := s.processEnrichmentTask(ctx, task); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processEnrichmentTask(ctx context.Context, task store.EnrichmentTask) error {
	if err := ctx.Err(); err != nil {
		return s.releaseEnrichmentTask(ctx, task, err)
	}
	if s.fetcher == nil {
		return errors.New("dispatch enrichment worker requires fetcher")
	}
	if s.store == nil {
		return errors.New("dispatch service requires store")
	}
	result, err := s.fetcher.Fetch(ctx, task.URL)
	if err != nil {
		nextAttempts := task.Attempts + 1
		statusCtx, cancel := statusContext()
		defer cancel()
		if s.cfg.EnrichmentMaxAttempts > 0 && nextAttempts >= s.cfg.EnrichmentMaxAttempts {
			return s.store.MarkEnrichmentFailed(statusCtx, task.ID, err.Error())
		}
		return s.store.MarkEnrichmentRetry(statusCtx, task.ID, nextAttempts, s.nextAttempt(nextAttempts), err.Error())
	}
	if result.URL == "" {
		result.URL = task.URL
	}
	if err := withStatusContext(func(statusCtx context.Context) error {
		return s.store.SaveEnrichment(statusCtx, store.Enrichment{
			MessageID:  task.MessageID,
			URL:        result.URL,
			Title:      result.Title,
			Summary:    result.Summary,
			Screenshot: result.Screenshot,
		})
	}); err != nil {
		return s.retryOrFailEnrichment(task, err)
	}
	payload := wecom.BuildEnrichedMarkdown(
		fmt.Sprintf("message-%d", task.MessageID),
		result.URL,
		result.Title,
		result.Summary,
		result.Screenshot,
	).Content
	if err := withStatusContext(func(statusCtx context.Context) error {
		_, err := s.store.EnqueueDelivery(statusCtx, store.DeliveryTask{
			MessageID:   task.MessageID,
			Kind:        store.DeliveryEnriched,
			Payload:     payload,
			NextAttempt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		return s.retryOrFailEnrichment(task, err)
	}
	if result.Screenshot != "" {
		if err := withStatusContext(func(statusCtx context.Context) error {
			_, err := s.store.EnqueueDelivery(statusCtx, store.DeliveryTask{
				MessageID:   task.MessageID,
				Kind:        store.DeliveryScreenshot,
				Payload:     result.Screenshot,
				NextAttempt: time.Now().UTC(),
			})
			return err
		}); err != nil {
			return s.retryOrFailEnrichment(task, err)
		}
	}
	if err := withStatusContext(func(statusCtx context.Context) error {
		return s.store.MarkEnrichmentDone(statusCtx, task.ID)
	}); err != nil {
		return s.retryOrFailEnrichment(task, err)
	}
	return nil
}

func (s *Service) releaseDeliveryTask(_ context.Context, task store.DeliveryTask, cause error) error {
	statusCtx, cancel := statusContext()
	defer cancel()
	if err := s.store.MarkDeliveryRetry(statusCtx, task.ID, task.Attempts, time.Now().UTC(), cause.Error()); err != nil {
		return err
	}
	return cause
}

func (s *Service) releaseEnrichmentTask(_ context.Context, task store.EnrichmentTask, cause error) error {
	statusCtx, cancel := statusContext()
	defer cancel()
	if err := s.store.MarkEnrichmentRetry(statusCtx, task.ID, task.Attempts, time.Now().UTC(), cause.Error()); err != nil {
		return err
	}
	return cause
}

func (s *Service) retryOrFailEnrichment(task store.EnrichmentTask, cause error) error {
	nextAttempts := task.Attempts + 1
	statusCtx, cancel := statusContext()
	defer cancel()
	if s.cfg.EnrichmentMaxAttempts > 0 && nextAttempts >= s.cfg.EnrichmentMaxAttempts {
		return s.store.MarkEnrichmentFailed(statusCtx, task.ID, cause.Error())
	}
	return s.store.MarkEnrichmentRetry(statusCtx, task.ID, nextAttempts, s.nextAttempt(nextAttempts), cause.Error())
}

func withStatusContext(fn func(context.Context) error) error {
	ctx, cancel := statusContext()
	defer cancel()
	return fn(ctx)
}

func statusContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), statusUpdateTimeout)
}

func (s *Service) nextAttempt(attempts int) time.Time {
	backoff := s.cfg.InitialBackoff
	if backoff > s.cfg.MaxBackoff {
		backoff = s.cfg.MaxBackoff
	}
	for i := 1; i < attempts; i++ {
		backoff *= 2
		if backoff >= s.cfg.MaxBackoff {
			backoff = s.cfg.MaxBackoff
			break
		}
	}
	return time.Now().UTC().Add(backoff)
}
