package chromereceiver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wxpusher"
)

type Config struct {
	CDPURL       string
	ExtensionID  string
	WxPusherHost string
	ReadyTimeout time.Duration
}

type Event struct {
	Kind    string
	Message string
}

type Receiver struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.Default()
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 60 * time.Second
	}
	if cfg.WxPusherHost == "" {
		cfg.WxPusherHost = "wxpusher.zjiecode.com"
	}
	return &Receiver{cfg: cfg, log: log}
}

func (r *Receiver) Run(ctx context.Context, onMessage func(wxpusher.Message), onEvent func(Event)) error {
	if strings.TrimSpace(r.cfg.CDPURL) == "" {
		return errors.New("chrome receiver requires cdp url")
	}
	if strings.TrimSpace(r.cfg.ExtensionID) == "" {
		return errors.New("chrome receiver requires extension id")
	}

	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, r.cfg.CDPURL)
	defer allocCancel()

	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()
	if err := chromedp.Run(browserCtx); err != nil {
		return fmt.Errorf("connect chrome cdp: %w", err)
	}

	info, err := r.waitForExtensionWorker(browserCtx)
	if err != nil {
		return err
	}
	if onEvent != nil {
		onEvent(Event{Kind: "chrome_receiver_worker_attached", Message: fmt.Sprintf("target=%s url=%s", info.TargetID, info.URL)})
	}

	workerCtx, workerCancel := chromedp.NewContext(browserCtx, chromedp.WithTargetID(info.TargetID))
	defer workerCancel()

	ready := make(chan struct{})
	frameState := newFrameState(r.cfg.WxPusherHost, func(msg wxpusher.Message) {
		if onMessage != nil {
			onMessage(msg)
		}
	})
	chromeReady := func() {
		select {
		case <-ready:
		default:
			close(ready)
		}
	}

	chromedp.ListenTarget(workerCtx, func(ev interface{}) {
		switch ev := ev.(type) {
		case *network.EventWebSocketCreated:
			if frameState.observeWebSocketCreated(string(ev.RequestID), ev.URL) && onEvent != nil {
				onEvent(Event{Kind: "chrome_receiver_ws_created", Message: ev.URL})
			}
		case *network.EventWebSocketWillSendHandshakeRequest:
			if frameState.observeHandshake(string(ev.RequestID), ev.Request.Headers) {
				chromeReady()
				if onEvent != nil {
					data, _ := json.Marshal(redactedHeaders(ev.Request.Headers))
					onEvent(Event{Kind: "chrome_receiver_ws_handshake", Message: string(data)})
				}
			}
		case *network.EventWebSocketHandshakeResponseReceived:
			if frameState.isTracked(string(ev.RequestID)) && onEvent != nil {
				onEvent(Event{Kind: "chrome_receiver_ws_response", Message: fmt.Sprintf("status=%d", ev.Response.Status)})
			}
		case *network.EventWebSocketFrameReceived:
			if frameState.handleFrame(string(ev.RequestID), ev.Response) {
				chromeReady()
			}
		case *network.EventWebSocketClosed:
			if frameState.isTracked(string(ev.RequestID)) && onEvent != nil {
				onEvent(Event{Kind: "chrome_receiver_ws_closed", Message: string(ev.RequestID)})
			}
		case *network.EventWebSocketFrameError:
			if frameState.isTracked(string(ev.RequestID)) && onEvent != nil {
				onEvent(Event{Kind: "chrome_receiver_ws_frame_error", Message: ev.ErrorMessage})
			}
		}
	})

	if err := chromedp.Run(workerCtx, network.Enable()); err != nil {
		return fmt.Errorf("enable chrome extension network events: %w", err)
	}

	timer := time.NewTimer(r.cfg.ReadyTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		if onEvent != nil {
			onEvent(Event{Kind: "chrome_receiver_ready", Message: "observed wxpusher websocket"})
		}
	case <-timer.C:
		return fmt.Errorf("chrome receiver did not observe wxpusher websocket within %s", r.cfg.ReadyTimeout)
	}

	<-ctx.Done()
	return ctx.Err()
}

func (r *Receiver) waitForExtensionWorker(ctx context.Context) (*target.Info, error) {
	deadline := time.Now().Add(r.cfg.ReadyTimeout)
	for {
		targets, err := chromedp.Targets(ctx)
		if err != nil {
			return nil, fmt.Errorf("list chrome targets: %w", err)
		}
		if info, ok := findExtensionWorker(targets, r.cfg.ExtensionID); ok {
			return info, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("chrome extension worker %s not found", r.cfg.ExtensionID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func findExtensionWorker(targets []*target.Info, extensionID string) (*target.Info, bool) {
	prefix := "chrome-extension://" + extensionID + "/"
	for _, info := range targets {
		if info == nil {
			continue
		}
		if !strings.Contains(info.Type, "worker") {
			continue
		}
		if strings.HasPrefix(info.URL, prefix) {
			return info, true
		}
	}
	return nil, false
}

type frameState struct {
	host     string
	requests map[string]struct{}
	onMsg    func(wxpusher.Message)
}

func newFrameState(host string, onMsg func(wxpusher.Message)) *frameState {
	return &frameState{
		host:     host,
		requests: make(map[string]struct{}),
		onMsg:    onMsg,
	}
}

func (s *frameState) observeWebSocketCreated(requestID, rawURL string) bool {
	if !strings.Contains(rawURL, s.host) {
		return false
	}
	s.requests[requestID] = struct{}{}
	return true
}

func (s *frameState) observeHandshake(requestID string, headers network.Headers) bool {
	if _, ok := s.requests[requestID]; ok {
		return true
	}
	if headerContains(headers, "Host", s.host) || headerContains(headers, ":authority", s.host) {
		s.requests[requestID] = struct{}{}
		return true
	}
	return false
}

func (s *frameState) isTracked(requestID string) bool {
	_, ok := s.requests[requestID]
	return ok
}

func (s *frameState) handleFrame(requestID string, frame *network.WebSocketFrame) bool {
	if frame == nil || frame.Opcode != 1 {
		return false
	}
	msg, err := wxpusher.ParseMessage([]byte(frame.PayloadData))
	if err != nil {
		return false
	}
	if msg.Type == wxpusher.MsgTypeHeart {
		return true
	}
	if !isForwardedMessageType(msg.Type) {
		return false
	}
	if _, ok := s.requests[requestID]; !ok && len(s.requests) > 0 {
		return false
	}
	if s.onMsg != nil {
		s.onMsg(msg)
	}
	return true
}

func isForwardedMessageType(msgType int) bool {
	switch msgType {
	case wxpusher.MsgTypeInit, wxpusher.MsgTypeError, wxpusher.MsgTypeUpdate, wxpusher.MsgTypeNotification:
		return true
	default:
		return false
	}
}

func redactedHeaders(headers network.Headers) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if isSensitiveHeader(key) {
			out[key] = "[redacted]"
			continue
		}
		out[key] = fmt.Sprint(value)
	}
	return out
}

func isSensitiveHeader(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "cookie", "device-token", "devicetoken", "sec-websocket-key", "proxy-authorization":
		return true
	default:
		return false
	}
}

func headerContains(headers network.Headers, key, want string) bool {
	for k, value := range headers {
		if !strings.EqualFold(k, key) {
			continue
		}
		return strings.Contains(fmt.Sprint(value), want)
	}
	return false
}
