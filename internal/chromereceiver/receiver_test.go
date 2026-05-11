package chromereceiver

import (
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/hhh/wxpusher-wecom-bridge/internal/wxpusher"
)

func TestFindExtensionWorkerMatchesConfiguredExtensionID(t *testing.T) {
	targets := []*target.Info{
		{TargetID: "page", Type: "page", URL: "https://example.com"},
		{TargetID: "other-worker", Type: "service_worker", URL: "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/background.js"},
		{TargetID: "wanted", Type: "service_worker", URL: "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/background.js"},
	}

	got, ok := findExtensionWorker(targets, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if !ok {
		t.Fatal("expected extension worker")
	}
	if got.TargetID != "wanted" {
		t.Fatalf("target id = %s, want wanted", got.TargetID)
	}
}

func TestHandleWebSocketFrameForwardsOnlyWxPusherMessages(t *testing.T) {
	var got []wxpusher.Message
	state := newFrameState("wxpusher.zjiecode.com", func(msg wxpusher.Message) {
		got = append(got, msg)
	})
	state.observeWebSocketCreated("ws1", "wss://wxpusher.zjiecode.com/ws?version=1.1.0&platform=Chrome-Mac")
	state.handleFrame("ws1", &network.WebSocketFrame{Opcode: 1, PayloadData: `{"msgType":201}`})
	state.handleFrame("ws1", &network.WebSocketFrame{Opcode: 1, PayloadData: `{"msgType":20001,"qid":"q1","content":"hello"}`})
	state.handleFrame("ws1", &network.WebSocketFrame{Opcode: 1, PayloadData: `not json`})

	if len(got) != 1 {
		t.Fatalf("forwarded messages = %d, want 1", len(got))
	}
	if got[0].Type != wxpusher.MsgTypeNotification || got[0].QID != "q1" {
		t.Fatalf("forwarded message = %#v", got[0])
	}
}

func TestHeaderSnapshotRedactsSecrets(t *testing.T) {
	snapshot := redactedHeaders(network.Headers{
		"User-Agent":            "Mozilla/5.0",
		"Cookie":                "session=secret",
		"Sec-WebSocket-Key":     "abc",
		"Sec-WebSocket-Version": "13",
		"deviceToken":           "dt-secret",
	})

	if snapshot["User-Agent"] != "Mozilla/5.0" {
		t.Fatalf("user agent = %q", snapshot["User-Agent"])
	}
	if snapshot["Cookie"] != "[redacted]" {
		t.Fatalf("cookie = %q, want redacted", snapshot["Cookie"])
	}
	if snapshot["Sec-WebSocket-Key"] != "[redacted]" {
		t.Fatalf("ws key = %q, want redacted", snapshot["Sec-WebSocket-Key"])
	}
	if snapshot["deviceToken"] != "[redacted]" {
		t.Fatalf("deviceToken = %q, want redacted", snapshot["deviceToken"])
	}
}
