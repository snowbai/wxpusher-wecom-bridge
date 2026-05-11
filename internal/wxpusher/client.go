package wxpusher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

type HTTPClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPClient(baseURL string, client *http.Client) *HTTPClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPClient{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (c *HTTPClient) UpdatePushToken(ctx context.Context, id identity.Identity, pushToken string) (identity.Identity, error) {
	body, err := json.Marshal(map[string]string{
		"pushToken":  pushToken,
		"deviceUuid": id.DeviceUUID,
	})
	if err != nil {
		return identity.Identity{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/device/register-device", bytes.NewReader(body))
	if err != nil {
		return identity.Identity{}, err
	}
	req.Header = BuildHTTPHeaders(id)

	resp, err := c.client.Do(req)
	if err != nil {
		return identity.Identity{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return identity.Identity{}, err
	}
	var registerResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			DeviceUUID  string `json:"deviceUuid"`
			DeviceToken string `json:"deviceToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &registerResp); err != nil {
		return identity.Identity{}, err
	}
	if registerResp.Code != 1000 {
		if registerResp.Msg == "" {
			registerResp.Msg = string(data)
		}
		return identity.Identity{}, fmt.Errorf("wxpusher register device failed: code=%d msg=%s", registerResp.Code, registerResp.Msg)
	}

	next := id
	next.PushToken = pushToken
	if registerResp.Data.DeviceUUID != "" {
		next.DeviceUUID = registerResp.Data.DeviceUUID
	}
	if registerResp.Data.DeviceToken != "" {
		next.DeviceToken = registerResp.Data.DeviceToken
	}
	next.UpdatedAt = time.Now().UTC()
	return next, nil
}

type Logger interface {
	Printf(format string, v ...any)
}

type WSClient struct {
	Host      string
	Identity  identity.Identity
	Logger    Logger
	OnMessage func(Message)
}

func (c *WSClient) Run(ctx context.Context) error {
	if c.Host == "" {
		return errors.New("wxpusher host is required")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, BuildWebSocketURL(c.Host, c.Identity), nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	ticker := time.NewTicker(26 * time.Second)
	defer ticker.Stop()

	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.TextMessage, HeartbeatPayload()); err != nil {
					writeErr <- err
					return
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writeErr:
			return err
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		msg, err := ParseMessage(data)
		if err != nil {
			if c.Logger != nil {
				c.Logger.Printf("parse wxpusher message: %v", err)
			}
			continue
		}
		switch msg.Type {
		case MsgTypeInit, MsgTypeUpdate, MsgTypeNotification:
			if c.OnMessage != nil {
				c.OnMessage(msg)
			}
		}
	}
}
