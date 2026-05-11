package wxpusher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
