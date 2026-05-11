package wxpusher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func TestRegisterDeviceUsesExtensionHeaders(t *testing.T) {
	var received map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/device/register-device" {
			t.Fatalf("path = %s, want /api/device/register-device", r.URL.Path)
		}
		if got := r.Header.Get("deviceToken"); got != "dt" {
			t.Fatalf("deviceToken header = %q, want dt", got)
		}
		if got := r.Header.Get("platform"); got != "Chrome-Linux" {
			t.Fatalf("platform header = %q, want Chrome-Linux", got)
		}
		if got := r.Header.Get("version"); got != "1.1.0" {
			t.Fatalf("version header = %q, want 1.1.0", got)
		}
		if got := r.Header.Get("Cookie"); got != "" {
			t.Fatalf("Cookie header = %q, want empty", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1000,"data":{"deviceUuid":"du","deviceToken":"newdt"}}`))
	}))
	defer server.Close()

	id := identity.Identity{
		DeviceUUID:  "du",
		DeviceToken: "dt",
		PushToken:   "pt",
		Platform:    "Chrome-Linux",
		Version:     "1.1.0",
		Source:      "json",
	}

	got, err := NewHTTPClient(server.URL, server.Client()).UpdatePushToken(context.Background(), id, "pt2")
	if err != nil {
		t.Fatalf("UpdatePushToken returned error: %v", err)
	}
	if received["pushToken"] != "pt2" {
		t.Fatalf("pushToken body = %q, want pt2", received["pushToken"])
	}
	if received["deviceUuid"] != "du" {
		t.Fatalf("deviceUuid body = %q, want du", received["deviceUuid"])
	}
	if got.DeviceToken != "newdt" {
		t.Fatalf("DeviceToken = %q, want newdt", got.DeviceToken)
	}
	if got.PushToken != "pt2" {
		t.Fatalf("PushToken = %q, want pt2", got.PushToken)
	}
	if got.DeviceUUID != "du" {
		t.Fatalf("DeviceUUID = %q, want du", got.DeviceUUID)
	}
	if got.Platform != id.Platform || got.Version != id.Version || got.Source != id.Source {
		t.Fatalf("identity metadata changed: got %#v, original %#v", got, id)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt was not set")
	}
}

func TestUpdatePushTokenDoesNotSendCookieFromJar(t *testing.T) {
	var cookieHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieHeader = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1000,"data":{"deviceUuid":"du","deviceToken":"newdt"}}`))
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{{Name: "session", Value: "leaked"}})

	httpClient := server.Client()
	httpClient.Jar = jar

	id := identity.Identity{
		DeviceUUID:  "du",
		DeviceToken: "dt",
		Platform:    "Chrome-Linux",
		Version:     "1.1.0",
	}
	if _, err := NewHTTPClient(server.URL, httpClient).UpdatePushToken(context.Background(), id, "pt2"); err != nil {
		t.Fatalf("UpdatePushToken returned error: %v", err)
	}
	if cookieHeader != "" {
		t.Fatalf("Cookie header = %q, want empty", cookieHeader)
	}
}

func TestUpdatePushTokenRejectsHTTPErrorStatusWithBoundedBody(t *testing.T) {
	body := strings.Repeat("x", 4096)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, body, http.StatusInternalServerError)
	}))
	defer server.Close()

	id := identity.Identity{
		DeviceUUID:  "du",
		DeviceToken: "dt",
		Platform:    "Chrome-Linux",
		Version:     "1.1.0",
	}
	_, err := NewHTTPClient(server.URL, server.Client()).UpdatePushToken(context.Background(), id, "pt2")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %q, want HTTP status", err)
	}
	if len(err.Error()) > 1400 {
		t.Fatalf("error length = %d, want bounded response snippet", len(err.Error()))
	}
}

func TestWSClientStopsHeartbeatOnRunReturn(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer server.Close()

	tlsConfig := websocket.DefaultDialer.TLSClientConfig
	websocket.DefaultDialer.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig
	t.Cleanup(func() {
		websocket.DefaultDialer.TLSClientConfig = tlsConfig
	})

	host := strings.TrimPrefix(server.URL, "https://")
	before := runtime.NumGoroutine()
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})

	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		err := (&WSClient{
			Host: host,
			Identity: identity.Identity{
				Platform: "Chrome-Linux",
				Version:  "1.1.0",
			},
		}).Run(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want websocket close error", err)
		}
	}

	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before+4 {
		t.Fatalf("goroutines after closed websocket runs = %d, before = %d; heartbeat goroutines likely leaked", after, before)
	}
}
