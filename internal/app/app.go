package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hhh/wxpusher-wecom-bridge/internal/browser"
	"github.com/hhh/wxpusher-wecom-bridge/internal/chromereceiver"
	"github.com/hhh/wxpusher-wecom-bridge/internal/config"
	"github.com/hhh/wxpusher-wecom-bridge/internal/dispatch"
	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
	"github.com/hhh/wxpusher-wecom-bridge/internal/store"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wecom"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wxpusher"
)

const (
	workerPollInterval = time.Second
	appEventTimeout    = 5 * time.Second
	pushTokenTimeout   = 15 * time.Second
)

type pushTokenUpdater interface {
	UpdatePushToken(context.Context, identity.Identity, string) (identity.Identity, error)
}

type chromeReceiver interface {
	Run(context.Context, func(wxpusher.Message), func(chromereceiver.Event)) error
}

type receiverFunc func(context.Context, func(wxpusher.Message), func(chromereceiver.Event)) error

func (f receiverFunc) Run(ctx context.Context, onMessage func(wxpusher.Message), onEvent func(chromereceiver.Event)) error {
	return f(ctx, onMessage, onEvent)
}

type App struct {
	cfg            config.Config
	log            *slog.Logger
	st             store.Store
	wxHTTP         pushTokenUpdater
	chromeReceiver chromeReceiver
}

func New(cfg config.Config, log *slog.Logger, st store.Store) *App {
	if log == nil {
		log = slog.Default()
	}
	return &App{
		cfg:    cfg,
		log:    log,
		st:     st,
		wxHTTP: wxpusher.NewHTTPClient(httpBaseURL(cfg.WxPusher.Host), &http.Client{Timeout: pushTokenTimeout}),
	}
}

func Backoff(cfg config.Config, attempts int) time.Duration {
	initial := time.Duration(cfg.Retry.InitialBackoffSeconds) * time.Second
	max := time.Duration(cfg.Retry.MaxBackoffSeconds) * time.Second
	if initial <= 0 {
		initial = 2 * time.Second
	}
	if max <= 0 {
		max = 60 * time.Second
	}
	if max < initial {
		max = initial
	}

	d := initial
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

func (a *App) Run(ctx context.Context) error {
	if a.st == nil {
		return errors.New("app requires store")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	dispatchSvc, workerErrs := a.startDispatch(runCtx)
	if a.cfg.Receiver.Mode == "chrome-cdp" {
		return a.runChromeReceiver(runCtx, cancel, dispatchSvc, workerErrs)
	}
	return a.runGoFallback(runCtx, cancel, dispatchSvc, workerErrs)
}

func (a *App) startDispatch(ctx context.Context) (*dispatch.Service, chan error) {
	sender := wecom.New(a.cfg.WeCom.WebhookURL, nil)
	var fetcher dispatch.Fetcher
	if a.cfg.Browser.Enabled {
		fetcher = browserDispatchFetcher{fetcher: browser.NewFetcher(browser.Config{
			Enabled:         a.cfg.Browser.Enabled,
			ChromePath:      a.cfg.Browser.ChromePath,
			ScreenshotDir:   a.cfg.Browser.ScreenshotDir,
			Timeout:         time.Duration(a.cfg.Browser.TimeoutSeconds) * time.Second,
			SummaryMaxChars: a.cfg.Browser.SummaryMaxChars,
		})}
	}
	dispatchSvc := dispatch.New(a.st, sender, fetcher, dispatch.Config{
		WeComMaxAttempts:      a.cfg.Retry.WeComMaxAttempts,
		EnrichmentMaxAttempts: a.cfg.Retry.EnrichmentMaxAttempts,
		InitialBackoff:        time.Duration(a.cfg.Retry.InitialBackoffSeconds) * time.Second,
		MaxBackoff:            time.Duration(a.cfg.Retry.MaxBackoffSeconds) * time.Second,
	})

	workerErrs := make(chan error, 2)
	startWorker(workerErrs, "delivery", func() error {
		return dispatchSvc.RunDeliveryWorker(ctx, workerPollInterval)
	})
	if a.cfg.Browser.Enabled {
		startWorker(workerErrs, "enrichment", func() error {
			return dispatchSvc.RunEnrichmentWorker(ctx, workerPollInterval)
		})
	}
	return dispatchSvc, workerErrs
}

func (a *App) runChromeReceiver(ctx context.Context, cancel context.CancelFunc, dispatchSvc *dispatch.Service, workerErrs <-chan error) error {
	receiver := a.chromeReceiver
	if receiver == nil {
		receiver = chromereceiver.New(chromereceiver.Config{
			CDPURL:       a.cfg.Receiver.CDPURL,
			ExtensionID:  a.cfg.Receiver.ExtensionID,
			WxPusherHost: wsHost(a.cfg.WxPusher.Host),
			ReadyTimeout: time.Duration(a.cfg.Receiver.ReadyTimeoutSeconds) * time.Second,
		}, a.log)
	}

	receiverErr := make(chan error, 1)
	go func() {
		receiverErr <- receiver.Run(ctx, func(msg wxpusher.Message) {
			a.handleObservedWxMessage(ctx, msg, dispatchSvc)
		}, func(event chromereceiver.Event) {
			a.log.Info("chrome receiver event", "kind", event.Kind, "message", event.Message)
			a.recordAppEvent(event.Kind, event.Message)
		})
	}()

	select {
	case err := <-workerErrs:
		cancel()
		return err
	case <-ctx.Done():
		return ctx.Err()
	case err := <-receiverErr:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			a.recordAppEvent("chrome_receiver_stopped", err.Error())
		}
		return err
	}
}

func (a *App) runGoFallback(ctx context.Context, cancel context.CancelFunc, dispatchSvc *dispatch.Service, workerErrs <-chan error) error {
	id, err := a.st.LoadIdentity(ctx)
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	id = id.WithDefaults(a.cfg.WxPusher.Platform, a.cfg.WxPusher.Version, id.Source)
	if err := id.Validate(); err != nil {
		return fmt.Errorf("stored identity is invalid: %w", err)
	}

	var idMu sync.Mutex
	attempts := 1
	for {
		runID := copyIdentity(&idMu, &id)
		wsErr := make(chan error, 1)
		wsClient := &wxpusher.WSClient{
			Host:     wsHost(a.cfg.WxPusher.Host),
			Identity: runID,
			Logger:   slogPrintfAdapter{log: a.log},
			OnMessage: func(msg wxpusher.Message) {
				a.handleWxMessage(ctx, msg, dispatchSvc, &idMu, &id)
			},
		}
		go func() {
			wsErr <- wsClient.Run(ctx)
		}()

		select {
		case err := <-workerErrs:
			cancel()
			return err
		case <-ctx.Done():
			return ctx.Err()
		case err := <-wsErr:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.recordWebSocketClose(runID, err)
		}

		delay := Backoff(a.cfg, attempts)
		attempts++
		if err := a.waitBeforeReconnect(ctx, workerErrs, delay); err != nil {
			cancel()
			return err
		}
	}
}

func (a *App) handleObservedWxMessage(ctx context.Context, msg wxpusher.Message, svc *dispatch.Service) {
	switch msg.Type {
	case wxpusher.MsgTypeInit:
		message := "observed wxpusher init message from Chrome extension"
		if msg.PushToken != "" {
			message += " pushToken=" + config.RedactSecret(msg.PushToken)
		}
		a.recordAppEvent("wxpusher_init_observed", message)
	case wxpusher.MsgTypeError:
		message := strings.TrimSpace(strings.Join([]string{msg.Title, msg.Content, msg.URL}, " "))
		if message == "" {
			message = "wxpusher error message observed"
		}
		a.log.Error("wxpusher error observed", "message", message)
		a.recordAppEvent("wxpusher_error_observed", message)
	case wxpusher.MsgTypeUpdate:
		message := strings.TrimSpace(strings.Join([]string{msg.Title, msg.Content, msg.URL}, " "))
		if message == "" {
			message = "wxpusher version update message received"
		}
		a.log.Warn("wxpusher version update", "message", message)
		a.recordAppEvent("wxpusher_version_update", message)
	case wxpusher.MsgTypeNotification:
		if err := svc.HandleMessage(ctx, dispatch.IncomingMessage{
			QID:        msg.QID,
			MsgType:    msg.Type,
			Content:    msg.Content,
			RawPayload: msg.Raw,
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			a.log.Error("handle wxpusher notification failed", "err", err)
			a.recordAppEvent("wxpusher_notification_failed", err.Error())
		}
	}
}

func (a *App) handleWxMessage(ctx context.Context, msg wxpusher.Message, svc *dispatch.Service, idMu *sync.Mutex, id *identity.Identity) {
	switch msg.Type {
	case wxpusher.MsgTypeInit:
		a.handleInitMessage(ctx, msg, idMu, id)
	case wxpusher.MsgTypeUpdate:
		message := strings.TrimSpace(strings.Join([]string{msg.Title, msg.Content, msg.URL}, " "))
		if message == "" {
			message = "wxpusher version update message received"
		}
		a.log.Warn("wxpusher version update", "message", message)
		a.recordAppEvent("wxpusher_version_update", message)
	case wxpusher.MsgTypeNotification:
		if err := svc.HandleMessage(ctx, dispatch.IncomingMessage{
			QID:        msg.QID,
			MsgType:    msg.Type,
			Content:    msg.Content,
			RawPayload: msg.Raw,
			ReceivedAt: time.Now().UTC(),
		}); err != nil {
			a.log.Error("handle wxpusher notification failed", "err", err)
			a.recordAppEvent("wxpusher_notification_failed", err.Error())
		}
	}
}

func (a *App) handleInitMessage(ctx context.Context, msg wxpusher.Message, idMu *sync.Mutex, id *identity.Identity) {
	if strings.TrimSpace(msg.PushToken) == "" {
		a.log.Warn("wxpusher init message missing push token")
		a.recordAppEvent("wxpusher_push_token_missing", "msgType=202 did not include pushToken")
		return
	}

	current := copyIdentity(idMu, id)
	updateCtx, cancel := context.WithTimeout(ctx, pushTokenTimeout)
	defer cancel()
	next, err := a.wxHTTP.UpdatePushToken(updateCtx, current, msg.PushToken)
	if err != nil {
		message := redactIdentitySecrets(err.Error(), current, msg.PushToken)
		a.log.Error("wxpusher push token update failed", "err", message)
		a.recordAppEvent("wxpusher_push_token_update_failed", message)
		return
	}
	if err := a.st.SaveIdentity(updateCtx, next); err != nil {
		message := redactIdentitySecrets(err.Error(), next, msg.PushToken)
		a.log.Error("save wxpusher identity failed", "err", message)
		a.recordAppEvent("wxpusher_identity_save_failed", message)
		return
	}

	idMu.Lock()
	*id = next
	idMu.Unlock()
	a.log.Info("wxpusher push token updated", "device_uuid", next.DeviceUUID)
}

func (a *App) recordWebSocketClose(id identity.Identity, err error) {
	message := "wxpusher websocket closed"
	if err != nil {
		message = redactIdentitySecrets(err.Error(), id)
	}
	a.log.Warn("wxpusher websocket disconnected", "err", message)
	a.recordAppEvent("wxpusher_websocket_disconnected", message)
}

func (a *App) waitBeforeReconnect(ctx context.Context, workerErrs <-chan error, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case err := <-workerErrs:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *App) recordAppEvent(kind, message string) {
	if a.st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), appEventTimeout)
	defer cancel()
	if err := a.st.AddAppEvent(ctx, store.AppEvent{Kind: kind, Message: message, CreatedAt: time.Now().UTC()}); err != nil {
		a.log.Error("record app event failed", "kind", kind, "err", err)
	}
}

type browserDispatchFetcher struct {
	fetcher *browser.Fetcher
}

func (f browserDispatchFetcher) Fetch(ctx context.Context, pageURL string) (dispatch.EnrichmentResult, error) {
	result, err := f.fetcher.Fetch(ctx, pageURL)
	if err != nil {
		return dispatch.EnrichmentResult{}, err
	}
	return dispatch.EnrichmentResult{
		URL:        result.URL,
		Title:      result.Title,
		Summary:    result.Summary,
		Screenshot: result.Screenshot,
	}, nil
}

type slogPrintfAdapter struct {
	log *slog.Logger
}

func (l slogPrintfAdapter) Printf(format string, args ...any) {
	if l.log != nil {
		l.log.Info(fmt.Sprintf(format, args...))
	}
}

func reportWorkerError(ch chan<- error, name string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	select {
	case ch <- fmt.Errorf("%s worker: %w", name, err):
	default:
	}
}

func startWorker(ch chan<- error, name string, run func() error) {
	go func() {
		reportWorkerError(ch, name, run())
	}()
}

func copyIdentity(mu *sync.Mutex, id *identity.Identity) identity.Identity {
	mu.Lock()
	defer mu.Unlock()
	return *id
}

func httpBaseURL(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimRight(host, "/")
	}
	return "https://" + strings.TrimRight(host, "/")
}

func wsHost(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		parsed, err := url.Parse(host)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return strings.TrimRight(host, "/")
}

func redactIdentitySecrets(text string, id identity.Identity, extra ...string) string {
	secrets := []string{id.DeviceToken, id.PushToken}
	secrets = append(secrets, extra...)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, config.RedactSecret(secret))
	}
	return text
}
