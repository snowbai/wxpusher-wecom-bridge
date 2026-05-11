package wxpusher

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/hhh/wxpusher-wecom-bridge/internal/identity"
)

func TestBuildWebSocketURLMatchesExtensionOrder(t *testing.T) {
	id := identity.Identity{
		PushToken: "pt",
		Platform:  "Chrome-Linux",
		Version:   "1.1.0",
	}

	got := BuildWebSocketURL("wxpusher.zjiecode.com", id)
	want := "wss://wxpusher.zjiecode.com/ws?version=1.1.0&platform=Chrome-Linux&pushToken=pt"
	if got != want {
		t.Fatalf("BuildWebSocketURL() = %q, want %q", got, want)
	}
}

func TestBuildHTTPHeadersMatchesExtensionHeaders(t *testing.T) {
	id := identity.Identity{
		DeviceToken: "dt",
		Platform:    "Chrome-Linux",
		Version:     "1.1.0",
	}

	got := BuildHTTPHeaders(id)
	want := http.Header{
		"Platform":     []string{"Chrome-Linux"},
		"Version":      []string{"1.1.0"},
		"Devicetoken":  []string{"dt"},
		"Content-Type": []string{"application/json;charset=UTF-8"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildHTTPHeaders() = %#v, want %#v", got, want)
	}
	if got.Get("Cookie") != "" {
		t.Fatalf("Cookie header = %q, want empty", got.Get("Cookie"))
	}
}

func TestParseMessageParsesNotificationAndPreservesRaw(t *testing.T) {
	raw := []byte(`{"msgType":20001,"content":"hello","qid":"q1"}`)

	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage returned error: %v", err)
	}
	if msg.Type != MsgTypeNotification {
		t.Fatalf("Type = %d, want %d", msg.Type, MsgTypeNotification)
	}
	if msg.Content != "hello" {
		t.Fatalf("Content = %q, want hello", msg.Content)
	}
	if msg.QID != "q1" {
		t.Fatalf("QID = %q, want q1", msg.QID)
	}
	if !json.Valid(msg.Raw) || string(msg.Raw) != string(raw) {
		t.Fatalf("Raw = %s, want %s", msg.Raw, raw)
	}
}
